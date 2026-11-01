#!/usr/bin/env python3
"""
dedupe.py — 资产自动去噪清洗管道(收集产出的最后一道闸)

输入: 域名/子域列表(每行一个, 支持 host:port)
输出: 保留的可用资产 + 各类噪音剔除报告

去噪规则(全部 2026-09-07 用 jd.com 297 子域实测验证):
  1. CDN 域名/纯 CDN 接入点: CNAME 指向已知 CDN 厂商特征(cloudflare/qianxun/alibabadns/shifen 等)
     + 多个域名共享同一 CDN CNAME 后缀 = CDN 源站接入点(只标 CDN 不删——走 CDN 不代表没业务)
  2. CDN/云 IP 段: A 记录落在已知 CDN 网段(104.16/12 Cloudflare 等)或 Team Cymru ASN 判为 CDN 厂商
  3. 空壳跳转站: HTTP 302 跳主站/错误页(京东 error2.aspx from=shopdomain 类)
  4. 无业务壳: 200 但标题为 "Welcome to nginx/OpenResty" 默认页
  5. 死域名: DoH 解析无 A 记录
  6. 第三方 SaaS: CNAME 指向 *.service-now.com / *.herokuapp.com 等(标注非自建, 不删)

核心原则(对齐 dig-scope §3.1): CDN 标记不删除——401/403/登录墙=存活, 有业务=资产;
只剔除「无业务的纯壳」。每个 IP 的 CDN 判定只查一次(本地网段表优先, 未命中走 Team Cymru DNS, 带内存缓存)。

用法:
  python dedupe.py --in subs.txt --out clean.txt
  python dedupe.py --in subs.txt --out clean.txt --json report.json   # 附机器可读报告
  # 默认行为: CDN 保留但在报告里标注(没有 --keep-cdn 这个开关, 2026-09-27 删去误写)
"""
import argparse
import concurrent.futures as cf
import ipaddress
import json
import os
import re
import ssl
import subprocess
import sys
import urllib.request
from collections import Counter, defaultdict

DOH = "https://223.5.5.5/resolve?name={}&type={}"  # 阿里 DoH: 解析目标域名(国内快、稳)
# 全球公共 DoH —— 专给 Team Cymru 的 .cymru.com TXT 用:
# 实测阿里 223.5.5.5 对 .cymru.com 一律返回 SERVFAIL(status=2), 换公共解析才有答案。
# DoH 是加密的: 相比旧的 ip-api 明文 HTTP POST(把整份目标 IP 列表写到链路上), 不再明文外泄。
DOH_PUBLIC = "https://1.1.1.1/dns-query?name={}&type={}"

# Windows Python 默认不加载系统 CA(ssl.get_default_verify_paths 全空) → HTTPS 证书验证必挂。
# 优先 certifi(已装), 否则降级不验证(仅信息收集场景, 可接受)。
try:
    import certifi
    _SSL_CTX = ssl.create_default_context(cafile=certifi.where())
except ImportError:
    _SSL_CTX = ssl.create_default_context()
    _SSL_CTX.check_hostname = False
    _SSL_CTX.verify_mode = ssl.CERT_NONE

# GBK 管道下 ⚠️ 等 emoji 会 UnicodeEncodeError 崩, replace 保命
for _s in (sys.stdout, sys.stderr):
    try:
        _s.reconfigure(errors="replace")
    except Exception:
        pass

