#!/usr/bin/env python3
"""
o4a_enum.py — OneForAll 包装器(子域枚举第三条线: 被动多源 / 字典爆破 / 置换枚举)

把 D:\\OneForAll-0.4.5(2026-09 实用化改造版)接进本技能的资产管道:
  collect.py(crtsh+subfinder+ht, 快)  +  dns_brute.py(小字典主动爆破)  +  **o4a_enum.py(20 源聚合 + massdns 大字典 + 置换)**

用法:
  python tools/o4a_enum.py --domain example.com                    # 被动档: 20 源 + DNS 解析(默认)
  python tools/o4a_enum.py --domain example.com --mode brute       # 追加 massdns 字典爆破(默认小字典 9.5 万行)
  python tools/o4a_enum.py --domain example.com --mode brute --dict medium   # 88 万行大字典(慢, 约 5 分钟)
  python tools/o4a_enum.py --domain example.com --mode perm --seed subs.txt  # 置换枚举: 拿已有子域当种子重组
  python tools/o4a_enum.py --domain example.com --mode full        # 全开(含 HTTP 探活/指纹/finder/altdns/enrich)
  python tools/o4a_enum.py --domain example.com --out-dir 资产/x   # 指定输出目录

输出(out-dir 下):
  o4a.txt         纯主机名列表(一行一个, 可直接喂 dedupe.py, 或作为 collect.py 的 --sources o4a 输入)
  o4a_sources.md  各源产量统计(哪个源还有肉 —— 下轮剪枝依据)
  o4a_raw.csv     OneForAll 原始结果(22 列: subdomain/ip/cname/resolve/source/...)

前置: O4A_HOME 环境变量指定 OneForAll 目录(默认 D:\\OneForAll-0.4.5), 目录内需有 .venv。
节奏口径: 本脚本只管收集手段; 什么时候跑、跑几轮认 dig-scope 一种子闭环。
"""
import argparse
import collections
import csv
import os
import shutil
import subprocess
import sys

DEFAULT_HOME = r"D:\OneForAll-0.4.5"

# 档位 → (oneforall 开关, 环境变量开关)
MODES = {
    # 被动收集 + DNS 解析, 不做 HTTP 探活(探活交给 httpx + dedupe.py)
    "passive": (["--brute", "False", "--req", "False", "--takeover", "False"], {}),
    # 追加 massdns 字典爆破
    "brute": (["--brute", "True", "--req", "False", "--takeover", "False"], {}),
    # 置换枚举: 由 perm.py 驱动(种子来自 --seed)
    "perm": (None, {}),
    # 全开: HTTP 探活 + 指纹 + finder + altdns + enrich(慢, 需要 title/banner 时才用)
    "full": (["--brute", "True", "--req", "True", "--takeover", "False"],
             {"O4A_REQ": "1", "O4A_FINDER": "1", "O4A_ALTDNS": "1", "O4A_ENRICH": "1"}),
}

MODE_TIMEOUT = {"passive": 600, "brute": 1800, "perm": 900, "full": 1800}


def o4a_python(home):
    """OneForAll 自带 venv 的解释器"""
    for rel in (os.path.join(".venv", "Scripts", "python.exe"),
                os.path.join(".venv", "bin", "python")):
        path = os.path.join(home, rel)
        if os.path.exists(path):
            return path
    return None


def run_oneforall(home, py, domain, out_dir, args, env_extra, timeout):
    """调 oneforall.py; 返回 (ok, 输出尾部)"""
    cmd = [py, "oneforall.py", "--target", domain, "--path", out_dir,
           "--alive", "False", "--fmt", "csv"] + args + ["run"]
    env = {**os.environ, **env_extra, "PYTHONIOENCODING": "utf-8"}
    print(f"[*] 执行: {' '.join(cmd[1:])}", file=sys.stderr)
    try:
        p = subprocess.run(cmd, cwd=home, timeout=timeout, env=env,
                           stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                           encoding="utf-8", errors="replace")
    except subprocess.TimeoutExpired:
        return False, f"超时({timeout}s)"
    except OSError as e:
        return False, f"启动失败: {e}"
    tail = "\n".join((p.stdout or "").strip().splitlines()[-12:])
    return p.returncode == 0, tail


def run_perm(home, py, domain, seed, out_dir, timeout):
    """置换枚举(perm.py): 外部子域列表当种子"""
    cmd = [py, "perm.py", "--domain", domain, "--seed", seed, "--path", out_dir]
    env = {**os.environ, "PYTHONIOENCODING": "utf-8"}
    print(f"[*] 执行: {' '.join(cmd[1:])}", file=sys.stderr)
    try:
        p = subprocess.run(cmd, cwd=home, timeout=timeout, env=env,
                           stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                           encoding="utf-8", errors="replace")
    except subprocess.TimeoutExpired:
        return False, f"超时({timeout}s)"
    except OSError as e:
        return False, f"启动失败: {e}"
    tail = "\n".join((p.stdout or "").strip().splitlines()[-6:])
    return p.returncode == 0, tail


