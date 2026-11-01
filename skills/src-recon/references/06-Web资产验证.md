# 06 Web 资产验证 — 探活 / 端口 / 指纹 / CDN 差分

> **何时打开：** 收集到域名/子域清单后验证分级；进站前最后一轮资产定性（谁肥谁瘦）；遇到 403/502 疑似 CDN 过滤要差分。
> **何时不打开：** 已进站挖洞（归 src-dig §4）；纯被动阶段（03 §6 之前）。
> 来源：attack-surface-recon · recon-and-methodology §3/4/12 · information-gathering §6-7 · opensrc-recon（2026-09-07 蒸馏）。
> **纪律：** 主动请求全部带超时；探活 ≠ 挖洞——本篇产物是分级资产表，不是漏洞。

---

## §1 httpx 探活+指纹（本机可用，主力）

> ⚠️ 注意：PATH 里的 `httpx` 是 Python HTTP 库的同名 CLI（参数不兼容）；资产探活用 `~/go/bin/httpx`（ProjectDiscovery 版，已装）。写全路径最稳。

```bash
~/go/bin/httpx -l subs.txt -silent -title -status-code -tech-detect -cdn -ip -o live.txt -timeout 30
# 看：标题（业务定性）/ 状态码 / 技术栈 / CDN 标记 / 解析 IP

# 批量 DNS 判 CDN(dig 的本机等价; CNAME 链/A 记录一次出):
~/go/bin/dnsx -l subs.txt -a -resp -cname -silent -o dnsx_out.txt
# 同一 CNAME 尾巴(如 xxx.qianxun.com)成簇 = CDN 接入点; A 记录直给真实 IP 候选
```

**探活完直接接去噪管道（09 §2）：** `python tools/dedupe.py --in subs.txt --out clean.txt --json report.json`——空壳跳转/默认页壳自动剔除、CDN 域名/IP 自动标注（DoH 真实解析，绕系统 DNS 污染/统一出口）、404/5xx 进存疑清单、httpx `-tech-detect` 指纹自动落 report.json。

**泛解析族判据(2026-09-13 实测)：** 同一父域下多个**语义无关**子域返回**完全一致的响应**（状态码+正文+头，例 `*.iam.bytedance.com` 的 apps-ext/autodiscover/prd/stat/ipmi 全返同一 Tengine 404；`*.arcosite.bytedance.com` 全返 302+HSTS）= 泛解析域，**整族剔除**再入表。旗舰根（bytedance/volcengine 级）的 crt.sh/subfinder/FOFA 结果尤其容易灌进几十条假子域（实测四根差集 293 条里 20 条是泛解析）。

**401/403/登录墙/管理台挑战页 = 存活，不当死站丢**（`dig-scope` §1.0.1 口径）。停放页/无业务响应 = 非存活。

**渲染级验证（SPA 站 httpx 抓不到真实业务面）：** 标题空白/只有壳 JS 的站，用 playwright MCP 逐个打开（navigate → snapshot）看渲染后菜单/登录方式——SPA 站点列表大时抽 10-20 个代表定性即可。批量流量留存：本机 Yakit/Charles 挂代理批量过一遍资产，留流量库给 src-dig 进站复核（App 线已配 Yakit MITM 8083）。

## §2 端口扫描策略（两段式）

```bash
# naabu 快扫（大段 IP; 本机已装 ~/go/bin/naabu, masscan 未装）
~/go/bin/naabu -host <ip或段> -p 80,443,8080,8443,8888,3000,5000,9090,9200 -silent

# nmap 精扫（重点主机，-sCV 服务版本; 本机 7.80 装在 Program Files (x86)\Nmap, 新 shell 进 PATH）
nmap -sCV -p 80,443,8080,8443,8888,3000,9090 <ip> -oN nmap_out.txt
```

**高危端口清单（命中优先记录）：**

