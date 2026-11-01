#!/usr/bin/env bash
# crtsh.sh — CT 证书透明子域拉取(crt.sh → certspotter → HackerTarget 自动换路)
# 用法: bash crtsh.sh <domain> [输出文件]
# 国内网络 crt.sh 常被墙: 依次换路, 全失败退出码 1(跳过, 用测绘 cert 反查补)。
# Windows/Git Bash 兼容: python→python3 自动选择; mktemp 路径转 Windows 形式给原生 python。
set -u
DOMAIN="${1:?用法: bash crtsh.sh <domain> [输出文件]}"
OUT="${2:-}"

# 域名白名单校验(2026-09-27 加)。此前 $DOMAIN 被直接拼进 URL 与 `python -c "..."` 的**程序源码**,
# 一个单引号就能闭合字符串注入任意 Python; 含 & / 空格还会改写 curl 请求。
# 域名常来自上游列表(collect.py 直接透传), 不能假设它是干净的。
if ! printf '%s' "$DOMAIN" | grep -qE '^[A-Za-z0-9.-]+$'; then
  echo "[!] 非法域名: $DOMAIN(只允许字母/数字/点/连字符)" >&2
  exit 2
fi

TMP=$(mktemp)
# 原来只在成功/全失败两条路径手动 rm; Ctrl-C 或中途异常退出会残留临时文件。
trap 'rm -f "$TMP"' EXIT

# 解释器选择: Linux 有 python3; Windows Git Bash 的 python3 可能是 WindowsApps 商店 stub
# (command -v 找得到但执行静默失败) → 优先能真跑出版本的
PY=""
for c in python python3; do
  if command -v "$c" >/dev/null 2>&1 && "$c" -c "print(1)" >/dev/null 2>&1; then
    PY="$c"; break
  fi
done
[ -n "$PY" ] || { echo "[!] 无可用 python" >&2; exit 1; }
# mktemp 在 Git Bash 给 /tmp/xxx(MSYS 虚拟路径), 原生 python 读不到 → cygpath 转 Windows 路径
TMP_WIN=$TMP
command -v cygpath >/dev/null 2>&1 && TMP_WIN=$(cygpath -m "$TMP")

# 域名与临时路径走环境变量传给 python, 不再拼进程序源码(消除注入面)
export _CRTSH_DOMAIN="$DOMAIN" _CRTSH_TMP="$TMP_WIN"

# curl 统一参数:
#   -f   HTTP 错误(4xx/5xx)按失败处理 —— 否则错误页会被原样写进 $TMP, 靠后续 json 解析兜(原因还被 2>/dev/null 吞掉)
#   -L   跟随 302(crt.sh / certspotter 会跳)
#   --retry 2 --retry-connrefused  国内网络首连超时概率高, 没有重试就只剩「换源」一条路
#   -m 60  原 30s 对大域偏短(crt.sh 大域查询常 60s+)
CURL_OPTS=(-fsSL --retry 2 --retry-connrefused -m 60)

fetch_crtsh() {
  # crt.sh JSON(最全)
  curl "${CURL_OPTS[@]}" "https://crt.sh/?q=%25.${DOMAIN}&output=json" -o "$TMP" 2>/dev/null \
    && [ -s "$TMP" ] \
    && "$PY" -c "
import json, os, sys
dom = os.environ['_CRTSH_DOMAIN']
try:
    data = json.load(open(os.environ['_CRTSH_TMP']))
except Exception:
    sys.exit(1)
names = set()
suffix = '.' + dom
for item in data:
    for n in str(item.get('name_value', '')).split('\n'):
        n = n.strip().lstrip('*.').lower()
        if n and (n.endswith(suffix) or n == dom):
            names.add(n)
for n in sorted(names):
    print(n)
" 2>/dev/null
}

fetch_certspotter() {
  curl "${CURL_OPTS[@]}" "https://api.certspotter.com/v1/issuances?domain=${DOMAIN}&include_subdomains=true&expand=dns_names" -o "$TMP" 2>/dev/null \
    && [ -s "$TMP" ] \
    && "$PY" -c "
import json, os, sys
dom = os.environ['_CRTSH_DOMAIN']
try:
    data = json.load(open(os.environ['_CRTSH_TMP']))
except Exception:
    sys.exit(1)
if isinstance(data, dict):
    sys.exit(1)  # 错误对象/限流
names = set()
suffix = '.' + dom
for item in data:
    for n in item.get('dns_names', []):
        n = n.strip().lstrip('*.').lower()
        if n.endswith(suffix) or n == dom:
            names.add(n)
for n in sorted(names):
    print(n)
" 2>/dev/null
}

fetch_hackertarget() {
  # HackerTarget hostsearch(免费无 key): 直接给 子域名,IP 映射(输出两列)。
  # 字符类含下划线(_dmarc 类子域不丢)。DOMAIN 已过白名单, 这里只有 `.` 需要转义。
  curl "${CURL_OPTS[@]}" "https://api.hackertarget.com/hostsearch/?q=${DOMAIN}" 2>/dev/null \
    | grep -E "^[a-zA-Z0-9._-]+\.${DOMAIN//./\\.},[0-9]"
}

RESULT=""
for fn in fetch_crtsh fetch_certspotter fetch_hackertarget; do
  R=$($fn)
  if [ -n "$R" ]; then
    echo "[i] 命中源: $fn" >&2
    RESULT="$R"
    break
  else
    echo "[i] $fn 失败/为空, 换路" >&2
  fi
done

if [ -z "$RESULT" ]; then
  echo "[!] 三源全失败(crt.sh 被墙?): 跳过, 用测绘 cert 反查补(FOFA cert.subject.cn= / Quake cert:)" >&2
  exit 1
fi

if [ -n "$OUT" ]; then
  echo "$RESULT" | sort -u > "$OUT"
  echo "[+] $(sort -u <<<"$RESULT" | wc -l) 个子域 → $OUT" >&2
else
  sort -u <<<"$RESULT"
fi
# 临时文件由上面的 trap 清理