# ---- 已知 CDN CNAME 特征(2026-09-07 实测核对) ----
CDN_CNAME_PATTERNS = [
    r"cloudflare\.net\.?$", r"\.cdn\.cloudflare", r"cf\.tcoproxy",
    r"gslb\.qianxun\.com", r"cdnhwcvix", r"\.wscdns\.com", r"wscloudcdn",
    r"alibabadns\.com", r"tbcache", r"\.alicdn\.com$",
    r"a\.shifen\.com", r"\.bcelb\.com", r"\.lxdns\.com", r"jiashule\.com", r"accelerate",
    r"cdnhwc\d+", r"hwclouds?cdn", r"\.cdn\d+\.myqcloud", r"tencdns", r"dnsv1",
    r"edgeone", r"eo\.dnse\d", r"dnsv\d+\.com", r"cdngslb", r"\.ourwebpic\.com",
    r"akamai(edge|net)", r"cloudfront\.net", r"fastly\.net", r"cdn77",
    r"jsdelivr", r"bootcdn", r"unpkg", r"pantheon", r"wpengine", r"netlify",
    r"herokuapp", r"vercel", r"azureedge", r"trafficmanager", r"azurewebsites",
]
# 第三方 SaaS(CNAME 指向 = 目标在用 SaaS, 标注不删)
SAAS_CNAME_PATTERNS = [
    r"service-now\.com", r"salesforce", r"\.scdn\.wixpress", r"\.shops\.thinkwithportals",
    r"readme\.io", r"intercom", r"zendesk", r"freshdesk", r"helpscout",
]
# Cloudflare 等已知 CDN IPv4 网段(先本地判, 不烧 API)
CDN_IP_RANGES = [
    ("104.16.0.0/12", "Cloudflare"), ("172.64.0.0/13", "Cloudflare"),
    ("1.0.0.0/24", "Cloudflare"), ("188.114.96.0/22", "Cloudflare"),
    ("23.185.0.0/24", "Cloudflare"),
    ("119.91.0.0/16", "腾讯云CDN候选"), ("36.143.0.0/16", "移动CDN候选"),
]
CDN_ASN_KEYWORDS = ["cloudflare", "akamai", "fastly", "cdn77", "edgecast",
                    "wangsu", "china netcom", "cdn", "chinacache", "qianxun",
                    "tencent cloud", "alibaba", "kingsoft", "baishan"]
# 无业务默认页标题
DEAD_TITLES = re.compile(
    r"welcome to (nginx|openresty|iis|apache)|默认首页|测试页|"
    r"it works!|insert title here|没有任何页面|placeholder", re.I)
# 空壳跳转特征(302 Location 跳主站/错误页)
NOISE_REDIRECTS = re.compile(
    r"error\d*\.aspx|from=shopdomain|/404|/notfound|goto=www\.|"
    r"jfe\.html\?err|/public/jfe|file-no\.|\.com/$|/error|/not-exist|/domain", re.I)


def ip_in_cidr(ip: str, cidr: str) -> bool:
    try:
        import ipaddress
        return ipaddress.ip_address(ip) in ipaddress.ip_network(cidr)
    except ValueError:
        return False


def doh_query(name: str, qtype: str, timeout=12, resolver=None):
    """DoH 查询, 返回 [data, ...]。fake-ip 环境下系统 DNS 不可信, DoH 拿真实值。

    resolver 默认走阿里 DoH(解析目标域名); 查 .cymru.com 这类境外权威必须传 DOH_PUBLIC
    —— 阿里 DoH 对它们返回 SERVFAIL。此前只认 A(1)/CNAME(5), 现已支持 TXT(16)。
    """
    tpl = resolver or DOH
    want = {"A": 1, "CNAME": 5, "TXT": 16}.get(qtype.upper(), 1)
    try:
        req = urllib.request.Request(tpl.format(name, qtype), headers={"accept": "application/dns-json"})
        with urllib.request.urlopen(req, timeout=timeout, context=_SSL_CTX) as r:
            data = json.loads(r.read().decode())
        return [a["data"] for a in data.get("Answer", []) if a.get("type") == want]
    except Exception:
        return []


def wildcard_check(domain: str) -> bool:
    """通配符 DNS 检测: 随机子域(几乎不可能真实存在)有 A 记录 = *.domain 通配。
    通配域上「有解析」不再证明子域存在——凡解析到通配指向(同 IP/同 CNAME)的子域视为待验证,
    只能靠 HTTP 层判真伪(dedupe 的空壳过滤正好兜住)。
    2026-09-07 jd.com 实测: *.jd.com 全 CNAME 到 ym.gslb.qianxun.com, subfinder 26k 输出 74% 空壳。"""
    import time
    rand = f"zzznotexist{int(time.time())}xyz.{domain}"
    return bool(doh_query(rand, "A"))