| 端口 | 服务 | 备注 |
|------|------|------|
| 22/21 | SSH/FTP | 真实服务器（CDN 节点不开）|
| 6379/27017 | Redis/MongoDB | 未授权直接高危 |
| 3306/5432 | MySQL/PG | 公网暴露即问题 |
| 2375/2376 | Docker API | RCE 级 |
| 8080-9090 | 管理/Java 中间件 | Jenkins/JBoss/WebLogic |
| 9200 | ES | 数据泄露 |
| 11211 | Memcached | 未授权 |
| 2049/111 | NFS/rpcbind | |
| 161 | SNMP (UDP) | 信息泄露 |
| 50000 | SAP | |

**CDN 节点多端口：** 扫 CDN IP 的 3000-9200（8000/8080/9090 常代理同一后端但规则不同；9200 可能独立 nginx）。

## §3 Web 指纹

**favicon / icon_hash 归属线（06↔02 最高价值联动）：**

```bash
# ① 算目标 favicon 的 mmh3 hash(python: mmh3.hash(favicon bytes); 或在线工具)
# ② FOFA 反查同图标资产(扩面 + 归属判定两用):
intel_search(expr='icon_hash="-247838494"', limit=100)          # icon_hash 直查
intel_search(expr='icon_hash="-247838494" && country="CN"')     # 组合表达式
# ③ 非显性域名(不在公司树/备案表)但同 favicon = 同主体强判据 → 过股权闸后入表
# ④ 反向: 疑似仿冒站(见 02 §6)用同 hash 一锅端
```

同 favicon 是仅次于备案/股权的资产归属判据；hash 不同但页面结构雷同（同模板建站）不算归属。

```bash
whatweb https://<target>                                        # ⚠️ 未装(winget/官网可补)
~/go/bin/httpx -u https://<target> -tech-detect
~/go/bin/webanalyze -host https://<target> -silent              # Go 版 wappalyzer 技术栈识别(已装)
curl -sI https://<target> | grep -iE 'server|x-powered-by|x-generator|cf-ray|via|x-px'
# Cookie 指纹：PHPSESSID=PHP · JSESSIONID=Java · _rails_session=Rails
# Meta generator / HTML 注释 / JS 框架文件路径
# webanalyze 首跑会自动下指纹库(technologies.json)
```

**Java 中间件指纹矩阵（命中 → 转组件情报）：**

| 中间件 | 检测路径 | 特征 |
|--------|---------|------|
| Tomcat | /manager/html | tomcat:tomcat 默认 |
| JBoss/WildFly | /jmx-console/ | WAR 部署 |
| WebLogic | /console/ /wls-wsat/ | T3 7001 |
| Spring Actuator | /actuator/env, /actuator/heapdump | heapdump 含密钥 |
| Jenkins | /script /manage | Groovy 控制台 |
| GlassFish | /common/ | 4848 空密码 |
| Resin | /resin-admin/ | |

**国产组件指纹+默认口令**（致远/通达/泛微/用友/金蝶/Nacos/Druid…）：查 `~/.claude/skills/src-dig/知识库/cn-fingerprint-creds.md`（表已备好，不复制到这里）。

**识别出框架版本 → 组件漏洞情报链（收集阶段只记录「指纹+候选 CVE」，验证利用归 src-dig）：**

1. 拿指纹精确版本（`/actuator/info`、`X-Powered-By`、错误页、登录页 footer）——只有版本到位 CVE 才有意义
2. FOFA 用组件名扩同类面：`q='app="APACHE-Tomcat"'`、`q='title="Apache Tomcat" && country="CN"'`（同类目标=同风险面）
3. CVE 查询：搜索引擎 `组件名 版本 CVE`、cve.mitre.org / NVD / 中文社区（先知/FreeBuf）
4. PoC 找 GitHub：`组件名 poc` / `组件名 cve-20xx`（搜 In: exploits repo）
5. 全部命中只落资产表「指纹+候选 CVE」列，不打——验证利用归 src-dig 进站流程

## §4 CDN 差分陷阱（403/502 ≠ 不存在）

**CDN 响应过滤（PWS/GCCDN 等）：** CDN 对特定 URL 模式（admin 路径/actuator）返 502 而非转发后端：

