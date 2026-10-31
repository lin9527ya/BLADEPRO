#!/usr/bin/env bash
# Compare a scorer --json result against a saved baseline. Fails on material
# regression; this is intentionally a human-run gate, not a default CI LLM job.
#
#   go run ./srcbench/score ... --json > current.json
#   srcbench/compare-baseline.sh current.json baseline.json
#
# BENCH_REGRESSION_RATIO=0.90 允许指标降到基线的 90%；Token/TP 反向判定。
set -euo pipefail
current="${1:?usage: compare-baseline.sh current.json baseline.json}"
baseline="${2:?usage: compare-baseline.sh current.json baseline.json}"
ratio="${BENCH_REGRESSION_RATIO:-0.90}"
command -v jq >/dev/null || { echo "需要 jq" >&2; exit 2; }

fail=0
for metric in precision recall f1 coverage_rate check_rate; do
  cur="$(jq -r ".${metric} // 0" "$current")"
  base="$(jq -r ".${metric} // 0" "$baseline")"
  if awk -v c="$cur" -v b="$base" -v r="$ratio" 'BEGIN { exit !(b > 0 && c < b * r) }'; then
    echo "[x] ${metric} 回退：${cur} < ${base} × ${ratio}" >&2
    fail=1
  fi
done
cur_tp="$(jq -r '.tokens_per_tp // 0' "$current")"
base_tp="$(jq -r '.tokens_per_tp // 0' "$baseline")"
if awk -v c="$cur_tp" -v b="$base_tp" -v r="$ratio" 'BEGIN { exit !(b > 0 && c > b / r) }'; then
  echo "[x] tokens_per_tp 回退：${cur_tp} > ${base_tp} ÷ ${ratio}" >&2
  fail=1
fi
if [[ "$fail" -ne 0 ]]; then
  echo "基准回退（允许比例 ${ratio}）" >&2
  exit 1
fi
echo "[+] 基准未回退（允许比例 ${ratio}）"
