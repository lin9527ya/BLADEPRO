#!/usr/bin/env python3
"""
collect.py — 一键多源子域收集管道（收集→合并→去噪）

串接本技能已有组件（crtsh.sh / subfinder / HackerTarget / amass / gau / dns_brute.py / dedupe.py），
单命令跑完标准链路，输出可直接用的 clean.txt。

用法:
  python tools/collect.py --domain jd.com                              # 全免费源+去噪
  python tools/collect.py --domain jd.com --out-dir 资产/jd/          # 指定输出目录
  python tools/collect.py --domain jd.com --sources crtsh,ht          # 只跑指定源
  python tools/collect.py --domain jd.com --sources crtsh,subfinder,ht,o4a   # 加 OneForAll 20 源(约 +20~60s)
  python tools/collect.py --domain jd.com --brute                      # 追加字典爆破(fake-ip 环境可能全 0)
  python tools/collect.py --domain jd.com --urls                       # 追加 gau 历史 URL
  python tools/collect.py --domain jd.com --skip-dedupe                # 只收集不去噪

输出文件(out-dir 下):
  raw_subs.txt   合并去重后的原始子域
  clean.txt      dedupe.py 去噪后的可用资产
  report.json    去噪明细(剔除原因/CDN 标注/存疑)
  summary.md     一行一源的统计
节奏口径: 节奏认 dig-scope 一种子闭环; 本脚本只管收集手段, 条数不是进度。
"""
import argparse
import os
import re
import shutil
import subprocess
import sys
import time
import urllib.request

SKILL = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TOOLS = os.path.join(SKILL, "tools")
GOBIN = os.path.expanduser("~/go/bin")
USERBIN = os.path.expanduser("~/bin")

# amass 默认不跑(v5 被动源多为 key-gated, 实测 5-8 分钟常 0 产出白拖慢管道);
# 需要 --sources crtsh,subfinder,ht,amass 显式加回
DEFAULT_SOURCES = ["crtsh", "subfinder", "ht"]

_MISSING_WARNED: set = set()


def resolve_bin(name: str) -> str:
    """统一二进制解析: PATH → ~/go/bin/ → ~/bin/(Windows 补 .exe)。
    找不到返回空串并 stderr 警告(只警告一次, 防刷屏)——否则源静默 0 条会被误读成目标没有资产。"""
    cands = []
    w = shutil.which(name)
    if w:
        cands.append(w)
    for d in (GOBIN, USERBIN):
        for exe in (os.path.join(d, name), os.path.join(d, name + ".exe")):
            if os.path.exists(exe):
                cands.append(exe)
    if cands:
        return cands[0]
    if name not in _MISSING_WARNED:
        _MISSING_WARNED.add(name)
        print(f"[!] 找不到工具 {name}(装法见 ENV.md), 该源将输出 0 条", file=sys.stderr)
    return ""


def norm(domains: list) -> list:
    """域名归一化: 去 *. 前缀/去 path/去端口/小写/去空"""
    out = set()
    for d in domains:
        d = d.strip().lower().split("/")[0].split(":")[0].lstrip("*.").rstrip(".")
        # 过滤明显不是域名的行(公司名混入/含空格)
        if d and "." in d and " " not in d and re.match(r"^[a-z0-9.-]+$", d):
            out.add(d)
    return sorted(out)


def run(cmd, timeout, stdin=None):
    try:
        # encoding 固定 utf-8: Windows 默认 GBK 会把子域列表/外部工具输出解崩(subfinder/crtsh 全是 utf-8)
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout,
                           input=stdin, encoding="utf-8", errors="replace",
                           env={**os.environ, "PATH": f"{GOBIN}{os.pathsep}{os.environ.get('PATH','')}"})
        return p.stdout
    except (subprocess.TimeoutExpired, FileNotFoundError, OSError):
        return ""


def src_crtsh(domain):
    p = run(["bash", os.path.join(TOOLS, "crtsh.sh"), domain], 90)
    return [l for l in p.splitlines() if l.strip()]


def src_subfinder(domain):
    binp = resolve_bin("subfinder")
    if not binp:
        return []
    p = run([binp, "-d", domain, "-silent", "-timeout", "15"], 120)
    return [l for l in p.splitlines() if l.strip()]


def src_ht(domain):
    """HackerTarget hostsearch — 免费, 直接给 子域,IP 映射"""
    try:
        req = urllib.request.Request(
            f"https://api.hackertarget.com/hostsearch/?q={domain}",
            headers={"User-Agent": "Mozilla/5.0"})
        with urllib.request.urlopen(req, timeout=30) as r:
            text = r.read().decode(errors="ignore")
    except Exception:
        return []
    if "error" in text.lower() or "API count exceeded" in text:
        return []
    return [l.split(",")[0] for l in text.splitlines() if "," in l]