```
502 = 后端响应被 CDN 过滤（可能存在！）
404 = 路径真不存在
403 = CDN 路由拒绝
→ 用 502/404/403 差异枚举存活端点
路径穿越确认：/api/v1/v/..;/..;/actuator/env → 502(被过滤) vs /..;/..;/xxxx → 404(Tomcat 原生)
```

**Nginx catch-all 陷阱：** PHP CMS rewrite 全路由 → 所有路径返 200 同大小（不是文件存在！）。识别：3 个随机路径对比 size 一致 = catch-all。真实存活路径用 `.php` 后缀测（真 404 = 不存在）。

**Nginx 404 差分指纹：** 真实 nginx 404（~146B）= 文件存在但 deny；catch-all（大页面）= 路由未匹配。批量探测 CMS 标志路径（/caches/configs/ /phpcms/modules/ /statics/）比响应大小区分 CMS。

## §5 宝塔面板 / 同 IP 多站

**宝塔识别：** 32 位 hex Cookie + 302 跳 /login + nginx 反代 +「入口校验失败」页 = BT Panel 随机安全入口。Windows 版：IIS+nginx 共存、8888 面板、888 phpMyAdmin、/index.html 可能泄露站点配置。

**同 IP 多站横向：** 反查 IP（SecurityTrails/微步/VirusTotal）找同服务器其他域名 → 弱站（独立 nginx 返真 404 可枚举）突破 → 宝塔统一管理=横向所有站。⚠️ 同服务器≠同授权——只记录，归属过闸。

## §6 目录与敏感路径探测（收集级，不是挖洞）

```bash
# ⚠️ ffuf 本机未装 — 字典爆破类在 src-dig 进站阶段再补装; 收集级目录发现用已装的:
~/go/bin/katana -u https://<target>/ -d 2 -jc -kf all -silent            # 爬虫式链接/JS端点发现
python tools/api_hunt.py --in <host列表> --out api_hits.csv             # 本技能 90 条精选路径(已含 .git/.env/druid/actuator)
curl -s -o /dev/null -w '%{http_code} %{size_download}' https://<target>/<path>   # 单点差分
# 需要大字典爆破时(ffuf 官方字典 github.com/danielmiessler/SecLists 下载后配 ffuf):
#   ffuf -u https://<target>/FUZZ -w SecLists/Discovery/Web-Content/common.txt -mc 200,301,302,403
# 重点命中（记录进资产表备注，进站归 src-dig）：
# /.git/HEAD /.svn/ /.env /backup.zip /swagger-ui.html /api-docs /graphql /phpinfo.php
# /actuator/ /druid/ /console/ /nacos/
```

nuclei 收窄三连（引擎已装 `~/go/bin/nuclei` v3.11；**模板未预装**——首跑先 `nuclei -update-templates`，走 Clash 代理 `export https_proxy=http://127.0.0.1:7890`，默认装到 `~/nuclei-templates`；串到 dedupe 出的 clean.txt 上），**不当全量扫**（`dig-scope` §4 nuclei 口径）：

```bash
nuclei -l clean.txt -severity medium,high,critical -t exposures/ -t misconfiguration/ -t takeovers/ -o nuclei_out.txt
# 收集阶段只记录命中(资产表备注 🔥), 验证利用归 src-dig
```

## §7 JS/端点预筛（给 src-dig 的进站口粮）

收集阶段只做**浅抽**确认面肥瘦（深挖归 src-dig `知识库/js-reverse-guide.md` + `dig-scope` §4.1）：

