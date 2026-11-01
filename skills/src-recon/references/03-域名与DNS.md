# 03 域名与 DNS — 子域枚举 / DNS 深挖 / CDN 溯源

> **何时打开：** 有主域名要挖子域；要解析验证/分级标注；目标套 CDN 要找真实 IP；备案长尾要活筛。
> **何时不打开：** 只要测绘（去 02）；已进站挖洞（归 src-dig §4）。
> 来源：infogather Mode3/4/6/9/11 · src-dig 知识库/recon-methodology §2/§14 · attack-surface-recon · information-gathering §三（2026-09-07 蒸馏）。
> **活筛口径**（备案长尾先便宜筛再送 FOFA）认 `dig-scope` §1.2。

---

## §1 CT 证书透明（被动首选，零触达）

证书 SAN 字段 = 目标自己「投喂」的子域名清单。

```bash
# crt.sh JSON（最全；国内可能被墙，curl 加超时重试，失败换路）
curl -s -m 30 "https://crt.sh/?q=%25.<domain>&output=json" | \
  jq -r '.[].name_value' | tr '\n' '\n' | sort -u

# certspotter（备选）
curl -s -m 30 "https://api.certspotter.com/v1/issuances?domain=<domain>&include_subdomains=true&expand=dns_names" | jq -r '.[].dns_names[]' | sort -u

# HackerTarget hostsearch（免费无 key，被墙时的救命源；直接给「子域名,IP」映射）
curl -s -m 30 "https://api.hackertarget.com/hostsearch/?q=<domain>"

# Censys 证书搜索（精确字段过滤）
# https://search.censys.io/certificates?q=names%3A+<domain>
```

**一键换路：** `bash tools/crtsh.sh <domain>`（crt.sh → certspotter → HackerTarget 自动切换，见工具节）。

**组织反查（`https://crt.sh/?O=<组织名>&output=json`）：** 按证书 O 字段反查主体全部证书。**实测收录很弱**（现代证书 O 字段缺失率高 + 接口限流：10 个主体仅出 5 域且全为已知）——低优先，试一次无货即弃，别投入。

**测绘覆盖提示：** 大企业 CT 数据大头已被测绘 cert 反查覆盖（FOFA `q='cert.subject.cn="…"'` / Quake `cert:"域名"`）——先免费源，再烧配额。

**RapidDNS（rapiddns.info）免费聚合兜底：** `https://rapiddns.io/subdomain/<domain>?full=true`——CT+被动 DNS 双源聚合的子域+IP 映射，国内可达，CT 三源之外最快的免费兜底。

**amass 被动聚合（`~/bin/amass.exe` v5；⚠️ 选配慢源，实测常 0 产出）：**

```bash
amass enum -passive -d <domain> -o subs.txt
# v5 免费源多为 key-gated, iana.org 实测 5-8 分钟 0 FQDN(直连/挂代理同) — 只在前三源全空时才值得试
# 走 Clash 提升源可达性: export https_proxy=http://127.0.0.1:7890
```

> 想把 CT + certspotter + 被动 DNS 数据集 + 搜索引擎一次跑完: 走 **§11 OneForAll 一体化枚举**(20 源, 实测 20~60 秒)。

## §2 搜索引擎 site 语法

```
site:<domain> -www -mail
site:<domain> intitle:登录 OR intitle:后台 OR intitle:管理
site:<domain> intitle:test OR intitle:dev OR intitle:uat
site:<domain> inurl:.git OR inurl:phpinfo
```

## §3 DNS 记录全类型（每类都可能是入口）

> 本机无 `dig`——`dig +short <domain> <type>` 用 `nslookup -type=<type> <domain>` 等价；批量场景用 `~/go/bin/dnsx`；AXFR 无 nslookup 等价，用 `dnsx -d <domain> -axfr @ns1.<domain>` 或 dnswalk。

```bash
nslookup -type=ANY <domain>          # 或逐类型: A/MX/NS/TXT/CNAME/SOA/SRV/CAA
nslookup -type=SRV _autodiscover._tcp.<domain>   # 邮件自动发现
nslookup -type=SRV _ldap._tcp.<domain>           # 域控线索
nslookup -type=SRV _sip._tcp.<domain>            # VoIP
nslookup -type=SRV _vlmcs._tcp.<domain>          # KMS 激活服务器

# TXT 高风险点：SPF/DKIM 泄露邮件基础设施；verification token 可冒充验证
# CNAME 指向云服务 = 子域名接管候选（转 07 §4）
# AXFR 区域传送（老而有效）：dnsx -d <domain> -axfr @ns1.<domain>
# DNSSEC NSEC zone walking（开了 NSEC 的域可遍历全部记录名）: dnsrecon --walk / ldns-walk（未装则跳过, 低优先）
```