def src_amass(domain):
    binp = resolve_bin("amass")
    if not binp:
        return []
    # amass -timeout 单位是分钟(3), 子进程 timeout 留足余量(200s > 180s)
    p = run([binp, "enum", "-passive", "-d", domain, "-timeout", "3"], 200)
    return [l for l in p.splitlines() if l.strip() and "." in l]


def src_o4a(domain, out_dir):
    """OneForAll 被动多源(20 源 + DNS 解析, 约 20-60 秒)。
    爆破/置换档不在管道里跑(太慢), 需要时单独: python tools/o4a_enum.py --mode brute|perm"""
    o4a_dir = os.path.join(out_dir, "o4a")
    run([sys.executable, os.path.join(TOOLS, "o4a_enum.py"),
         "--domain", domain, "--mode", "passive", "--out-dir", o4a_dir], 600)
    try:
        with open(os.path.join(o4a_dir, "o4a.txt"), encoding="utf-8") as f:
            return [line.strip() for line in f if line.strip()]
    except OSError:
        return []


def src_gau(domain):
    """gau 历史 URL 聚合; gau 缺失时降级 waybackurls(输出同为 URL 列表)。"""
    binp = resolve_bin("gau") or resolve_bin("waybackurls")
    if not binp:
        return [], []
    if "waybackurls" in os.path.basename(binp):
        p = run([binp, domain], 90)
    else:
        p = run([binp, "--providers", "wayback,otx", domain], 90)
    urls = [l for l in p.splitlines() if l.strip()]
    # 从 URL 抽 host
    hosts = {u.split("//")[-1].split("/")[0].split(":")[0] for u in urls}
    return sorted(hosts), urls


