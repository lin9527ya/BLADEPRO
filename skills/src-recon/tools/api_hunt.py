#!/usr/bin/env python3
"""
api_hunt.py — API/文档/调试端点批量发现（对 dedupe 出的 clean.txt 跑）

内置 90 条精选路径(swagger/openapi/graphql/actuator/druid/BFF/版本漂移/.git/.env),
对每个 host 并发探测(http+https), 特征判定命中类型, 只记录不改打 — 验证归 src-dig。

用法:
  python tools/api_hunt.py --in clean.txt --out api_hits.csv
  python tools/api_hunt.py --in clean.txt --threads 40 --timeout 8
  python tools/api_hunt.py --in clean.txt --paths my_paths.txt   # 自定义路径(每行一条)

判定:
  Swagger UI     swagger-ui 资源特征(webjars/swagger-ui.css/bundle)
  OpenAPI JSON   body 含 "openapi"/"swagger" 字段(JSON)
  GraphQL        graphiql/playground 特征
  Actuator       /actuator 返回 _links; /actuator/env 直接出 JSON
  Druid          /druid/ 302→login.html
  API JSON       200 且 Content-Type json 且非错误对象
  30x 跟一跳; **4xx 一律不记**; catch-all 壳(随机路径对照同响应)整站不记
  2026-09-13 修正: ①404 页正文里的关键词不再算命中 ②开放平台统一错误体 {"data":..,"error_code":N} 不算
  ③先打一条随机路径做壳基线, 全路径比对(治 SPA catch-all 回 index.html)
输出: CSV(host, url, 类型, 状态码, 特征) — 资产表备注 🔥
"""
import argparse
import concurrent.futures as cf
import csv
import hashlib
import random
import ssl
import string
import sys
import time
import urllib.request
import urllib.error

# 复用 collect.py 的 host 归一化(与 rescan.py 同一做法: 把 tools/ 加进 sys.path 再导入)。
# 这样无论当脚本跑还是被 import, 都能找到同级模块。
import os
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from collect import norm  # noqa: E402

# Windows GBK 控制台防崩。本文件 docstring 里有 🔥，目前只是侥幸没被打印
# (argparse 不打印 docstring)；一旦哪天把它打进 help 或日志就会 UnicodeEncodeError。
# 与 dedupe/bucket_enum/itunes_enum/rescan 四处保持一致。2026-09-27 补。
for _s in (sys.stdout, sys.stderr):
    try:
        _s.reconfigure(errors="replace")
    except Exception:
        pass

# Windows Python 默认不加载系统 CA → https 自签/内网目标会 SSLVerifyError 被吞成跳过(大面积漏报)。
# certifi 优先; 无 certifi 降级不验证(信息收集场景可接受, 只收不打)。
try:
    import certifi
    _SSL_CTX = ssl.create_default_context(cafile=certifi.where())
except ImportError:
    _SSL_CTX = ssl.create_default_context()
    _SSL_CTX.check_hostname = False
    _SSL_CTX.verify_mode = ssl.CERT_NONE

UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"