**多 DNS 对比识别 CDN / 地域策略（国内目标必做）：**

```bash
nslookup <domain> 114.114.114.114    # 国内 DNS
nslookup <domain> 8.8.8.8            # 海外 DNS
nslookup <domain> 1.1.1.1
# 差异即 CDN 地域策略；国内 CDN（987dns/dnspod/阿里云）常对海外 DNS 返 0.0.0.0
# → 真实 IP 藏在国内 DNS 结果中
```

## §4 DNS 字典爆破（无需 API key，31.7% 实测命中率）

用本技能 `tools/dns_brute.py`（119 条前缀字典爆破，无需 API key；subfinder 被动聚合 / dnsx 批量解析已装 `~/go/bin`；本机系统 DNS 真实解析可用，fake-ip 是旧机坑已不存在——dns_brute.py 内置 198.18.x 假 IP 过滤兜底）：

```bash
# 内置 119 条前缀字典爆破
python tools/dns_brute.py --domain <domain>

# 自定义前缀
python tools/dns_brute.py --domain <domain> --wordlist my_prefixes.txt

# 多域名批量
python tools/dns_brute.py --domains domains.txt

# 拼音缩写模式猜测（国内企业域名模式）
python tools/dns_brute.py --domain <domain> --patterns
```

**内置前缀字典覆盖：** sso/login/passport/auth/cas/uc · portal/mail/webmail · oa/workflow/bpm · erp/crm/scm/ehr/hr/pay · git/gitlab/jenkins/jira/confluence/wiki · api/open/developer/console/dashboard/admin/manage/backend · test/uat/dev/stage/demo · vpn/monitor/zabbix/grafana · nas/backup/storage/upload · k8s/docker/rancher/harbor/nexus/elk/kibana · im/meeting/video · m/app/h5/weixin/miniapp · data/bi/db/redis/kafka · 若依/金蝶/用友类（ncc/yonyou/u8/kingdee）。

**拼音缩写域名模式猜测（实战经验表）：**

| 模式 | 实战案例 | 预测公式 |
|------|---------|---------|
| 拼音缩写+fd(发电) | lyxfd.com, lxwfd.com | 龙羊峡(lyx)+fd |
| 缩写+sdz(水电站) | ljxsdz.com | 李家峡(ljx)+sdz |
| 公司缩写+英文 | hhdcsilicon.com | 黄河电(hhd)+silicon |
| 品牌+solar/pv/power | spicsolar.com | spic+solar |

**验证铁律：** 域名有 A 记录 ≠ 可达 ≠ 属于目标。DNS 爆破结果必须过三重确认（IP 段归属 / 备案 / 页面内容）才进资产表。

> 需要**大字典**(9.5 万/88 万行)或**置换枚举**(拿已收集子域重组 num/word 变体, 实测 17 种子→6 个新存活子域)时走 **§11 OneForAll**(`--mode brute` / `--mode perm --seed`)。

## §5 vhost 虚拟主机发现

> ⚠️ ffuf 未装（需要时 release 二进制装）。已装工具的等价写法：

```bash
# dnsx 批量测同 IP 的 Host 头差异（把候选子域列表灌进去）
~/go/bin/dnsx -l subs.txt -resp -silent | grep <目标IP>   # 同 IP 的子域 = vhost 候选

# katana 爬取发现的 Host 变体 / 或 playwright 单点验证
curl -s -o /dev/null -w '%{http_code} %{size_download}' https://<ip>/ -H "Host: <candidate>.<domain>"
# 不同 Host 同 IP 响应码/大小不同 = 独立 vhost（尤其单 IP 多租户/宝塔场景）
```

## §6 解析后验证与 5 级标注

DNS 解析成功 → `~/go/bin/httpx` 探活（`-title -status-code -tech-detect -cdn`）→ `tools/dedupe.py` 去噪分级（09 §2）→ 5 级标注：

| 级 | 含义 | 处理 |
|----|------|------|
| 🟢 公开 | httpx 200 有页面 | 直接入资产表 |
| 🟡 有解析未验证 HTTP | 有 A 记录 | 逐个探 |
| 🔴 内网/错误 | 解析但连接拒绝 | 可能仅内网，记过 |
| ⚫ IP 段独立 | IP 与其他域名不同段 | ⭐ 独立系统可能弱防护，优先 |
| 🟣 云部署 | IP 属阿里/腾讯云 | 可能子公司自运维，⚠️ 待归属确认 |

