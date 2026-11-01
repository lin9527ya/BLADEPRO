#!/usr/bin/env python3
"""
bucket_enum.py — 对象存储桶枚举（S3 / 阿里 OSS / 腾讯 COS / Azure Blob / 华为 OBS / 百度 BOS / 金山 KS3 / 京东云 OSS）

命名组合穷举 + DoH 解析判存在 + HTTP 三态判定(可列🔥/私有存在/不存在)。

用法:
  python tools/bucket_enum.py --brand jd --suffixes prod,dev,backup,static,file
  python tools/bucket_enum.py --brand aliyun --cloud s3            # 只测 S3
  python tools/bucket_enum.py --brand jd --extra-words mall,media  # 追加品牌词
  python tools/bucket_enum.py --brand jd --known jd-bucket-1       # 已知桶名直验

输出: stdout CSV(桶名,云,区域,状态,样例) + --out 写文件
判定(2026-09-07 口径, 对齐 07 §4):
  200 + ListBucketResult → 🔥可列桶(高危, 记录进资产表)
  403 / AccessDenied     → 私有桶存在(值得记, 备注私有)
  NoSuchBucket / 404     → 不存在
  DoH 无解析              → 不存在(省一次 HTTP)
局限: 七牛(qiniu)/又拍(UPYUN)走自定义加速域名、无固定桶子域, 本脚本不可枚举——
  只能从 JS/泄露/页面引用里找完整绑定域名(见 07 §4)。
"""
import argparse
import concurrent.futures as cf
import csv
import json
import sys
import urllib.request
import urllib.error

# Windows GBK 管道下 emoji 会 UnicodeEncodeError 崩(🔥/⚠), 换 replace 保命
for _s in (sys.stdout, sys.stderr):
    try:
        _s.reconfigure(errors="replace")
    except Exception:
        pass

DOH = "https://223.5.5.5/resolve?name={}&type=A"
UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"

# 云厂商域名模板(区域=None 表示无区域概念或自动路由)
CLOUDS = {
    "s3":    [("{b}.s3.amazonaws.com", None)],
    "oss":   [("{b}.oss-cn-beijing.aliyuncs.com", "cn-beijing"),
              ("{b}.oss-cn-hangzhou.aliyuncs.com", "cn-hangzhou"),
              ("{b}.oss-cn-shenzhen.aliyuncs.com", "cn-shenzhen"),
              ("{b}.oss-cn-shanghai.aliyuncs.com", "cn-shanghai"),
              ("{b}.oss-cn-guangzhou.aliyuncs.com", "cn-guangzhou"),
              ("{b}.oss-cn-qingdao.aliyuncs.com", "cn-qingdao"),
              ("{b}.oss-cn-zhangjiakou.aliyuncs.com", "cn-zhangjiakou"),
              ("{b}.oss-cn-huhehaote.aliyuncs.com", "cn-huhehaote"),
              ("{b}.oss-cn-chengdu.aliyuncs.com", "cn-chengdu"),
              ("{b}.oss-cn-hongkong.aliyuncs.com", "cn-hongkong")],
    "cos":   [("{b}.cos.ap-guangzhou.myqcloud.com", "ap-guangzhou"),
              ("{b}.cos.ap-shanghai.myqcloud.com", "ap-shanghai"),
              ("{b}.cos.ap-beijing.myqcloud.com", "ap-beijing"),
              ("{b}.cos.ap-nanjing.myqcloud.com", "ap-nanjing"),
              ("{b}.cos.ap-chengdu.myqcloud.com", "ap-chengdu"),
              ("{b}.cos.ap-hongkong.myqcloud.com", "ap-hongkong")],
    "azure": [("{b}.blob.core.windows.net", None)],
    "obs":   [("{b}.obs.cn-north-4.myhuaweicloud.com", "cn-north-4"),
              ("{b}.obs.cn-east-3.myhuaweicloud.com", "cn-east-3")],
    "bos":   [("{b}.bj.bcebos.com", "bj"),
              ("{b}.gz.bcebos.com", "gz")],
    "ks3":   [("{b}.ks3-cn-beijing.ksyun.com", "cn-beijing"),
              ("{b}.ks3-cn-shanghai.ksyun.com", "cn-shanghai")],
    "jdoss": [("{b}.s3.cn-north-1.jdcloud-oss.com", "cn-north-1")],
}

DEFAULT_SUFFIXES = ["", "prod", "dev", "test", "uat", "backup", "bak", "static",
                    "file", "files", "img", "image", "images", "media", "cdn",
                    "data", "log", "logs", "app", "web", "www", "download", "doc", "docs"]


def doh_resolve(host):
    try:
        req = urllib.request.Request(DOH.format(host), headers={"accept": "application/dns-json"})
        with urllib.request.urlopen(req, timeout=8) as r:
            data = json.loads(r.read().decode())
        return any(a.get("type") == 1 for a in data.get("Answer", []))
    except Exception:
        return False


