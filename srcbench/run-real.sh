#!/usr/bin/env bash
# 启动真实靶场并等待 HTTP 就绪；随后按 srcbench/targets.real.json 建 SRC 任务。
set -euo pipefail
cd "$(dirname "$0")/.."

wait_http() {
  local name="$1" url="$2"
  for _ in $(seq 1 60); do
    if curl -fsS -o /dev/null "$url" 2>/dev/null; then
      echo "[+] $name 就绪：$url"
      return 0
    fi
    sleep 2
  done
  echo "[x] $name 在 120s 内未就绪：$url" >&2
  return 1
}

docker compose -f srcbench/real/docker-compose.yml up -d
wait_http "Juice Shop" "http://127.0.0.1:3000/"
wait_http "DVWA" "http://127.0.0.1:8080/login.php"

cat <<'TIP'

[+] 靶场已就绪。DVWA 首次使用：
    1. 打开 http://127.0.0.1:8080/setup.php 点 Create / Reset Database；
    2. 用 admin / password 登录，进入 DVWA Security 把等级设为 low；
    3. 金标见 srcbench/targets.real.json。

    在 BLADEPRO 里分别对下面两个资产各建一个 SRC 任务：
      http://127.0.0.1:3000   （Juice Shop）
      http://127.0.0.1:8080   （DVWA）
    任务结束后用 srcbench/score 对 targets.real.json 打分。

    销毁：docker compose -f srcbench/real/docker-compose.yml down -v
TIP