def httpx_probe(hosts: list, timeout=10, batch=2000):
    """用 ProjectDiscovery httpx 探活, 返回 {host: (status, title, location)}。
    大列表分批(每批 2000, 单批 300s), 防一次性灌 26k 卡死。
    httpx 定位: PATH 优先 → ~/go/bin/httpx; ⚠️ 必须 ProjectDiscovery 版,
    /usr/bin/httpx 若是 Python httpx 库的同名 CLI 则参数不兼容(shutil.which 按序找)。"""
    import shutil
    httpx_bin = shutil.which("httpx") or ""
    # PATH 里的 httpx 若是 Python 库 CLI(无 -json 参数)则跳过, 用 ~/go/bin
    # 注意 Windows 下路径分隔符是 \ 且可执行文件带 .exe 后缀
    if not httpx_bin or ("go/bin" not in httpx_bin and "go\\bin" not in httpx_bin):
        for cand in (os.path.expanduser("~/go/bin/httpx"),
                     os.path.expanduser("~/go/bin/httpx.exe")):
            if os.path.exists(cand):
                httpx_bin = cand
                break
    if not httpx_bin:
        print("[!] 找不到 ProjectDiscovery httpx(装: go install github.com/projectdiscovery/httpx/cmd/httpx@latest)",
              file=sys.stderr)
        return {}
    out = {}
    for i in range(0, len(hosts), batch):
        chunk = hosts[i:i + batch]
        try:
            # -tech-detect: 技术栈指纹随探活一并抓(webanalyze 主用途的零成本替代)
            p = subprocess.run(
                [httpx_bin, "-silent", "-json", "-timeout", "8",
                 "-status-code", "-title", "-location", "-tech-detect", "-threads", "25"],
                input="\n".join(chunk), capture_output=True, text=True, timeout=300,
                encoding="utf-8", errors="replace")
            for line in p.stdout.splitlines():
                try:
                    j = json.loads(line)
                    h = j.get("input") or j.get("url", "").split("//")[-1].split("/")[0]
                    out[h] = (j.get("status_code"), j.get("title") or "",
                              j.get("location") or j.get("final_url") or "",
                              j.get("tech") or "")
                except (json.JSONDecodeError, KeyError):
                    continue
        except Exception:
            continue
    return out


def _cymru_txt(name: str, timeout=8) -> str:
    """查一条 Team Cymru TXT, 返回去掉引号的首个非空答案; 查不到返回 ""。"""
    for v in doh_query(name, "TXT", timeout, resolver=DOH_PUBLIC):
        v = str(v).strip().strip('"').strip()
        if v:
            return v
    return ""


def _reverse_ip(ip: str) -> str:
    """Team Cymru 的反转查询名: 1.2.3.4 -> 4.3.2.1; IPv6 走 nibble 反转。"""
    if ":" in ip:
        return ".".join(reversed(ipaddress.ip_address(ip).exploded.replace(":", "")))
    return ".".join(reversed(ip.split(".")))