**重点优先标签：** 测试环境（uat/dev/minio-uat）· 独立 IP 段 · 云部署 · 对象存储（MinIO）· HR/财务 · SSO 统一认证。

## §7 IP 段聚类（判断系统重要性）

```bash
# tools/dns_brute.py 已内置聚类统计；手动版：
cat resolved.txt | awk -F, '{split($2,a,".");print a[1]"."a[2]"."a[3]".0/24"}' | sort | uniq -c | sort -rn
```

```
同一 /24 段内 5+ 域名 → 核心系统集群 → 重点
分布在多个不同 /24   → 各有独立管理员 → 逐个过
单域名独占 /24       → 独立系统 → 可能弱防护
```

## §8 CDN 溯源：找源站真实 IP（目标套 CDN 时）

**核心原则：按优先级打，P0 命中即验证，验证不过再升级；早停——P0/P1 有 2+ 独立来源交叉确认同一 IP 就进验证。**（详见 src-dig `知识库/recon-methodology.md` §14，此处为索引+速查。）

### P0 快速命中（成本极低，全执行）

```bash
nslookup -type=TXT <root>           # SPF: v=spf1 ip4:x.x.x.x → 极可能源站
nslookup -type=MX <root>            # 邮件服务器若非第三方邮件商 = 可能源站
nslookup -type=AAAA <target>        # IPv6 常只给 IPv4 接 CDN，直指源站
# 历史 DNS：ipchaxun.com/<domain>/ · site.ip138.com/<domain>/ → 找最早 A 记录/CDN 接入拐点
#   更多历史解析聚合源(SecurityTrails/微步/ViewDNS): src-dig 知识库/recon-methodology.md 下半「DNS 历史聚合源速查」+ 08 §2
nslookup <target>                   # CNAME 链定 CDN 厂商：
#   cloudflare=CF · eo.dnse5.com=腾讯EdgeOne · kunlun/tbcache=阿里 ·
#   cloudfront=AWS · akamaiedge=Akamai · lxdns=蓝汛 · jiashule=加速乐
# 泄露头：X-Real-IP / X-Origin-IP / X-Backend-Server / Via
```

### P1 证书与子域名

- 证书：`echo | openssl s_client -connect <t>:443 -servername <t> | openssl x509 -noout -serial -subject`
- CT 找非 CDN 子域（高价值前缀：mail/ftp/vpn/origin/direct/backend/api/admin/dev/old/bak）
- 全部子域解析到同一 CDN CNAME = 通配符记录 → 此法失效换 P2

### P2 空间引擎反查（对任何 CDN 厂商有效）

FOFA `q='cert="<domain>"'` / Shodan `ssl.cert.subject.CN:"<domain>"`（全网持有同证书的 IP，排 CDN ASN 后=源站候选）· favicon hash（`icon_hash=`，算料见 06 §3）· body/title 唯一特征反查。

### P3 报错回源与行为差异

```bash
curl -sI -k https://<candidate_ip>                                          # 直连无 Host
curl -sI -k --resolve <target>:443:<candidate_ip> https://<target>          # 带 Host
# 源站：200/301 + Server: nginx/apache/IIS；CDN：403/421/418/502 + CDN 标识
```

非标准端口（21/22/25/8888/9090/3306）不经过 CDN，SSH/FTP 存在 = 真实服务器。**邮件触发法**：收一封目标发来的邮件，解析 `Received:` 头 IP。Cloudflare 特有：`direct.<domain>` 常直连。

### 验证（每个候选 IP 必过）

**SSL 证书序列号精确比对（决定性）**：基准=CDN 节点证书 Serial；候选=带 SNI 证书 Serial+默认证书。CDN Serial==候选 SNI Serial 且默认证书是另一域名且 Server 头是真实 Web 服务器 → 确认源站。证据分级：S 决定性（序列号+默认证书+Server 头）/ A 强佐证 / B 佐证 / C 弱线索——报告时按最低档写，猜测不当实锤。

**查到的源站 IP 只做进一步测试的靶子，不对外散布**；直连源站=绕 CDN WAF 是测试手段，不是漏洞本身。

## §9 ASN / IP 段枚举（大企业网段资产线，2026-09-07 实测）

> 子域之外的独立资产线：有自有 ASN 的大企业（京东/腾讯/移动…）直接「企业名 → ASN → 全部网段」，不依赖子域反推。
> **归属闸照过**（`dig-scope` §3.2.0）：ASN 归属 ≠ 100% 控股，参股/合资网段冷池。