PATHS = [
    # OpenAPI / Swagger
    "/swagger-ui.html", "/swagger-ui/", "/swagger-ui/index.html", "/swagger",
    "/swagger-resources", "/v2/api-docs", "/v3/api-docs", "/api-docs",
    "/openapi.json", "/swagger.json", "/api/swagger.json", "/api/openapi.json",
    "/api-docs/swagger.json", "/docs/", "/docs/swagger", "/doc.html",
    # GraphQL
    "/graphql", "/graphiql", "/v1/graphql", "/api/graphql", "/gql", "/playground",
    # Java 中间件 / 调试
    "/actuator", "/actuator/env", "/actuator/health", "/actuator/heapdump",
    "/actuator/gateway/routes", "/actuator/mappings", "/actuator/beans",
    "/actuator/threaddump", "/actuator/logfile",
    "/druid/", "/druid/login.html", "/druid/index.html", "/console/", "/jmx-console/", "/manager/html",
    "/web-console/", "/resin-admin/", "/nacos/", "/eureka/", "/configprops",
    "/_cat/indices", "/debug/pprof",
    # BFF / 前端专属 (api-recon-and-docs)
    "/bff/", "/__api/", "/_next/data", "/api/bff", "/gw/", "/gateway/",
    # 版本漂移
    "/api/v1/", "/api/v2/", "/api/v3/", "/api/mobile/v1/", "/legacy/",
    "/v1/", "/v2/", "/rest/", "/api/rest",
    # 通用 API / 调试
    "/api/", "/api", "/health", "/healthz", "/status", "/ping", "/debug/",
    "/phpinfo.php", "/.well-known/", "/.well-known/openid-configuration", "/server-status", "/info", "/metrics",
    "/trace", "/env", "/dump", "/routes", "/mappings", "/beans", "/config",
    # 泄露/配置
    "/.git/config", "/.env",
    # 文档/配置
    "/robots.txt", "/sitemap.xml", "/crossdomain.xml", "/config.js", "/env.js",
    "/settings.json", "/config.json", "/package.json", "/manifest.json",
]


def _fp(status: int, body: str) -> str:
    """响应指纹(状态码+长度+正文哈希): 随机路径对照用, 判 catch-all 壳"""
    return f"{status}:{len(body)}:{hashlib.md5(body.encode('utf-8', 'ignore')).hexdigest()}"


# classify() 内部一律只看正文前 4000 字符 —— 基线取指纹时**必须给同一个量**。
# 历史 bug(2026-09-27 修): 基线读 r.read(8192) 不做截断、探测路径读 4096 字节,
# 两边长度必然不等 → 指纹永不相等 → catch-all 壳判定对**所有 >4KB 的页面完全失效**
# (SPA 回 index.html 的典型形态就是 5~50KB), 于是全路径假命中。
# 读 32KB 是为了保证即使正文是 3 字节/字的中文也能取满 4000 字符。
_FP_READ_BYTES = 32768
_FP_CHARS = 4000


def _read_body(stream) -> str:
    """从响应流读正文, 统一截断到 classify() 使用的长度。基线/探测两条路都必须走这里。"""
    return stream.read(_FP_READ_BYTES).decode(encoding="utf-8", errors="ignore")[:_FP_CHARS]


