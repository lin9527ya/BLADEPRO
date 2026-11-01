#!/usr/bin/env python3
"""
rescan.py — 增量回扫（对齐 dig-scope §1.3-C「回扫只捞新增」）

重跑免费源(crtsh + HackerTarget, 不烧 FOFA 配额)与 baseline 对比, 只输出新增子域。
「0 新增」不算进度(dig-scope 口径) — 连续 2 个 0 新增应改随机新业务名。

用法:
  python tools/rescan.py --baseline 资产/jd/raw_subs.txt --domain jd.com
  python tools/rescan.py --baseline ... --domain jd.com --out new_only.txt
  python tools/rescan.py --baseline ... --domain jd.com --fofa-outside  # FOFA 由主会话用 MCP 跑,脚本提示

输出:
  stdout: "N 新增 / M 消失" 一行结论 + 新增清单
  --out: new_only.txt(新增子域, 直接喂 dedupe.py 验证)
  同目录 dropped.txt(消失子域, 供参考)
"""
import argparse
import os
import sys

# Windows GBK 控制台防崩(⚠️/🔥 等 Emoji 不可编码)。
# 此前 rescan.py 缺这段, 而 --sources 的帮助文本里有一个 ⚠️ ->
# Git Bash(ENV.md 钦定的 shell, stdout=gbk)下 `rescan.py --help` 直接 UnicodeEncodeError 退出 1。
# 与 dedupe/bucket_enum/itunes_enum 三处保持一致。2026-09-27 补。
for _s in (sys.stdout, sys.stderr):
    try:
        _s.reconfigure(errors="replace")
    except Exception:
        pass

SKILL = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# 复用 collect.py 的源函数与默认源清单(保持与 baseline 采集口径一致, 防假「消失」)
sys.path.insert(0, os.path.join(SKILL, "tools"))
from collect import src_crtsh, src_ht, norm, DEFAULT_SOURCES  # noqa: E402


def main():
    ap = argparse.ArgumentParser(description="增量回扫: 只捞相对 baseline 的新增")
    ap.add_argument("--baseline", required=True, help="基线文件(collect.py 产出的 raw_subs.txt)")
    ap.add_argument("--domain", required=True)
    ap.add_argument("--sources", default=",".join(DEFAULT_SOURCES),
                    help=f"回扫源(默认与 collect.py 一致: {','.join(DEFAULT_SOURCES)}; FOFA 用 MCP 另跑)。"
                         "⚠️ 源集合应与 baseline 的 collect.py 源一致,否则旧源条目全被误报为「消失」")
    ap.add_argument("--out", default=None, help="新增清单输出文件")
    args = ap.parse_args()

    if not os.path.exists(args.baseline):
        print(f"[!] baseline 不存在: {args.baseline} — 先跑 collect.py 建 baseline", file=sys.stderr)
        sys.exit(1)

    # baseline 也过 norm(): 有人拿 clean.txt 当 baseline 传进来时, 去噪剔除的子域会全被误报「消失」
    base = set(norm([l for l in open(args.baseline, encoding="utf-8") if l.strip()]))
    print(f"[*] baseline: {len(base)} 个子域({args.baseline})", file=sys.stderr)

    merged = []
    zero_sources = []  # 返回 0 条的源(大概率拉取失败而非真没有) — dropped 判定不可靠
    # .strip(): `--sources "crtsh, ht"` 里的 " ht" 不去空格会被当成未知源静默跳过
    # (collect.py 的同名解析一直有 strip, 这里漏了)。2026-09-27 修。
    src_list = [s.strip() for s in args.sources.split(",") if s.strip()]
    for src in src_list:
        if src == "crtsh":
            got = src_crtsh(args.domain)
        elif src == "ht":
            got = src_ht(args.domain)
        elif src == "subfinder":
            from collect import src_subfinder
            got = src_subfinder(args.domain)
        elif src == "amass":
            from collect import src_amass
            got = src_amass(args.domain)
        else:
            print(f"[!] 未知源: {src}(可用: crtsh/ht/subfinder/amass)", file=sys.stderr)
            continue
        merged += got
        print(f"[*] {src}: {len(got)} 条", file=sys.stderr)
        if not got:
            zero_sources.append(src)

    current = set(norm(merged))
    new = sorted(current - base)
    dropped = sorted(base - current)

    print(f"\n[结论] {len(new)} 新增 / {len(dropped)} 消失", file=sys.stderr)
    for h in new:
        print(f"  + {h}", file=sys.stderr)
    if not new:
        print("  (0 新增 — 不算进度; 本回合连续 2 个 0 新增 → 改随机新业务名, dig-scope §1.3-C)", file=sys.stderr)

    out = args.out or os.path.join(os.path.dirname(args.baseline) or ".", "new_only.txt")
    if new:
        with open(out, "w", encoding="utf-8") as f:
            f.write("\n".join(new))
        print(f"[+] 新增 {len(new)} → {out}(验证: python tools/dedupe.py --in {out} --out {out}.clean.txt)", file=sys.stderr)
    if dropped:
        if zero_sources:
            # 有源拉了 0 条(crt.sh 被墙等) — baseline 独有条目大概率是拉取失败不是真下线, 不写 dropped 误导
            print(f"[!] 源 {','.join(zero_sources)} 返回 0 条(疑似拉取失败非真无数据) — "
                  f"跳过 dropped.txt({len(dropped)} 个疑似消失不可靠, 修好源再回扫)", file=sys.stderr)
        else:
            dp = os.path.join(os.path.dirname(args.baseline) or ".", "dropped.txt")
            # baseline 可能是用**更多**源跑出来的(如 collect.py --sources crtsh,subfinder,ht,o4a)。
            # 本次回扫只跑了一部分源时, 那些没跑的源独有的条目会全部落进 dropped —— 是假「消失」。
            # 文件头写明本次跑了哪些源, 让读的人自己判断, 而不是把误导藏起来。
            all_known = {"crtsh", "ht", "subfinder", "amass", "o4a"}
            missing = sorted(all_known - set(src_list))
            with open(dp, "w", encoding="utf-8") as f:
                f.write(f"# 本次回扫源: {','.join(src_list)}\n")
                if missing:
                    f.write(f"# ⚠️ 未跑的源: {','.join(missing)}\n")
                    f.write("#    baseline 里若含这些源独有的条目, 它们会**全部出现在下面**,\n")
                    f.write("#    那是「本次没查」不是「真消失」。要确认就带上这些源重跑。\n")
                f.write("\n".join(dropped))
            print(f"[*] 消失 {len(dropped)} → {dp}(参考: 可能下线/换 CDN)", file=sys.stderr)
            if missing:
                print(f"[!] 注意: 本次没跑 {','.join(missing)} —— baseline 里这些源独有的条目"
                      f"会被误报为「消失」(已在 dropped.txt 头部标注)", file=sys.stderr)

    # 提示 FOFA 增量由主会话 MCP 跑(脚本不碰 key)
    print("[i] FOFA/Quake 增量: 主会话用 mcp__fofa__get_alerts 对当前种子重查对比 covered(烧配额, 有缺口才跑)", file=sys.stderr)


if __name__ == "__main__":
    main()
