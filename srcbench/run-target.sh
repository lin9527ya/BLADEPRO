#!/usr/bin/env bash
# 启动本地金标靶场（授权基准测试用）。
set -euo pipefail
cd "$(dirname "$0")/.."
exec go run ./srcbench/target -addr "${1:-127.0.0.1:18080}"