def classify(path: str, status: int, body: str, ctype: str, final_url: str, baseline: str = None):
    """按响应特征判定命中类型, 返回类型名或 None(不记)"""
    b = body[:4000]
    low = b.lower()
    if baseline is not None and _fp(status, b) == baseline:
        return None  # catch-all 壳: 任意路径同响应(SPA 回 index.html / 网关统一错误页), 全路径不记
    if 400 <= status < 500:
        # 4xx 一律不记。此前只排除 (404,403,401,444), 导致 400/429/451 会继续往下走判定,
        # 而下面若干分支(swagger/graphiql/_links/.git/.env/信息文件)不校验 status ——
        # 一个 400 响应只要正文 >100 字符, 在 /robots.txt 上就能被记成「信息文件」假命中。
        # 这里按 docstring 写明的口径收紧。2026-09-27 修。
        return None  # (2026-09-13 修正: 旧版把 404 页正文里的 swagger/graphql 字样当命中)
    # 444/非标准码 + WAF 拦截文案 = 误报, 直接不记
    if "request forbidden" in low or "请求被拒绝" in low:
        return None
    if "swagger" in low or "openapi" in low:
        if '"openapi"' in b or '"swagger"' in b or '"swagger":"2' in b:
            return "OpenAPI JSON"
        # 仅 UI 资源特征才算(2026-09-13 修正: 页面里出现 swagger 字样≠有 UI)
        if "swagger-ui" in low and ("webjars" in low or "swagger-ui.css" in low or "swagger-ui-bundle" in low):
            return "Swagger UI"
        return None
    if "graphiql" in low or "playground" in low:
        return "GraphQL"  # 同上修正: 去掉裸 graphql 关键词
    if "_links" in b and ("actuator" in final_url or path.startswith("/actuator")):
        return "Actuator"
    if path.endswith("gateway/routes") and (b.lstrip().startswith("{") or b.lstrip().startswith("[")):
        return "SCG Routes"  # Spring Cloud Gateway 路由表裸奔
    if path == "/_cat/indices" and status == 200 and ("elastic" in low or b.lstrip().startswith(("green", "yellow", "red"))):
        return "ES 未授权"
    if path == "/debug/pprof" and status == 200 and "goroutine" in low:
        return "pprof"
    if path == "/.git/config" and "[core]" in b:
        return ".git 泄露"
    if path == "/.env" and ("=" in b and any(k in b for k in ("KEY=", "SECRET=", "PASSWORD=", "TOKEN=", "APP_"))):
        return ".env 泄露"
    if path.endswith("openid-configuration") and status == 200 and "issuer" in b:
        return "OIDC 配置"
    if "druid" in low and ("login" in low or "druid" in path) and status == 200:
        return "Druid"
    if path == "/phpinfo.php" and "phpinfo" in low:
        return "phpinfo"
    if path in ("/robots.txt", "/sitemap.xml", "/crossdomain.xml"):
        return "信息文件" if len(b) > 100 else None
    if status == 200 and "json" in ctype:
        # 有结构感的 JSON(排除纯错误对象)
        if b.lstrip().startswith("{") or b.lstrip().startswith("["):
            if '"code"' in b and '"msg"' in b and len(b) < 300:
                return None  # 统一错误壳
            if "error_code" in b or '"message":"error"' in b.replace(" ", ""):
                return None  # 开放平台统一错误体(头条/抖音网关 {"data":..,"error_code":22}), 非 API 面
            if any(k in b for k in ("未登录", "请先登录", "登录已过期", "if_not_login", '"st":401')):
                return None  # 业务网关统一登录壳(2026-09-13 实测: 内部系统 /api/* 全返 {"st":401,"msg":"未登录","data":null})
            if any(k in b for k in ('"hello":"world"', '"ping":"pong"', '"message":"pong"')):
                return None  # 脚手架/健康检查端点, 非业务 API
            return "API JSON"
    if status in (301, 302) and any(k in path for k in ("druid", "console", "manager", "nacos")):
        return "中间件(302)"
    return None



def probe_host_paths(host, paths, timeout, deadline_s=120):
    """探测一个 host 的所有路径, 返回 [(host, url, 类型, 状态, 特征)]。
    deadline_s: host 级总时限——坏 host 不该用 80+ 路径 × 8s 占死一个 worker。"""
    hits = []
    deadline = time.monotonic() + deadline_s
    scheme = None
    # 先定 scheme: https 优先, 失败换 http
    for sch in ("https", "http"):
        try:
            req = urllib.request.Request(f"{sch}://{host}/", headers={"User-Agent": UA})
            urllib.request.urlopen(req, timeout=timeout, context=_SSL_CTX)
            scheme = sch
            break
        except urllib.error.HTTPError:
            scheme = sch  # 有 HTTP 响应就算(403/404 站也探)
            break
        except Exception:
            continue
    if scheme is None:
        return []
    # catch-all 对照: 先打一条随机路径, 其响应指纹作为"壳基线"(SPA 回 index.html / 网关统一错误页)
    baseline = None
    rnd = "".join(random.choices(string.ascii_lowercase + string.digits, k=12))
    try:
        req = urllib.request.Request(f"{scheme}://{host}/zz-{rnd}", headers={"User-Agent": UA})
        with urllib.request.urlopen(req, timeout=timeout, context=_SSL_CTX) as r:
            baseline = _fp(r.status, _read_body(r))
    except urllib.error.HTTPError as e:
        try:
            baseline = _fp(e.code, _read_body(e))
        except Exception:
            baseline = None
    except Exception:
        baseline = None
    for p in paths:
        if time.monotonic() > deadline:
            break  # 该 host 超总时限, 放弃剩余路径
        url = f"{scheme}://{host}{p}"
        try:
            req = urllib.request.Request(url, headers={"User-Agent": UA})
            with urllib.request.urlopen(req, timeout=timeout, context=_SSL_CTX) as r:
                body = _read_body(r)
                ctype = r.headers.get("Content-Type", "")
                # 跟随跳转后拿到的内容不算命中(常为登录页/首页 → 假 API JSON/假 SCG Routes)
                t = classify(p, r.status, body, ctype, r.url, baseline) if r.url == url else None
                if t:
                    feat = next((k for k in ("swagger", "openapi", "_links", "graphiql",
                                             "druid", "phpinfo", '"paths"', '"data"')
                                 if k in body[:4000]), "")
                    hits.append((host, url, t, r.status, feat))
        except urllib.error.HTTPError as e:
            try:
                body = _read_body(e)
            except Exception:
                body = ""
            t = classify(p, e.code, body, e.headers.get("Content-Type", ""), url, baseline)
            if t:
                hits.append((host, url, t, e.code, ""))
        except Exception:
            continue
    return hits