```
链路: 已知域名 → DoH A 记录 → ASN → 组织其他 ASN → 全部网段 → 去重 → 端口扫描(06 §2)

① 域名 → ASN（ip-api，dedupe.py 同款）:
   curl -s "http://ip-api.com/batch?fields=query,as,org" -d '[{"query":"<ip>"}]'

② 组织其他 ASN（bgp.he.net 搜索，带 UA；playwright 更稳）:
   curl -s -A "Mozilla/5.0" "https://bgp.he.net/search?search%5Bsearch%5D=<公司英文名/拼音>&commit=Search" \
     | grep -oE 'AS[0-9]+' | sort -u
   实测: jingdong → AS137787 / AS137753 / AS134649…

③ ASN → 全部网段（RADB，实测 AS54994 → 2203 条；whois 未装，用 Team Cymru DNS TXT 等价）:
   nslookup -type=TXT AS54994.asn.cymru.com    # "AS54994 | 2203:xx::/32 | CN | ..." 取前缀
   # 批量前缀仍用 RADB 网页/或 curl -s "https://rest.db.ripe.net/..."（走代理）
   # whois 版(装了 whois 后): whois -h whois.radb.net -- '-T route -i origin AS54994' | grep '^route:'

④ 网段去重合并（python 一行，自动吸收重叠子网）:
   ... | python -c "import ipaddress,sys; \
     nets=[ipaddress.ip_network(l.split(':')[1].strip()) for l in sys.stdin if ':' in l]; \
     print('\n'.join(map(str, ipaddress.collapse_addresses(nets))))"

⑤ 优先段挑打（先 /16 后 /24、先网段中心后边缘）→ naabu/nmap 两段式（06 §2）
   ★ 段落地闭环：段列表先喂测绘全查（FOFA `ip="段"` / `asn="<ASN>"`，02 §1）拿无域名系统，再挑打（§10）
```

**坑（实测）：**
- `amass intel -org` 不出结果（勿用）；amass 实为 `~/bin/amass.exe`（被动聚合走 02/03 §1 的 Clash 代理口径）
- RADB 只有**注册过的路由**——企业实际网段可能更多，测绘 `ip=` 补（`dig-scope` §2.1 配额闸）
- 网段量大（2203 条）先聚合再挑，不要全段平铺扫
- **云/CDN 业务型企业的 ASN = 客户池**：实测字节三个 ASN（AS24429=CDN 649K 条 / AS396986=海外 CDN 276K / AS137718=火山云客户 218K）**全为第三方客户流量**（geely/wacom/deloitte/wps…），自有资产寥寥 → `asn=` 结果**必须按品牌域名过滤**（`asn="x" && (domain="品牌域" || cert="品牌")`），全段平铺=扫客户白干

## §10 C段 / 反向 IP 闭环（拿到任一有价值 IP → 看整段还有什么）

> 场景：探测到任一肥 IP（暴露系统/源站候选/边缘段）想看「同段还有谁」；或 §9 拿到段列表后的落地闭环。
> 分工：本篇是「段 → 资产发现」的串联；扫描机制（naabu/nmap/httpx）见 06 §2，同 IP 多站见 06 §5，此处不复制。

```
① 段内存活扫描(naabu 支持 CIDR):
   ~/go/bin/naabu -host x.x.x.0/24 -p 80,443,8080,8081,8443,8888,3000,5000,8000,9000,9090,9200 -silent > seg_ports.txt
② 每 IP HTTP 探活 + 指纹:  ~/go/bin/httpx -l seg_ports.txt -silent -title -status-code -tech-detect -cdn -o seg_live.txt
③ vhost / 同 IP 多站:      Host 头差分 + 子域回灌(06 §5)
④ 反向解析 / 反查域名(免费三源, 逐 IP):
   ~/go/bin/dnsx -l iplist.txt -ptr -resp -silent                     # PTR 反向 DNS
   curl "https://api.hackertarget.com/reverseiplookup/?q=x.x.x.x"      # 免费无 key 域名反查
   https://rapiddns.io/sameip/x.x.x.x?full=true                        # CT+被动DNS 同 IP 聚合
   (半免费交叉: SecurityTrails/微步/VT, 08 §2)
⑤ 测绘段查询(抓无域名/无 PTR 系统 + 非标端口):
   FOFA q='ip="x.x.x.0/24"' · Hunter ip="x.x.x.0/24" · Quake ip:"x.x.x.0/24"(02 §2 策略 C)
   组织视角一次出: FOFA q='asn="137718"' —— §9 归属表 → 该 ASN 全段资产
⑥ 归属过闸(逐 IP): Team Cymru ASN + 备案 + /24 聚类(dedupe.py)——混入的他司共段剔除后再入表
```

