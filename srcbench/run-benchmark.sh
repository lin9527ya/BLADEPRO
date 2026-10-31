#!/usr/bin/env bash
# 端到端 SRC 基准 runner：健康检查 → 建固定预算任务 → 等终态 → 分页导出 → 打分。
#
# 只允许本机/私网靶场；不要把生产站点写进来。需要先启动靶场：
#   srcbench/run-target.sh
#
# 环境变量：
#   BLADEPRO_BASE_URL   默认 http://127.0.0.1:9898
#   BLADEPRO_TOKEN      已有登录 token；否则用 BLADEPRO_USERNAME / BLADEPRO_PASSWORD 登录
#   TARGET_URL          被测靶场，默认 http://127.0.0.1:18080（必须是 loopback/私网）
#   GOLDEN              金标文件，默认 srcbench/targets.json
#   BENCH_TARGET_NAME   scorer 的 target 名，默认 local-vuln
#   BENCH_TIMEOUT_SECONDS / BENCH_MAX_INTENTS / BENCH_MAX_INTENTS_PER_ASSET
#   BENCH_MODEL         仅写入清单，便于人工固定模型配置
#   BENCH_ALLOW_NONLOCAL=1  显式允许非本机靶场（默认拒绝）
set -euo pipefail
cd "$(dirname "$0")/.."

BASE="${BLADEPRO_BASE_URL:-http://127.0.0.1:9898}"
TARGET="${TARGET_URL:-http://127.0.0.1:18080}"
GOLDEN="${GOLDEN:-srcbench/targets.json}"
TARGET_NAME="${BENCH_TARGET_NAME:-local-vuln}"
TIMEOUT="${BENCH_TIMEOUT_SECONDS:-1800}"
MAX_INTENTS="${BENCH_MAX_INTENTS:-80}"
MAX_PER_ASSET="${BENCH_MAX_INTENTS_PER_ASSET:-12}"
MODEL="${BENCH_MODEL:-unknown}"

if [[ "${BENCH_ALLOW_NONLOCAL:-0}" != "1" ]]; then
  host="$(printf '%s' "$TARGET" | sed -E 's#^[a-zA-Z]+://([^/:]+).*#\1#')"
  case "$host" in
    127.*|localhost|::1|10.*|192.168.*|172.16.*|172.17.*|172.18.*|172.19.*|172.2[0-9].*|172.3[0-1].*) ;;
    *) echo "拒绝非本机/私网靶场：$TARGET（确需如此请显式 BENCH_ALLOW_NONLOCAL=1）" >&2; exit 2 ;;
  esac
fi

command -v jq >/dev/null || { echo "需要 jq" >&2; exit 2; }
command -v curl >/dev/null || { echo "需要 curl" >&2; exit 2; }

auth=()
if [[ -n "${BLADEPRO_TOKEN:-}" ]]; then
  auth=(-H "Authorization: Bearer ${BLADEPRO_TOKEN}")
else
  : "${BLADEPRO_USERNAME:?需要 BLADEPRO_TOKEN 或 BLADEPRO_USERNAME}"
  : "${BLADEPRO_PASSWORD:?需要 BLADEPRO_TOKEN 或 BLADEPRO_PASSWORD}"
  token="$(curl -fsS -H 'Content-Type: application/json' \
    -d "$(jq -n --arg u "$BLADEPRO_USERNAME" --arg p "$BLADEPRO_PASSWORD" '{username:$u,password:$p}')" \
    "$BASE/api/auth/login" | jq -r '.token')"
  auth=(-H "Authorization: Bearer ${token}")
fi

api() { curl -fsS "${auth[@]}" -H 'Content-Type: application/json' "$@"; }

api "$BASE/api/health" >/dev/null
echo "[+] 健康检查通过：$BASE"

run_dir="srcbench/runs/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$run_dir"
mode_doc="$(jq -n \
  --argjson timeout "$TIMEOUT" \
  --argjson max_total "$MAX_INTENTS" \
  --argjson max_per_asset "$MAX_PER_ASSET" \
  '{mode:"src",max_intents_total:$max_total,max_intents_per_asset:$max_per_asset,timeout_seconds:$timeout,delivery_reserve_pct:10}')"
