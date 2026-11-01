#!/usr/bin/env python3
"""
dns_brute.py — 批量 DNS 解析 + 子域前缀字典爆破（无需 API key）

用法:
  python dns_brute.py --domain example.com                     # 内置 119 条前缀字典爆破
  python dns_brute.py --domain example.com --wordlist p.txt    # 自定义前缀(每行一个)
  python dns_brute.py --domains domains.txt                    # 批量域名(逐个解析,不爆破)
  python dns_brute.py --domain example.com --patterns          # 拼音缩写模式猜测

输出: stdout CSV(域名,IP) + /24 聚类统计; --out 写文件
替代本机缺失的 subfinder/dnsx 本地爆破部分(被动聚合仍用 amass/crt.sh)。
验证铁律: 有 A 记录 ≠ 可达 ≠ 属于目标, 结果须过归属三重确认(IP段/备案/页面内容)。
"""
import argparse
import concurrent.futures as cf
import socket
import sys
from collections import Counter

# 198.18.0.0/15 = RFC2544 基准网段; 本机走透明代理(fake-ip)时所有域名都解析成该段,
# 爆破结果全是假命中 —— 默认过滤, 并提示改用真实 DNS 或换网络环境。
FAKE_IP_PREFIX = "198.18."


def _is_fake(ip: str) -> bool:
    return ip.startswith(FAKE_IP_PREFIX)

PREFIXES = [
    # 认证/入口
    "sso", "login", "signin", "passport", "auth", "cas", "id", "uc", "elink",
    # 门户
    "portal", "my", "home", "www", "mail", "webmail",
    # 办公
    "oa", "office", "work", "workflow", "bpm", "form", "report",
    # 业务系统
    "erp", "crm", "scm", "pms", "tms", "wms", "hrm", "hr", "ehr", "fin", "pay", "billing",
    # 开发/运维
    "git", "gitlab", "jenkins", "jira", "confluence", "wiki", "doc", "help", "support",
    # API/服务
    "api", "open", "developer", "console", "dashboard", "admin", "manage", "backend",
    # 测试
    "test", "uat", "dev", "stage", "pre", "preprod", "demo", "sandbox",
    # 安全/监控
    "vpn", "safe", "sec", "waf", "monitor", "zabbix", "nagios", "grafana",
    # 存储
    "nas", "backup", "storage", "file", "upload", "download", "minio",
    # 基础架构
    "cloud", "k8s", "docker", "rancher", "harbor", "nexus", "elk", "kibana",
    # 通信
    "im", "chat", "meeting", "video", "voip",
    # 移动
    "m", "mobile", "app", "h5", "weixin", "wechat", "miniapp",
    # 业务
    "order", "trade", "shop", "store", "product", "logistics", "supplier", "vendor",
    # 数据
    "data", "bi", "etl", "sync", "db", "mysql", "redis", "mq", "kafka",
    # 国产组件
    "ny", "dsj", "tsp", "nc", "ncc", "yonyou", "u8", "kingdee",
]

# 国内企业域名拼音模式: 前缀 x 后缀组合猜「独立注册域」时用 --domain 给根域名不合适,
# --patterns 生成的是 子域.主域 的常见业务词组合
PATTERN_SUFFIXES = ["fd", "sdz", "power", "solar", "silicon", "pv", "energy", "electric", "tech", "cloud"]


def resolve(host: str, timeout: float = 3.0):
    """解析 host, 返回 (host, [ips]) 或 (host, None)。过滤 fake-ip(198.18.0.0/15)。"""
    try:
        infos = socket.getaddrinfo(host, None, socket.AF_INET)
        ips = sorted({i[4][0] for i in infos if not _is_fake(i[4][0])})
        return host, ips or None
    except (socket.gaierror, socket.timeout, OSError):
        return host, None