def cymru_asn_lookup(ips: list):
    """Team Cymru DNS 查 ASN/组织名, 返回 {ip: (org, org, "AS<num>")}。

    返回**形状与旧的 ip_api_batch() 完全一致**, 所以下游 CDN_ASN_KEYWORDS 的 blob
    匹配逻辑一行都不用改。

    为什么换掉 ip-api(2026-09-27): 它用**明文 HTTP** 把本任务的整份目标 IP 列表
    POST 给第三方(100/批), 在 SRC 场景是可关联的侦查痕迹、且明文可被中间人读取;
    另外原实现没有节流(注释自称 45rpm 但循环里没有 sleep), 超量后 429 被
    `except Exception: continue` 静默吞掉, CDN_IP 标注大面积丢失且毫无提示。
    Team Cymru 是路由注册库(公开用途)、走 DoH 加密、单 IP 一次轻量查询。
    """
    out, asn_cache = {}, {}
    for ip in ips:
        try:
            asn = _cymru_txt(f"{_reverse_ip(ip)}.origin.asn.cymru.com").split("|")[0].strip()
        except Exception:
            continue
        if not asn.isdigit():
            continue
        if asn not in asn_cache:
            rec = _cymru_txt(f"AS{asn}.asn.cymru.com")
            # 格式: "13335 | US | arin | 2010-07-14 | CLOUDFLARENET - Cloudflare, Inc., US"
            asn_cache[asn] = rec.split("|")[-1].strip() if rec else ""
        org = asn_cache[asn]
        out[ip] = (org, org, f"AS{asn}")
    return out