def main():
    ap = argparse.ArgumentParser(description="一键多源子域收集→去噪管道")
    ap.add_argument("--domain", required=True)
    ap.add_argument("--out-dir", default=None, help="输出目录(默认 ./collect_<domain>)")
    ap.add_argument("--sources", default=",".join(DEFAULT_SOURCES),
                    help="逗号分隔源: crtsh,subfinder,ht,amass,o4a(默认 " + ",".join(DEFAULT_SOURCES) + ")")
    ap.add_argument("--brute", action="store_true", help="追加 tools/dns_brute.py 字典爆破")
    ap.add_argument("--urls", action="store_true", help="追加 gau 历史 URL(out-dir/urls.txt)")
    ap.add_argument("--skip-dedupe", action="store_true", help="只收集不去噪")
    args = ap.parse_args()

    domain = args.domain.lower().lstrip("*.")
    out_dir = args.out_dir or f"collect_{domain.replace('.', '_')}"
    os.makedirs(out_dir, exist_ok=True)
    sources = [s.strip() for s in args.sources.split(",") if s.strip()]

    print(f"[*] 目标: {domain} | 源: {sources} | 输出: {out_dir}", file=sys.stderr)
    stats = {}
    merged = []

    for src in sources:
        print(f"[*] 跑源: {src} ...", file=sys.stderr)
        n = 0
        if src == "crtsh":
            got = src_crtsh(domain)
            merged += got; n = len(got)
        elif src == "subfinder":
            got = src_subfinder(domain)
            merged += got; n = len(got)
            if n == 0:
                print("[i] subfinder 0 产出(本机 40 源未配 key, 属预期; 免费源配置见 ENV.md)", file=sys.stderr)
        elif src == "ht":
            got = src_ht(domain)
            merged += got; n = len(got)
        elif src == "amass":
            got = src_amass(domain)
            merged += got; n = len(got)
        elif src == "o4a":
            got = src_o4a(domain, out_dir)
            merged += got; n = len(got)
        else:
            print(f"[!] 未知源: {src}(可用: crtsh,subfinder,ht,amass,o4a)", file=sys.stderr)
            continue
        stats[src] = n
        print(f"[*] {src}: {n} 条", file=sys.stderr)

    if args.urls:
        print("[*] 跑源: gau(历史 URL) ...", file=sys.stderr)
        hosts, urls = src_gau(domain)
        merged += hosts
        stats["gau"] = len(hosts)
        with open(os.path.join(out_dir, "urls.txt"), "w", encoding="utf-8") as f:
            f.write("\n".join(urls))
        print(f"[*] gau: {len(hosts)} host / {len(urls)} URL → urls.txt", file=sys.stderr)

    if args.brute:
        print("[*] 跑字典爆破(dns_brute.py; fake-ip 环境可能全 0, 见 ENV.md) ...", file=sys.stderr)
        p = run([sys.executable, os.path.join(TOOLS, "dns_brute.py"), "--domain", domain,
                 "--out", os.path.join(out_dir, "brute.csv")], 300)
        try:
            with open(os.path.join(out_dir, "brute.csv"), encoding="utf-8") as f:
                # 排除 `#` 注释行 与 含 `/` 的行 —— 后者是 dns_brute 的 "/24 聚类" 行
                # (如 104.16.0.0/24,17)。老版本的 dns_brute 没给它打 `#`, 这里再兜一道:
                # 否则它会被 norm() 的 split("/")[0] 变成假主机名 104.16.0.0 注入 raw_subs.txt。
                got = [l.split(",")[0] for l in f.read().splitlines()
                       if l and not l.startswith("#") and "," in l and "/" not in l]
        except OSError:
            got = []
        merged += got
        stats["brute"] = len(got)
        print(f"[*] brute: {len(got)} 条", file=sys.stderr)

    subs = norm(merged)
    raw_path = os.path.join(out_dir, "raw_subs.txt")
    # raw_subs.txt 同时是 rescan.py 的 --baseline —— 静默覆盖它 = 增量回扫的基线被换掉,
    # 之后「新增/消失」全部失真, 而且毫无提示。旧文件先另存为带时间戳的 .bak 再写。
    # 2026-09-27 修。
    if os.path.exists(raw_path):
        stamp = time.strftime("%Y%m%d-%H%M%S")
        bak = os.path.join(out_dir, f"raw_subs.{stamp}.bak")
        os.replace(raw_path, bak)
        print(f"[!] {raw_path} 已存在(可能被 rescan.py 当 baseline 用过) —— "
              f"旧文件另存为 {os.path.basename(bak)}, 未覆盖。", file=sys.stderr)
    with open(raw_path, "w", encoding="utf-8") as f:
        f.write("\n".join(subs))
    print(f"\n[+] 合并去重: {len(subs)} 个子域 → {raw_path}", file=sys.stderr)

    # summary.md
    with open(os.path.join(out_dir, "summary.md"), "w", encoding="utf-8") as f:
        f.write(f"# collect.py 收集统计 — {domain} ({len(subs)} 合并后)\n\n")
        f.write("| 源 | 原始条数 |\n|---|---|\n")
        for s, n in stats.items():
            f.write(f"| {s} | {n} |\n")

    if args.skip_dedupe:
        print("[i] --skip-dedupe: 到 raw_subs.txt 为止。后续: python tools/dedupe.py "
              f"--in {raw_path} --out {out_dir}/clean.txt --json {out_dir}/report.json", file=sys.stderr)
        return

    # 自动接去噪
    clean_path = os.path.join(out_dir, "clean.txt")
    json_path = os.path.join(out_dir, "report.json")
    print("[*] 接去噪管道(dedupe.py, 含 httpx 探活, 大列表耗时数分钟) ...", file=sys.stderr)
    # Windows 无 python3 命令 → 用当前解释器; 固定 utf-8 + PYTHONUTF8 防 GBK 崩
    # 失败必须**可见**: 此前这一步没有 try/except、没有 timeout、不检查返回码,
    # dedupe 缺失或非 0 退出时 collect 仍以 exit 0 静默结束 —— 用户看到「完成」的错觉,
    # 实际 clean.txt 根本没生成。2026-09-27 修。
    dedupe_py = os.path.join(TOOLS, "dedupe.py")
    try:
        p = subprocess.run([sys.executable, dedupe_py,
                            "--in", raw_path, "--out", clean_path, "--json", json_path],
                           encoding="utf-8", errors="replace", timeout=1800,
                           env={**os.environ, "PYTHONUTF8": "1"})
    except subprocess.TimeoutExpired:
        print(f"\n[!] 去噪超时(>30 分钟), 已中止。原始产出仍在: {raw_path}", file=sys.stderr)
        print(f"    可单独重跑: python {dedupe_py} --in {raw_path} --out {clean_path} --json {json_path}",
              file=sys.stderr)
        return 1
    except OSError as e:
        print(f"\n[!] 去噪未能启动: {e}", file=sys.stderr)
        print(f"    原始产出仍在: {raw_path}; 去噪脚本应在 {dedupe_py}", file=sys.stderr)
        return 1

    if p.returncode != 0:
        print(f"\n[!] 去噪失败(退出码 {p.returncode}) —— clean.txt 未生成, 别当成已完成。",
              file=sys.stderr)
        print(f"    原始产出仍在: {raw_path}", file=sys.stderr)
        return 1
    if not os.path.exists(clean_path):
        print(f"\n[!] 去噪退出码为 0 但没写出 {clean_path} —— 产物缺失, 别当成已完成。",
              file=sys.stderr)
        return 1

    n = sum(1 for _ in open(clean_path, encoding="utf-8"))
    print(f"\n[+] 完成: {n} 个可用资产 → {clean_path}", file=sys.stderr)
    print(f"    去噪明细 → {json_path} | 源统计 → {out_dir}/summary.md", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