def probe(host):
    """HTTP 探测桶, 返回 (status, note)。
    注意: OSS 域名是通配符 DNS(任意桶名都解析), DNS 判存在性对 OSS 无效 — 全靠本函数判。"""
    url = f"http://{host}/"
    try:
        req = urllib.request.Request(url, headers={"User-Agent": UA})
        with urllib.request.urlopen(req, timeout=10) as r:
            body = r.read(2048).decode(errors="ignore")
            if "ListBucketResult" in body:
                import re
                keys = re.findall(r"<Key>([^<]{0,40})", body)[:3]
                return "LISTABLE", "; ".join(keys)
            return f"OK-{r.status}", body[:60].replace("\n", " ")
    except urllib.error.HTTPError as e:
        body = ""
        try:
            body = e.read(512).decode(errors="ignore")
        except Exception:
            pass
        if ("NoSuchBucket" in body or "ResourceNotFound" in body
                or "InvalidBucketName" in body):
            return "NOT_EXIST", ""
        if "Missing required header" in body and "appid" in body.lower():
            # COS 要求 <bucket>-<appid> 格式: 纯名字不带 appid → 无法判, 标记需补 appid
            return "NEED_APPID", "COS 桶名需 <name>-<appid> 格式(从 JS/泄露找完整桶名)"
        if e.code in (403, 401):
            # AccessDenied + "does not belong to you" = 桶存在但私有(OSS 私桶特征)
            return "PRIVATE", body[:60].replace("\n", " ") or "AccessDenied"
        if "AccountRequiresHttps" in body:
            # Azure: 账号存在但只收 HTTPS(桶存在线索)
            return "PRIVATE", "AccountRequiresHttps(HTTPS-only 账号)"
        return f"HTTP-{e.code}", body[:40].replace("\n", " ")
    except Exception:
        return "ERR", ""


def check(job):
    host, cloud, region, bucket = job
    # OSS/COS 等通配符 DNS 云(任意桶名都解析), DNS 判存在性无效 → 直接 HTTP 判
    if cloud not in ("oss", "cos", "obs", "bos", "ks3", "jdoss") and not doh_resolve(host):
        return None  # 无解析=不存在, 省 HTTP
    status, note = probe(host)
    # 白名单: 只认明确的「存在」信号, 网络 ERR/杂项状态码(HTTP-400/409/5xx 等)
    # 不算命中——否则 COS 无 appid 名、超时、区域错配的 400 会污染输出与统计
    if status == "LISTABLE" or status == "PRIVATE" or status == "NEED_APPID":
        return (bucket, cloud, region, status, note)
    if status.startswith("OK-"):  # OK-200/206 等 2xx 命中
        return (bucket, cloud, region, status, note)
    return None


def main():
    ap = argparse.ArgumentParser(description="对象存储桶枚举(S3/OSS/COS/Azure)")
    ap.add_argument("--brand", help="品牌/公司名(小写)。与 --known 至少给一个")
    ap.add_argument("--suffixes", default=",".join(DEFAULT_SUFFIXES),
                    help="逗号分隔后缀(空串=裸品牌名)")
    ap.add_argument("--extra-words", default="", help="追加品牌变体词(拼音/缩写)")
    ap.add_argument("--cloud", default="all",
                    choices=list(CLOUDS) + ["all"],
                    help="s3/oss/cos/azure/obs/bos/ks3/jdoss/all")
    ap.add_argument("--known", default="",
                    help="已知桶名直验(逗号分隔) —— 给了它就**跳过穷举**, 只验这些名字, 不再需要 --brand")
    ap.add_argument("--out", help="CSV 输出文件")
    ap.add_argument("--threads", type=int, default=16)
    args = ap.parse_args()

    # --known 的语义按帮助文本实现: 直验模式跳过穷举。
    # 此前虽然写着「跳过穷举」, 实现却仍拿 brand×suffixes 生成全量组合, 且 --brand 还是 required
    # —— 想快速直验一个桶, 实际会跑完 24×N×云端点 的全量组合。2026-09-27 修。
    if args.known.strip():
        buckets = {b.strip() for b in args.known.split(",") if b.strip()}
        print(f"[i] --known 直验模式: 跳过穷举, 只验 {len(buckets)} 个给定桶名", file=sys.stderr)
    else:
        if not args.brand:
            ap.error("--brand 与 --known 至少要给一个")
        brands = {args.brand.lower()}
        for w in args.extra_words.split(","):
            if w.strip():
                brands.add(w.strip().lower())
        sfxs = [s.strip() for s in args.suffixes.split(",")]
        # 生成桶名候选: brand / brand-sfx / brandsfx
        buckets = set()
        for b in brands:
            for s in sfxs:
                buckets.add(b)
                buckets.add(f"{b}-{s}" if s else b)
                if s:
                    buckets.add(f"{b}{s}")
    buckets.discard("")

    clouds = list(CLOUDS) if args.cloud == "all" else [args.cloud]
    jobs = []
    for bucket in buckets:
        for c in clouds:
            for tmpl, region in CLOUDS[c]:
                jobs.append((tmpl.format(b=bucket), c, region, bucket))

    print(f"[*] 桶名候选 {len(buckets)} × 云端点 {len(jobs)} 个组合", file=sys.stderr)

    found = []
    with cf.ThreadPoolExecutor(max_workers=args.threads) as ex:
        for r in ex.map(check, jobs):
            if r:
                found.append(r)
                print(f"[+] {r[0]},{r[1]},{r[2]},{r[3]},{r[4]}", file=sys.stderr)

    # csv.writer 处理 note 内逗号/换行(手拼 CSV 会串列)
    out = open(args.out, "w", newline="", encoding="utf-8") if args.out else sys.stdout
    w = csv.writer(out)
    w.writerow(["bucket", "cloud", "region", "status", "note"])
    for r in sorted(found):
        w.writerow(r)
    if out is not sys.stdout:
        out.close()

    n_list = sum(1 for r in found if r[3] == "LISTABLE")
    print(f"\n[+] 命中 {len(found)} 个桶(可列 {n_list} 🔥 / 私有 {len(found)-n_list}); "
          f"未列出的为不存在或无解析。可列桶→资产表标 🔥, 进站归 src-dig", file=sys.stderr)


if __name__ == "__main__":
    main()