def brute(domain: str, prefixes, workers: int = 64):
    targets = [f"{p}.{domain}" for p in prefixes]
    found = []
    with cf.ThreadPoolExecutor(max_workers=workers) as ex:
        for host, ips in ex.map(lambda h: resolve(h), targets):
            if ips:
                found.append((host, ips))
                print(f"{host},{','.join(ips)}", file=sys.stderr)
    return found


def cluster(found):
    c = Counter()
    for _, ips in found:
        for ip in ips:
            c[".".join(ip.split(".")[:3]) + ".0/24"] += 1
    return c


def main():
    ap = argparse.ArgumentParser(description="批量 DNS 解析 + 前缀字典爆破")
    ap.add_argument("--domain", help="目标主域名(爆破模式)")
    ap.add_argument("--domains", help="域名列表文件(批量解析,不爆破)")
    ap.add_argument("--wordlist", help="自定义前缀字典(每行一个)")
    ap.add_argument("--patterns", action="store_true",
                    help="额外跑拼音/业务后缀组合猜测")
    ap.add_argument("--out", help="结果 CSV 输出文件(默认 stdout)")
    ap.add_argument("--workers", type=int, default=64)
    args = ap.parse_args()

    out = open(args.out, "w", encoding="utf-8") if args.out else sys.stdout
    found = []

    if args.domains:
        domains = [l.strip() for l in open(args.domains, encoding="utf-8") if l.strip() and not l.startswith("#")]
        with cf.ThreadPoolExecutor(max_workers=args.workers) as ex:
            for host, ips in ex.map(resolve, domains):
                if ips:
                    found.append((host, ips))
        for host, ips in found:
            print(f"{host},{','.join(ips)}", file=out)
    elif args.domain:
        prefixes = list(PREFIXES)     # 必须拷贝: 否则下面的 += 会原地改模块级常量, 同进程二次调用累积
        if args.wordlist:
            prefixes += [l.strip() for l in open(args.wordlist, encoding="utf-8") if l.strip() and not l.startswith("#")]
        if args.patterns:
            # 常见业务前缀 × 业务后缀的真组合(如 api-power / data-fd / cloud-pv)
            base = [p for p in prefixes if p in ("api", "app", "m", "data", "cloud", "www")]
            combos = [f"{b}-{s}" for b in base for s in PATTERN_SUFFIXES]
            prefixes = list(dict.fromkeys(prefixes + combos))
        found = brute(args.domain, prefixes, args.workers)
        print("# 命中结果 (域名,IP)", file=out)
        for host, ips in found:
            print(f"{host},{','.join(ips)}", file=out)
    else:
        ap.error("需要 --domain 或 --domains")
        return

    # /24 聚类
    # 注意: 这些行必须是 `#` 注释 —— 它们不是主机名。此前不打 `#`, 被 collect.py 的
    # `not l.startswith("#")` 过滤放过, 再经 norm() 的 split("/")[0] 变成假主机名
    # (如 "104.16.0.0") 注入 raw_subs.txt, 让下游 dedupe 白跑一轮 DoH。2026-09-27 修。
    c = cluster(found)
    print("\n# /24 聚类(段, 域名数) — 同段 5+ = 核心集群; 独占段 = 独立系统可能弱防护", file=out)
    for seg, n in c.most_common():
        print(f"#{seg},{n}", file=out)

    print(f"\n# 共命中 {len(found)} 个子域 / {len(c)} 个 /24 段", file=out)
    if out is not sys.stdout:
        out.close()
    # 提示
    print("[i] 有 A 记录 ≠ 可达 ≠ 属于目标; 过 IP 段归属/备案/页面内容三重确认再入资产表", file=sys.stderr)
    print("[i] 若全 0 命中但预期应有: 本机若走透明代理(fake-ip), 系统 DNS 全返 198.18.x 已被过滤;"
          " 可换真实 DNS 环境/直连网络重跑, 或用 amass/crt.sh 被动源替代", file=sys.stderr)


if __name__ == "__main__":
    main()