payload="$(jq -n \
  --arg name "bench-$(date +%H%M%S)" \
  --arg desc "本地授权基准靶场：$TARGET" \
  --arg goal "在授权靶场中发现并验证可复现漏洞" \
  --argjson timeout "$TIMEOUT" \
  --argjson mode_doc "$mode_doc" \
  '{name:$name,description:$desc,goal:$goal,mode:"src",mode_doc:$mode_doc,timeout_seconds:$timeout,coverage_enabled:true}')"
task_id="$(api -X POST -d "$payload" "$BASE/api/tasks" | jq -r '.id')"
[[ -n "$task_id" && "$task_id" != "null" ]] || { echo "建任务失败" >&2; exit 1; }
echo "[+] 任务 #$task_id 已创建"

terminal_status() {
  case "$1" in done|failed|timeout|policy_blocked) return 0 ;; *) return 1 ;; esac
}
status=""
for _ in $(seq 1 $((TIMEOUT / 5 + 60))); do
  task_json="$(api "$BASE/api/tasks/$task_id")"
  status="$(jq -r '.status // ""' <<<"$task_json")"
  if terminal_status "$status"; then break; fi
  sleep 5
done
if ! terminal_status "$status"; then
  echo "[x] 任务 #$task_id 未在预期时间内终态（当前 ${status:-未知}）；可手动暂停/继续后重跑导出" >&2
  exit 1
fi
echo "[+] 任务终态：$status"
printf '%s\n' "$task_json" > "$run_dir/task.json"

# 分页导出本任务原生发现（不要用不带分页的 task= 分支，它会混入关联任务）。
: > "$run_dir/findings.json"
page=1
while :; do
  chunk="$(api "$BASE/api/exploration/findings?task=$task_id&page=$page&limit=200")"
  count="$(jq -r '(.items // []) | length' <<<"$chunk")"
  jq -c '(.items // [])[]' <<<"$chunk" >> "$run_dir/findings.json"
  [[ "$count" -lt 200 ]] && break
  page=$((page + 1))
done
jq -s '.' "$run_dir/findings.json" > "$run_dir/findings.array.json"

api "$BASE/api/tasks/$task_id/src" > "$run_dir/src-full.json"
jq '.summary' "$run_dir/src-full.json" > "$run_dir/src.json"
no_output="$(jq -r '.intent_states.no_output // 0' "$run_dir/src-full.json")"
tokens="$(jq -r '((.tokens.input_tokens // 0) + (.tokens.output_tokens // 0))' <<<"$task_json")"
cache_tokens="$(jq -r '((.tokens.cache_read_tokens // 0) + (.tokens.cache_write_tokens // 0))' <<<"$task_json")"

jq -n \
  --arg base "$BASE" --arg target "$TARGET" --arg golden "$GOLDEN" \
  --arg target_name "$TARGET_NAME" --arg model "$MODEL" \
  --arg task_id "$task_id" --arg status "$status" \
  --argjson tokens "$tokens" --argjson cache_tokens "$cache_tokens" \
  --argjson mode_doc "$mode_doc" --argjson no_output "$no_output" --arg git_rev "$(git rev-parse HEAD 2>/dev/null || echo unknown)" \
  '{base_url:$base,target_url:$target,golden:$golden,target_name:$target_name,model:$model,task_id:$task_id,status:$status,tokens:$tokens,cache_tokens:$cache_tokens,no_output:$no_output,mode_doc:$mode_doc,created_at:(now|todate),git_rev:$git_rev}' \
  > "$run_dir/manifest.json"

go run ./srcbench/score \
  --target "$TARGET_NAME" --golden "$GOLDEN" \
  --findings "$run_dir/findings.array.json" \
  --src "$run_dir/src.json" \
  --tokens "$tokens" | tee "$run_dir/score.md"

echo "[+] 产物：$run_dir"