**坑（实测）：**
- **多租户 IDC 段先嗅探再决定**：先打 10~20 个 IP 定性（页面是否同一主体），同段异主直接止损——字节 115.190.x（AS137718）实扫判定多租户 IDC，放弃全段
- 反查/PTR 结果**必须过归属闸**：共享主机/托管段会混入他司域名（云 WAF 段尤其脏）
- 边缘/CDN 段（155.102/183.60 类）多为节点机收益低，优先**业务 IP 所在 /24**

## §11 OneForAll 一体化枚举(被动 20 源 + massdns 大字典 + 置换, 2026-09 接入)

> 场景: 要把「多源被动 + 大字典爆破 + 置换」一次跑完时用(比 collect.py 默认三源更全, 字典比 dns_brute.py 大三个数量级); 或拿到一批子域后做**置换枚举**找同族命名。
> 位置: `D:\OneForAll-0.4.5`(2026-09 实用化改造版, 自带 Python 3.13 venv; 改造清单见其 `PATCHES.md`); 包装器 = 本技能 `tools/o4a_enum.py`(认环境变量 `O4A_HOME`)。

```bash
python tools/o4a_enum.py --domain example.com                  # 被动档(默认): 20 源 + massdns 解析, 实测 20~60 秒
python tools/o4a_enum.py --domain example.com --mode brute     # + 9.5 万行字典爆破(实测约 +35 秒)
python tools/o4a_enum.py --domain example.com --mode brute --dict medium   # 88 万行大字典(约 +5 分钟)
python tools/o4a_enum.py --domain example.com --mode perm --seed raw_subs.txt  # 置换枚举: 拿已有子域当种子重组
python tools/o4a_enum.py --domain example.com --mode full      # 全开(HTTP 探活+指纹+finder+altdns+enrich, 慢; 一般不需要)
# 接进主管道(o4a 不在默认源里, 按需加):
python tools/collect.py --domain example.com --sources crtsh,subfinder,ht,o4a
```

**输出**(out-dir 下): `o4a.txt` 纯主机名列表(可直接喂 `dedupe.py`)/ `o4a_sources.md` 各源产量(哪个源还有肉 = 下轮剪枝依据)/ `o4a_raw.csv` 22 列原始结果(subdomain·ip·cname·resolve·source)。

**20 个源(逐个实测过可达)**: crt.sh · certspotter · myssl | rapiddns · urlscan(唯一需要走 Clash 的) · hackertarget · ip138 | ns/mx/txt/spf/soa | cert/robots/sitemap/cdx/nsec/axfr | 360搜索 · bing

**净增量实测**(iyincaishijiao.com): 本技能默认三源(crtsh+subfinder+ht)= 4 个子域; OneForAll 20 源 = 15 个(约 3.7×) —— 多出来的主要来自 IP138 / RapidDNS / certspotter / 360搜索。

**坑(2026-09 实测)**:
- **代理只能在 `config/setting.py` 里配** —— `common/module.py` 里 `session.trust_env=False`, 环境变量代理一律无效; 而且 **crt.sh 走代理反而 502(直连正常)**, 实测只有 urlscan.io 需要代理
- **源剪枝是必须的**: 该工具 2023 年停更, 现值 25 个源要 API key、约 18 个源已停服(Netcraft/SiteDossier/Sublist3r/Riddler/Robtex/DNSDumpster/Anubis/Chinaz-Alexa/ipv4info/passivedns/spyse/RiskIQ/Censys-v1/Bing API…)、5 个境外搜索引擎被墙 —— 默认全跑 = 白等超时(白名单已固化在 `config/setting.py`)
- `brute_concurrent_num` 传给 massdns 的其实是 `--hashmap-size`, **不是并发数**; 真瓶颈是 `--socket-count`(固定 1)
- 输出 CSV 编码已修成 `utf-8-sig`(原版在 Windows 落 GBK, 中文列全乱码且 BOM 被丢)
- OneForAll 自带泛解析检测, 但结果仍要过 `dedupe.py` + 归属闸(**置换/爆破产物尤其容易全是泛解析**)
- 置换枚举的收益实测: 17 个种子 → 36 个候选 → **6 个真实存在的新子域**(`v3-ep-pc` / `v9-ep-teacher` / `lf9-ep-sign` 这种编号+角色同族命名, 被动源基本搜不到)