```bash
# 历史与当前 URL 聚合（gau / waybackurls 已装 ~/go/bin，走 web API 不依赖本机 DNS）
echo <domain> | gau | head -200
waybackurls <domain> | head -200
# ⚠️ 通道实测(2026-09-13): gau 直连与挂 Clash 均 0 产出(其 dialer 不吃 env proxy);
#    web.archive 直连超时、**挂 Clash 可用**。可靠路线 = CDX 挂代理 + data:URI 过滤
#    (不过滤时 3000 条窗口全被 data:URI 占满, 干净路径 0 条):
export https_proxy=http://127.0.0.1:7890
curl -s -m 90 "https://web.archive.org/cdx/search/cdx?url=<domain>&matchType=domain&output=text&fl=original&collapse=urlkey&filter=!original:.*(data%3A|base64).*&limit=5000" | head -200
# katana 爬取(浅爬 d=3, JS/端点发现)
~/go/bin/katana -u https://<target> -d 3 -silent -jc -o crawl.txt
# 从 JS 提 API 路径（确认有无业务接口面）
for u in $(cat js_urls.txt); do curl -s -m 30 "$u" | grep -oE '"/(api|rest|graphql)[^"]+"'; done | sort -u
```

**面肥瘦判据：** JS chunk 多 / 有 /api/ 前缀 / 有 swagger / 有 admin 路由 → 肥面（src-dig 深挖）；纯静态壳/无 script → 瘦面（一眼记过）。

## §8 API 资产批量发现（clean.txt 的标准后继动作）

**一键批量（对 dedupe 出的可用资产全量跑）：**

```bash
python tools/api_hunt.py --in clean.txt --out api_hits.csv
# 内置 90 条精选路径: swagger/openapi 全家族 / graphql / actuator 家族 / druid /
#   中间件 console / BFF 专属(/bff/ /__api/ /_next/data) / 版本漂移(/api/v1~v3 //legacy/)
# 特征判定命中类型(Swagger UI/OpenAPI JSON/GraphQL/Actuator/Druid/API JSON/信息文件)
# 误报已防: WAF 444 拦截页/统一错误壳不记; 4xx 一律不记; 先打随机路径做壳基线, catch-all(SPA 回 index.html)整站不记
```

> ⚠️ **命中 ≠ 有洞, 必须逐条 curl 验内容(2026-09-13 实测教训):** 123 host 扫描出 14 Swagger UI / 4 GraphQL / 2 SCG Routes,**逐条验完全是误报**——①404 页正文里出现 swagger/graphql 字样被旧判定器当命中 ②SPA catch-all(任意路径回 index.html)让 /api-docs /api/v1 等全返 200 JSON ③头条/抖音开放平台网关统一错误体 `{"data":{...,"error_code":22},"message":"error"}` 的 `"data"` 字段骗过判定器。判定器已按这三条修正(见上),**但报告前仍要逐条看响应体**:真命中的样子 = springfox UI 静态资源(`webjars/springfox-swagger-ui/`)/ 真 OpenAPI JSON(`"paths"` 结构)/ `_links` 的 actuator。

**从 OpenAPI/Swagger 文档提取什么（api-recon-and-docs 精华，拿到文档后）：**

| 提取项 | 价值 |
|--------|------|
| optional / 未文档化字段 | 未在前端用的参数=更少校验的入口 |
| admin-only 请求示例 | 管理接口+参数格式直接拿到 |
| `additionalProperties: true` | 模式宽松（注入/类型杂耍候选） |
| 过滤/排序/ID/角色/租户参数名 | 直接喂 src-dig §4.2 对象图/换 id |
| deprecated 但仍活的端点 | 老代码=老洞 |
| 版本漂移 `/api/v1/` vs `/api/v2/` vs `/api/mobile/v1/` | 移动端/旧版常少防护 |

**历史接口聚合（gau 里的 /api/ 前缀）：**

```bash
gau <domain> 2>/dev/null | grep -oE 'https?://[^/]+/api/[a-z0-9/_-]+' | sort -u | head -100
```

**隐藏参数发现（arjun 未装，手工级）：** 对已确认的 API 端点按常见参数名试差（`page/size/userId/tenantId/roleId/debug/test/admin`），有行为差分=存在隐藏参数 → 喂 src-dig。

## §9 输出

验证完按 09 分级落盘：每个 host 一行（host/IP/端口/标题/技术栈/CDN/分级/备注）。发现暴露组件（actuator/swagger/.git）→ 资产表备注列标记 🔥，不自行验证利用。
