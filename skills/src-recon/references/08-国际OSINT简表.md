# 08 国际 OSINT 简表 — 跨境/外企目标才开

> **何时打开：** 跨境目标、外企中国区（总部信息在国外）、需要全球基础设施/泄露库视角。
> **何时不打开：** 国内常规 SRC 线（这些源速度慢/覆盖不如国内线，开了浪费时间）。
> 来源：offensive-osint（491 行）+ offensive-osint-methodology（441 行）压缩（2026-09-07）。工具索引为主，按需再深入。

---

## §1 何时值得开国际线

| 场景 | 用什么 |
|------|--------|
| 外企中国区（YUM/KFC/Parkson 类） | LinkedIn 员工 + 总部域名 + SecurityTrails |
| 目标有海外资产（.my/.tw/.com 主域） | Shodan/Censys + 各国企业注册库 |
| 需要泄露库确认凭据有效性 | HIBP / Dehashed |
| 供应链是海外厂商 | OpenCorporates + SEC EDGAR |

## §2 工具速查表（一行一个，按类）

> CLI 类工具本机可用性认 `ENV.md`（holehe/Sherlock 等未装）；Web 类直接 playwright/browser 用。

### 邮箱/用户名/人员

| 工具 | 用途 |
|------|------|
| hunter.io | 域名 → 邮箱列表 + 格式推断 |
| holehe | 邮箱在哪些平台注册（CLI） |
| Sherlock / Maigret | 用户名跨平台查账号 |
| whatsmyname.app | 同上（Web） |
| Epieos | 邮箱 pivot 与元数据 |
| RocketReach / Apollo | 企业人员 enrichment |
| linkedin.com | 员工名/职位（`"@target.com" site:linkedin.com`） |

### 泄露库/凭据

| 工具 | 用途 |
|------|------|
| haveibeenpwned.com | 邮箱是否在已知泄露中 |
| Dehashed / IntelX | 泄露记录查询（含密码哈希） |
| Hudson Rock | infostealer 日查询（API） |
| ddosecrets.com | 泄露数据归档 |

**纪律：** 泄露凭据当**钥匙验证**（认 src-dig `rules/src-value-hunting.md` 认钥闸），不抄哈希/明文进资产表；只在授权范围内使用。

### 基础设施/域名

| 工具 | 用途 |
|------|------|
| **VirusTotal** | 域名/IP 被动解析历史 + 社区样本 + 域名关系图（免费 API 档够收集用；VT 持有 Comodo 时代外的全量被动 DNS） |
| SecurityTrails | 子域/历史 A 记录/IP 反查（**免费账号**即可查子域+历史解析——CDN 溯源候选直出，联动 03 §8 P0） |
| Censys | 证书精确过滤：`services.tls.certificates.leaf_data.subject.common_name:"example.com"`（证书反查，联动 03 §8 P2） |
| Shodan | `hostname:` `ssl.cert.subject.CN:` `org:` `http.favicon.hash:-247838494`（favicon 反查联动 06 §3） |
| viewdns.info / whoisxmlapi | 历史 WHOIS、反查同注册人 |
| rdap.org | RDAP 结构化 WHOIS（curl 即用） |
| bgp.he.net / ASN 工具 | ASN/网段归属（Team Cymru DNS TXT 等价见 03 §9） |
| crt.sh / certspotter | CT（国内被墙时见 03 换路） |

### 企业/注册

| 工具 | 用途 |
|------|------|
| opencorporates.com | 全球公司注册库聚合 |
| SEC EDGAR | 美股公司（10-K 里子公司清单🔥） |
| gsxt.gov.cn | 国内工商（对应 01） |
| OFAC / 制裁名单 | 合规核查（跨境必做） |

### 社媒/存档

| 工具 | 用途 |
|------|------|
| snscrape | Telegram 抓取（CLI）。⚠️ X/Reddit 线已失效（2023 年 API 政策关游客接口），X 用网页/playwright，Reddit 用 old.reddit 搜索 |
| web.archive.org / archive.today | 历史页面（CDX API 见 06 §7） |
| Google/Bing/DDG dork | 通用（`site:` `filetype:`，语法见 04） |

### GEOINT/取证（一般用不上，极跨境红队才碰）

| 工具 | 用途 |
|------|------|
| search4faces / picuki | 人脸/Instagram |
| SunCalc / 卫星图 | 影子分析定位时间地点 |
| ExifTool | 图片元数据（⚠️ Windows 默认无、本机未装——需要时官网装或 python PIL `Image._getexif()` 替代） |

## §3 Sock Puppet 纪律（要开社交面时）

- 独立浏览器 profile（Firefox Multi-Account Containers）；不登个人账号
- 虚拟号码（Burner/Silent Link）过验证；不带真实痕迹
- 最小链路：每个 case 一个 persona；记录工具版本与操作时间戳
- **来源纪律：** 社交工程类操作（联系员工/加群）在国内 SRC 授权范围外——**只做被动观察，不主动接触**，除非用户明确授权且 SRC 规则允许

## §4 与国内线的衔接

- 外企中国区：国际线拿**总部域名/组织/全球资产** → 国内线（01 爱企查查中国子公司 + 02 FOFA）收中国资产 → 合并去重（09）
- 海外域（.my/.tw）测绘命中是**真实分支**，别当误报删（infogather Mode17 坑点）
- 归属闸同样适用：海外参股/合资 → 冷池