def main():
    ap = argparse.ArgumentParser(description="API/文档/调试端点批量发现")
    ap.add_argument("--in", dest="infile", required=True, help="host 列表(clean.txt)")
    ap.add_argument("--out", dest="outfile", required=True, help="命中 CSV 输出")
    ap.add_argument("--paths", help="自定义路径字典(每行一条, 默认内置)")
    ap.add_argument("--threads", type=int, default=20)
    ap.add_argument("--timeout", type=int, default=8)
    args = ap.parse_args()

    # host 归一化复用 collect.norm()(全项目口径最全: 小写/去 *. 前缀/去 path/port),
    # 之前这里只有 `split(":")[0]` —— 喂 URL 列表时 `https://x.com/a` 会变成 "https",
    # 然后静默去探 https://https/ (且大小写/通配前缀都不处理, 与 collect 口径不一致)。
    # norm() 处理不了带 scheme 的整条 URL(会整个丢掉), 所以先把 scheme 剥掉。
    # 2026-09-27 修。
    with open(args.infile, encoding="utf-8") as _f:
        raw_hosts = [re.sub(r"^[a-z][a-z0-9+.\-]*://", "", l.strip(), flags=re.I)
                     for l in _f if l.strip() and not l.startswith("#")]
    hosts = norm(raw_hosts)
    if not hosts:
        sys.exit(f"[!] {args.infile} 里没有解析出合法 host —— 检查输入(应为每行一个 host[:port] 或 URL)")
    paths = PATHS
    if args.paths:
        paths = [l.strip() for l in open(args.paths, encoding="utf-8") if l.strip() and not l.startswith("#")]

    print(f"[*] {len(hosts)} host × {len(paths)} 路径 = {len(hosts)*len(paths)} 探测", file=sys.stderr)
    all_hits = []
    with cf.ThreadPoolExecutor(max_workers=args.threads) as ex:
        futs = {ex.submit(probe_host_paths, h, paths, args.timeout): h for h in hosts}
        done = 0
        for f in cf.as_completed(futs):
            done += 1
            if done % 20 == 0:
                print(f"[*] 进度 {done}/{len(hosts)}", file=sys.stderr)
            all_hits += f.result()

    with open(args.outfile, "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["host", "url", "type", "status", "feature"])
        for r in all_hits:
            w.writerow(r)

    # 按类型统计
    from collections import Counter
    bytype = Counter(h[2] for h in all_hits)
    print(f"\n[+] 命中 {len(all_hits)} 个端点 → {args.outfile}", file=sys.stderr)
    for t, n in bytype.most_common():
        print(f"    {t}: {n}", file=sys.stderr)
    print("[i] 命中只记录不改打; Swagger/OpenAPI 的字段提取法见 06 §8; 验证归 src-dig", file=sys.stderr)


if __name__ == "__main__":
    main()