def find_csv(out_dir, domain):
    """OneForAll 以主域命名结果文件(输入 www.x.com 会落 x.com.csv)"""
    cands = [os.path.join(out_dir, f"{domain}.csv")]
    for name in os.listdir(out_dir):
        if name.endswith(".csv"):
            cands.append(os.path.join(out_dir, name))
    for path in cands:
        if os.path.exists(path):
            return path
    return None


def summarize(csv_path, out_dir):
    """CSV → o4a.txt(纯主机名) + o4a_sources.md(各源产量)"""
    rows = []
    with open(csv_path, encoding="utf-8-sig", errors="replace") as fd:
        for row in csv.DictReader(fd):
            sub = (row.get("subdomain") or "").strip().lower()
            if sub:
                rows.append(row)
    hosts = sorted({r["subdomain"].strip().lower() for r in rows})
    with open(os.path.join(out_dir, "o4a.txt"), "w", encoding="utf-8", newline="\n") as fd:
        fd.write("\n".join(hosts) + "\n")

    by_source = collections.defaultdict(lambda: [0, 0])  # source -> [总数, 解析成功]
    for r in rows:
        src = r.get("source") or "-"
        by_source[src][0] += 1
        if str(r.get("resolve")).strip() == "1":
            by_source[src][1] += 1
    with open(os.path.join(out_dir, "o4a_sources.md"), "w", encoding="utf-8", newline="\n") as fd:
        fd.write("| 源 | 发现条数 | 其中解析成功 |\n|---|---|---|\n")
        for src, (total, resolved) in sorted(by_source.items(), key=lambda kv: -kv[1][0]):
            fd.write(f"| {src} | {total} | {resolved} |\n")
    resolved_total = len({r["subdomain"].strip().lower() for r in rows
                          if str(r.get("resolve")).strip() == "1"})
    return hosts, resolved_total, dict(by_source)


def main():
    ap = argparse.ArgumentParser(description="OneForAll 包装器(被动多源/字典爆破/置换枚举)")
    ap.add_argument("--domain", required=True, help="目标主域")
    ap.add_argument("--mode", default="passive", choices=list(MODES),
                    help="passive(默认)/brute/perm/full")
    ap.add_argument("--dict", dest="dict_size", default="small", choices=["small", "medium"],
                    help="brute 档的字典: small=9.5 万行(默认) / medium=88 万行")
    ap.add_argument("--seed", default=None, help="perm 档的种子子域文件(每行一个主机名)")
    ap.add_argument("--out-dir", default=None, help="输出目录(默认 ./o4a_<domain>)")
    ap.add_argument("--timeout", type=int, default=None, help="子进程超时秒数(默认按档位)")
    ap.add_argument("--home", default=os.environ.get("O4A_HOME", DEFAULT_HOME),
                    help="OneForAll 目录(默认 $O4A_HOME 或 " + DEFAULT_HOME + ")")
    args = ap.parse_args()

    home = args.home
    if not os.path.isdir(home):
        print(f"[!] OneForAll 目录不存在: {home}", file=sys.stderr)
        return 2
    py = o4a_python(home)
    if not py:
        print(f"[!] 未找到 {home}\\.venv 解释器(先建 venv 并装 requirements-py313.txt)", file=sys.stderr)
        return 2

    domain = args.domain.strip().lower()
    out_dir = os.path.abspath(args.out_dir or f"o4a_{domain}")
    os.makedirs(out_dir, exist_ok=True)
    timeout = args.timeout or MODE_TIMEOUT[args.mode]

    if args.mode == "perm":
        if not args.seed or not os.path.exists(args.seed):
            print("[!] perm 档必须给 --seed <子域文件>(每行一个主机名; 可直接用资产表/collect.py 的 raw_subs.txt)", file=sys.stderr)
            return 2
        ok, tail = run_perm(home, py, domain, os.path.abspath(args.seed), out_dir, timeout)
    else:
        fargs, fenv = MODES[args.mode]
        if args.mode == "brute":
            fenv = {**fenv, "O4A_DICT": args.dict_size}
        ok, tail = run_oneforall(home, py, domain, out_dir, fargs, fenv, timeout)

    if not ok:
        print(f"[!] OneForAll 未正常结束:\n{tail}", file=sys.stderr)
        return 1

    csv_path = find_csv(out_dir, domain)
    if not csv_path:
        print(f"[!] 没找到结果 CSV(目录: {out_dir})——可能是目标无任何发现", file=sys.stderr)
        return 1
    hosts, resolved, by_source = summarize(csv_path, out_dir)
    raw_copy = os.path.join(out_dir, "o4a_raw.csv")
    if os.path.abspath(csv_path) != os.path.abspath(raw_copy):
        shutil.copyfile(csv_path, raw_copy)

    print(f"[+] {domain}: {len(hosts)} 个唯一主机名(其中解析成功 {resolved})"
          f"\n    o4a.txt / o4a_sources.md / o4a_raw.csv → {out_dir}", file=sys.stderr)
    for src, (total, res) in sorted(by_source.items(), key=lambda kv: -kv[1][0]):
        print(f"      {src}: {total} 条(解析成功 {res})", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