def main():
    ap = argparse.ArgumentParser(description="资产自动去噪: CDN/空壳/死域/第三方 SaaS")
    ap.add_argument("--in", dest="infile", required=True, help="输入域名列表(每行一个 host[:port])")
    ap.add_argument("--out", dest="outfile", required=True, help="输出清洗后资产")
    ap.add_argument("--json", dest="jsonfile", help="机器可读报告 JSON")
    ap.add_argument("--threads", type=int, default=20)
    ap.add_argument("--skip-probe", action="store_true", help="跳过 httpx 探活(只做 DNS/CNAME/CDN 级去噪)")
    args = ap.parse_args()

    hosts = []
    with open(args.infile, encoding="utf-8") as f:
        for l in f:
            h = l.strip().split("/")[0]
            if h and not h.startswith("#"):
                hosts.append(h.split(":")[0])
    # 与 collect.norm() 同口径: 小写 + 去 *. 前缀 + 去尾点。
    # 此前不做归一化, dict.fromkeys 去重是**大小写敏感**的 → A.com 与 a.com 会双份进报告,
    # 跨脚本串联(collect → dedupe)时也会漏去重。2026-09-27 修。
    hosts = list(dict.fromkeys(
        h.lower().lstrip("*.").rstrip(".") for h in hosts if h))
    hosts = [h for h in hosts if h]
    print(f"[*] 输入 {len(hosts)} 个域名", file=sys.stderr)

    # Pass 0: 通配符 DNS 检测(根域级, 一次)
    # 通配域(如 *.jd.com)上「有解析」不再证明子域存在; 提示后靠 HTTP 层判真伪
    root = ".".join(hosts[0].split(".")[-2:]) if hosts else ""
    is_wildcard = wildcard_check(root) if root else False
    if is_wildcard:
        print(f"[!] ⚠️ 根域 {root} 是通配符 DNS(*.{root} 全解析) — 大列表里虚假子域比例高, "
              f"DNS 层判存活无效, 由 HTTP 空壳过滤兜底(通配指向的 302 错误页会被剔除)", file=sys.stderr)

    # Pass 1: DoH 解析(A + CNAME), 并行(通配域跳过 DNS 询问省一半时间)
    records = {}
    need_dns = hosts if not is_wildcard else hosts[:2000]  # 通配域: DNS 全假, 只抽前 2000 做 CNAME 指纹
    with cf.ThreadPoolExecutor(max_workers=args.threads) as ex:
        futs_a = {ex.submit(doh_query, h, "A"): h for h in need_dns}
        futs_c = {ex.submit(doh_query, h, "CNAME"): h for h in need_dns}
        a_map = {h: f.result() for f, h in futs_a.items()}
        c_map = {h: f.result() for f, h in futs_c.items()}

    for h in hosts:
        if is_wildcard and h not in a_map:
            records[h] = {"ips": [], "cname": "", "wildcard_unverified": True}
        else:
            records[h] = {"ips": a_map.get(h, []), "cname": (c_map.get(h) or [""])[0],
                          "wildcard_unverified": False}

    if is_wildcard:
        alive = hosts  # 通配域: 全部交给 HTTP 层判(真实子域有业务, 虚假 302 空壳)
        dead = set()
        print(f"[*] 通配域模式: {len(hosts)} 全部进 HTTP 探活(DNS 层不判生死)", file=sys.stderr)
    else:
        # dead 用 set: 下面 alive 的判定是 `h not in dead`, 用 list 就是 O(n×m)
        # (26k 主机 → 数亿次比较)。2026-09-27 修。
        dead = {h for h in hosts if not records[h]["ips"] and not records[h]["cname"]}
        alive = [h for h in hosts if h not in dead]
        print(f"[*] DNS 解析: {len(alive)} 活 / {len(dead)} 死(无解析)", file=sys.stderr)

    # Pass 2: CNAME 分类 — CDN 特征 / SaaS 特征 / CNAME 共享聚类
    cdn_by_cname = defaultdict(list)
    for h in alive:
        cn = records[h]["cname"]
        records[h]["cdn"] = None
        records[h]["saas"] = None
        if cn:
            for pat in CDN_CNAME_PATTERNS:
                if re.search(pat, cn, re.I):
                    records[h]["cdn"] = cn
                    # 共享同 CDN 接入后缀聚类(qianxun 类轮询 CNAME 不同但同源, 归到 2 级域名)
                    key = ".".join(cn.split(".")[-4:])
                    cdn_by_cname[key].append(h)
                    break
            else:
                for pat in SAAS_CNAME_PATTERNS:
                    if re.search(pat, cn, re.I):
                        records[h]["saas"] = cn
                        break

    # Pass 3: IP 级 CDN 判定(本地网段优先, 未命中走 Team Cymru ASN)
    all_ips = sorted({ip for h in alive for ip in records[h]["ips"]})
    local_cdn = {}
    api_need = []
    for ip in all_ips:
        hit = next((name for cidr, name in CDN_IP_RANGES if ip_in_cidr(ip, cidr)), None)
        if hit:
            local_cdn[ip] = hit
        else:
            api_need.append(ip)
    api_info = cymru_asn_lookup(api_need) if api_need else {}
    for ip in all_ips:
        if ip in local_cdn:
            continue
        info = api_info.get(ip)
        if info:
            blob = " ".join(info).lower()
            if any(k in blob for k in CDN_ASN_KEYWORDS):
                local_cdn[ip] = info[2] or info[0]

    for h in alive:
        records[h]["ip_cdn"] = next((local_cdn.get(ip, "") for ip in records[h]["ips"] if ip in local_cdn), None)

    # Pass 4: httpx 探活(可选) — 空壳/默认页/跳主站判定
    # 通配域大列表: 先抽 300 个样本预判空壳率, >70% 时提示后只对「非空壳样本率低」的输入全量跑
    if args.skip_probe:
        probes = {}
    elif len(alive) > 3000:
        import random
        sample = random.sample(alive, 300)
        print(f"[*] 大列表({len(alive)}): 先抽 300 样本预判空壳率 ...", file=sys.stderr)
        sp = httpx_probe(sample)
        n_shell = sum(1 for h in sample if sp.get(h, (None, "", ""))[0] in (301, 302)
                      and NOISE_REDIRECTS.search(sp[h][2] or ""))
        rate = n_shell / len(sample) if sample else 0
        print(f"[*] 样本空壳率 {rate:.0%} ({n_shell}/300)", file=sys.stderr)
        if rate > 0.7:
            print(f"[i] 空壳率 {rate:.0%} (>{int(rate*100)}%) — 全量探活预计大多被剔; 仍继续(分批, 耗时长)"
                  f" — 想快可 --skip-probe 或缩小输入", file=sys.stderr)
        probes = httpx_probe(alive)
    else:
        probes = httpx_probe(alive)
    for h in alive:
        st, title, loc, tech = probes.get(h, (None, "", "", ""))
        records[h]["status"] = st
        records[h]["title"] = title
        records[h]["tech"] = tech
        records[h]["noise"] = []
        if st:
            if DEAD_TITLES.search(title or ""):
                records[h]["noise"].append(f"默认页壳({title[:30]})")
            if st in (301, 302) and NOISE_REDIRECTS.search(loc or ""):
                records[h]["noise"].append(f"空壳跳转({loc[:50]})")

    # 汇总输出: keep(可用) / suspect(存疑: 404/405/410 等边缘) / removed(噪音壳)
    keep, suspect, cdn_kept, removed = [], [], [], []
    for h in alive:
        r = records[h]
        tags = []
        if r["cdn"]:
            tags.append("CDN域名")
        if r["ip_cdn"]:
            tags.append(f"CDN_IP({r['ip_cdn'][:20]})")
        if r["saas"]:
            tags.append(f"SaaS({r['saas'][:30]})")
        if r["noise"]:
            # 302 跳错误页/默认页 = 无业务壳 → 剔除
            removed.append((h, ";".join(r["noise"] + tags)))
            continue
        if r["status"] in (404, 405, 410, 500, 502, 503, 504):
            # 404/网关错误: 根路径死但常有业务 path(/api/xxx) → 存疑清单, 不进可用也不丢
            suspect.append((h, f"HTTP {r['status']} {r['title'][:20]}"))
            continue
        # 200/2xx/3xx(非噪音跳转)/401/403登录墙/未探到 → 保留(存活口径认 dig-scope §1.0.1)
        keep.append(h)
        if tags:
            cdn_kept.append((h, ";".join(tags)))

    with open(args.outfile, "w", encoding="utf-8") as f:
        for h in keep:
            f.write(h + "\n")

    print(f"\n[+] 保留(可用) {len(keep)}/{len(hosts)}", file=sys.stderr)
    if cdn_kept:
        print(f"[i] 其中 {len(cdn_kept)} 个带 CDN 标注(有业务,保留; 溯源真实 IP 见 03 §8):", file=sys.stderr)
        for h, t in cdn_kept[:10]:
            print(f"    {h}  [{t}]", file=sys.stderr)
    if suspect:
        print(f"[?] 存疑 {len(suspect)} 个(根路径 404/5xx — 常有业务 path, 供进站前复查):", file=sys.stderr)
        for h, why in suspect[:15]:
            print(f"    {h}  [{why}]", file=sys.stderr)
    if removed:
        print(f"[-] 剔除 {len(removed)} 个噪音:", file=sys.stderr)
        for h, why in removed[:20]:
            print(f"    {h}  [{why}]", file=sys.stderr)
    if dead:
        print(f"[-] 死域名 {len(dead)} 个(无解析, 全列表在 JSON 报告)", file=sys.stderr)

    # CNAME 共享聚类提示(40% 噪音率的 GSLB 共享发现)
    shared = {k: v for k, v in cdn_by_cname.items() if len(v) >= 3}
    if shared:
        print(f"[i] {len(shared)} 个 CDN 接入点被 3+ 域名共享(同源站,可考虑每簇留代表):", file=sys.stderr)
        for k, v in sorted(shared.items(), key=lambda x: -len(x[1]))[:5]:
            print(f"    {k}: {len(v)} 个域名", file=sys.stderr)

    if args.jsonfile:
        # dead 是 set(见上), JSON 序列化前要转回有序 list;
        # 用 with 保证异常时不会留下半截 JSON(此前是 json.dump(..., open(...)), 无 close 也无保护)。
        with open(args.jsonfile, "w", encoding="utf-8") as f:
            json.dump({"keep": keep, "suspect": suspect, "removed": removed,
                       "dead": sorted(dead) if isinstance(dead, set) else dead,
                       "records": records},
                      f, ensure_ascii=False, indent=1)
        print(f"[+] JSON 报告 → {args.jsonfile}", file=sys.stderr)


if __name__ == "__main__":
    main()
