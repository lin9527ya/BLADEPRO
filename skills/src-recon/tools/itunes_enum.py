#!/usr/bin/env python3
"""
itunes_enum.py — App Store 枚举(iTunes Search API, 免 key): 主体发现 + 全 App 枚举

用法:
  python tools/itunes_enum.py --term 字节跳动                    # 品牌词 → 列各主体(sellerName)及名下 App
  python tools/itunes_enum.py --artist-id 1170416082            # 指定主体全 App 枚举(200 条上限)
  python tools/itunes_enum.py --term 抖音 --seller-contains Douyin --out apps.csv
  python tools/itunes_enum.py --artist-id 1701815329 --country us

输出: stdout 按主体分组清单; --out 写 CSV(bundle_id,track_name,seller_name,artist_id,release_date)
反哺(references/07 §2): sellerName=归属判据; bundleId 代号→域名字典/测绘(实测 hubble/yumme/spark/sicily);
  新主体→01 公司树; 拿到 Android 版名→应用宝查 APK 直链。
局限: 仅收录上架 App, 企业分发/未上架不在; term 搜索有模糊噪音, 用 --seller-contains 过滤。
"""
import argparse
import csv
import json
import ssl
import sys
import time
import urllib.parse
import urllib.request

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


def fetch(url: str, timeout=20, retries=2):
    """取 JSON。**失败不抛** —— 返回 {}，调用方 `fetch(...).get("results", [])` 自然降级为空列表。

    此前是裸奔(urlopen + json.loads 全无保护): Apple 限流 / 403 / 返回 HTML 时直接
    traceback 把整个脚本打死, 前面已经枚举到的结果全丢。全项目其他 5 处 HTTP 调用
    都有兜底, 只有这里没有。2026-09-27 修。
    """
    last = ""
    for attempt in range(retries + 1):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
            with urllib.request.urlopen(req, timeout=timeout, context=_SSL_CTX) as r:
                return json.loads(r.read().decode("utf-8"))
        except Exception as e:
            last = str(e)[:90]
            if attempt < retries:
                time.sleep(1.5 * (attempt + 1))     # 退避重试, 别把限流打成封禁
    print(f"[!] 取数失败(已重试 {retries} 次): {last}", file=sys.stderr)
    return {}


def norm_apps(results):
    """过滤非软件条目 + 去重(bundleId 为准), 返回统一字段 dict 列表。"""
    seen, apps = set(), []
    for a in results:
        if a.get("kind") != "software":
            continue
        bid = a.get("bundleId") or ""
        key = bid or a.get("trackId")
        if key in seen:
            continue
        seen.add(key)
        apps.append({
            "bundle_id": bid,
            "track_name": a.get("trackName") or "",
            "seller_name": a.get("sellerName") or "",
            "artist_id": a.get("artistId") or "",
            "release_date": (a.get("currentVersionReleaseDate") or "")[:10],
        })
    return apps


def search_term(term: str, country: str, limit=50):
    url = ("https://itunes.apple.com/search?"
           + urllib.parse.urlencode({"term": term, "entity": "software",
                                     "limit": limit, "country": country}))
    return norm_apps(fetch(url).get("results", []))


def lookup_artist(artist_id: str, country: str, limit=200):
    url = ("https://itunes.apple.com/lookup?"
           + urllib.parse.urlencode({"id": artist_id, "entity": "software",
                                     "limit": limit, "country": country}))
    return norm_apps(fetch(url).get("results", []))


def main():
    ap = argparse.ArgumentParser(description="App Store 枚举(iTunes Search API)")
    ap.add_argument("--term", help="品牌词/公司名/产品名(主体发现)")
    ap.add_argument("--artist-id", help="主体 artistId(全 App 枚举)")
    ap.add_argument("--seller-contains", default="", help="按 sellerName 子串过滤(去 term 噪音)")
    ap.add_argument("--country", default="cn", help="商店区域(默认 cn; 海外线用 us 等)")
    ap.add_argument("--out", help="CSV 输出路径")
    args = ap.parse_args()

    if not args.term and not args.artist_id:
        ap.error("至少要给 --term 或 --artist-id")

    if args.artist_id:
        apps = lookup_artist(args.artist_id, args.country)
        groups = {args.artist_id: apps}
    else:
        apps = search_term(args.term, args.country)
        if args.seller_contains:
            apps = [a for a in apps if args.seller_contains.lower() in a["seller_name"].lower()]
        groups = {}
        for a in apps:
            groups.setdefault(a["seller_name"] or "(unknown)", []).append(a)

    # stdout 分组清单
    for seller, items in groups.items():
        print(f"== {seller} ({len(items)} apps)")
        for a in items:
            print(f"   {a['bundle_id']} | {a['track_name']} | {a['release_date']} | artist={a['artist_id']}")

    if args.out:
        with open(args.out, "w", newline="", encoding="utf-8") as f:
            w = csv.DictWriter(f, fieldnames=["bundle_id", "track_name", "seller_name",
                                              "artist_id", "release_date"])
            w.writeheader()
            for items in groups.values():
                w.writerows(items)
        print(f"[+] {sum(len(v) for v in groups.values())} apps → {args.out}", file=sys.stderr)


if __name__ == "__main__":
    main()
