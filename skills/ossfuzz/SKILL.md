---
name: ossfuzz
description: 云对象存储桶（OSS/COS/OBS/S3/MinIO/TOS/BOS/七牛）的批量发现与权限测试：从 JS/APK/小程序提取桶与凭证 → 厂商 request-id 指纹 → 桶名种子+积木交叉 fuzz → prefix 参数绕 ListBucket → 权限面矩阵（列表/PUT/ACL/分块/PostObject/CORS/镜像回源）→ 桶接管识别 → STS/预签名升级。内置防覆盖与低并发红线。目标出现存储桶、storage/CDN 域名、领签接口，或需要批量刷桶时使用。
origin: quiver
upstream-license: AGPL-3.0-only
---

> 来源：quiver（AGPL-3.0-only），原文件 `skills/ossfuzz/SKILL.md`；移植时改写了工具名与外部路径。

# ossfuzz — 云对象存储桶批量测试

配套文件（先读本页；深度用时再开）：

| 文件 | 内容 |
|---|---|
| `（外部知识库未随包提供）-mode/kb/知识库/oss-bucketscan-test.md` | 内网 KB 模块（中文速查 + 短表口径 + 假点清单）——桶级全流程主线，与本技能互补 |
| `references/ossfuzz-full.md` | 全量工艺原文（八阶段全部脚本、分厂商 payload 矩阵、分级映射、清理自检清单） |
| `（外部知识库未随包提供）-mode/kb/report-format.md` | 报告格式与写不写门槛 |
| `（外部知识库未随包提供）-mode/kb/知识库/file-upload-test.md` | 上传点/凭证滥用/覆盖对象链（分工：本条管「桶本身」） |

## 触发场景

- 资产出现对象存储域名：`*.aliyuncs.com` / `*.myqcloud.com` / `*.myhuaweicloud.com` / `*.jdcloud-oss.com` / `*.bcebos.com` / `*.volces.com` / `*.amazonaws.com(.cn)` / `IP:9000|9001|7480`
- JS/APK/小程序提取到：`bucket` 字段、`cloudEnvId`、12/13 位 APPID、领签接口（`getUploadToken` / `getStsToken` / `getSignedUrl` / `presign` / `ossAccessKeyId` / `S3Signer`）
- 见到 403 AccessDenied 的桶、CDN 回源错误 XML（`<HostId>`）、`?prefix=` 可列、桶名可爆的信号
- 需要把一批业务域/子域批量筛成「谁的桶敞开」

## 红线（违反任一条即失败）

1. **防覆盖**：PUT 前必 GET/HEAD 探存（404/NoSuchKey 才 PUT）；文件名唯一 `bucketscan-{sid}-{rand6}.txt`；建清理台账记所有自建 URL，收尾逐个 DELETE + 复核（CDN 用 `?t=` 破缓存）。
2. **低并发**：全程串行；每请求 sleep 0.3~1s；单目标域单会话 ≤500 请求；禁 `xargs -P` / `parallel` / `Promise.all` / 线程池。
3. **敏感域只读**：`*.gov.cn` / `*.edu.cn` / `pay.` / `bank.` / `auth.` / `sso.` / `passport.` 禁止任何写测试。
4. **写权限探测只用无副作用法**：ACL 写=不存在对象+合法值 或 无效值法；分块=假 `uploadId=1`；禁真实 `CompleteMultipartUpload` / `PutBucketAcl` / `PutBucketPolicy` / `PutBucketPolicy`。
5. **不下载敏感内容**：列表里出现 `.env` / `.sql` / `dump-` / `.pem` / `id_rsa` / 证件名 → 标 Critical 停测，只留 sample-key。
6. **接管只识别不实操**：禁注册同名桶、禁改 DNS、禁给「不存在桶」上传任何文件（会被记为新建桶=接管）。
7. **垃圾洞过滤**：CORS 只探测记录不单独成洞；公开静态/CDN 本职内容不写；必须过 `garbage-gate.md` 六问。

## 执行流程（浓缩）

### 一、资产提取（纯静态，零请求）
- 全量提取 host/URL **禁关键词过滤**；`IP:port` 也要；协议相对 `//host` 生成 http+https 两份候选。
- bucket 字面量：`{name}.oss-*.aliyuncs.com` / `{name}.cos.*.myqcloud.com` / `{name}.obs.*.myhuaweicloud.com` / `{name}.s3[.-]*.amazonaws.com(.cn)` / `{name}.s3.*.jdcloud-oss.com` / `{name}.*.bcebos.com` / `{name}.tos-*.volces.com`。
- 硬编码凭证扫描：`LTAI…` / `AKID…` / `AKIA…` / `ASIA…` / `JDC_…` + SK 上下文（`secretKey`/`accessKeySecret`）+ PostPolicy Base64（解码含 `conditions`/`expiration`）+ 预签名 URL 字面量（`OSSAccessKeyId`/`q-sign-algorithm`/`X-Amz-Signature`）。
- APPID：代码里 12/13 位数字（腾讯 COS 桶名 `name-appid` 必需）；`cloudEnvId` 可直接拼 `{envId}.cos.{region}.myqcloud.com`。
- 命中主账号 AK/SK → 只做一次 `?list-type=2` 验归属即停手报轮换，禁任何写。

### 二、逐 host 识别
必须发请求看 `Server` + `x-*-request-id` 头才算认定（对照 KB §2 表）；`IP:port` **先 HTTP**（自建多无 TLS）；域名先 HTTPS。CDN 兜底页 200 但无 request-id → 不算。

### 三、桶发现（种子优先 · 积木交叉 · 降噪兜底）
1. **种子**：代码里的 bucket 字段 / bizkey / 子域前缀 / 已泄露桶名 / APPID。
2. **泄露反查**：CDN 根 `<HostId>`；`GET /?uploads&max-uploads=1` 的 `<Bucket>`。
3. **积木交叉**：已知桶名拆原子 × 跨厂商 × 跨 region × 后缀（-prod/-dev/-test/-uat）× endpoint 形态；COS 必须拼 APPID，无 APPID 跳过腾讯候选。
4. tier 分层（已知桶名 > 业务种子 > 种子×全 region > 通用字典），500 请求上限优先喂高 tier；串行 HEAD，403/200 + request-id 才算命中。
5. 子域爆破：static/img/file/data/upload/cdn/assets/oss/cos/… 1~2 req/s。

### 四、权限面（只读优先）
顺序：列表（根/一级/前缀三级）→ **prefix 绕**（见下，最值钱）→ PUT（唯一 key）→ DELETE（仅自建）→ ACL 读/写（分厂商）→ 分块（假 uploadId）→ `?versions` / inventory / logging → CORS → PostObject → 镜像回源（随机 key 一次 GET）→ 对象参数（`response-content-type` / `x-oss-process`）。

分厂商 ACL 写探测：

| 厂商 | 校验顺序 | 方法 | 有权限信号 |
|---|---|---|---|
| 腾讯 COS | 先鉴权→后校值 | `x-cos-acl: bucket1-owner-full-control`（无效值） | 400 InvalidArgument + request-id |
| 阿里 OSS / 华为 OBS | 先校值→后鉴权 | 不存在对象 + `private`（合法值） | 404 NoSuchKey + request-id |
| AWS / MinIO / 京东 | 先鉴权→后校值 | `x-amz-acl: probe-invalid` | 400 + request-id |
| 火山 TOS | 先鉴权→后校值 | `x-tos-acl: probe-invalid` | 400 + request-id |
| 百度 BOS | JSON body | 空 body `PUT ?acl` | 400 MalformedJson + request-id |

### 五、桶接管（只读识别）
四信号：①桶已删但 CNAME 未回收（厂商"桶不存在"错误码 + request-id 双条件）；②CNAME 指向厂商桶域但直连返回桶不存在；③CDN 回源 502 + `<HostId>` 泄露已删桶名；④子域爆破 `*-old/-bak/-test` 返回 NoSuchBucket。
错误码：`NoSuchBucket`（阿里/腾讯/S3/MinIO/R2）/ `NoSuchCosBucket`（腾讯）/ `BucketNotExist`（华为）/ 七牛 `612 no such bucket` / 又拍 `40400001` / BOS JSON `"code":"NoSuchBucket"`。
危害分析四件套（引用次数+资源类型→js=全站XSS/小程序分包=供应链；业务与人群；攻击链描述；修复建议）→ 写入报告。

### 六、凭证升级（匿名成效不足时）
① 代码挖领签接口（`sts`/`assumeRole`/`getUploadToken`/`getSignedUrl`/`q-sign-algorithm`）→ 匿名/空 token/devtoken 探测；② 拿到凭证用官方 CLI 签（ossutil/coscli/awscli，别自己撸签名）→ 复测 403 面；③ 越权边界：key-prefix 越权（`/admin/` `../`）、跨桶、action 越权、无过期/重放/无 IP 绑定、RoleArn 跨账号（命中即停手汇报）；④ 预签名 URL 篡改：换 key / 追加未签名 header / 改 method / 加长 Expires。

## 核心枪：prefix 绕 ListBucket（只读，最高命中）

心智模型：**桶内每个前缀都是独立枚举目标**；`s3:ListBucket` 桶级动作，管理员漏写 `Condition s3:prefix` 就整桶可列。

```
v1:      /?prefix=<P>&marker=&max-keys=100
v2:      /?list-type=2&prefix=<P>&max-keys=100&start-after=
编码:    /?prefix=static%2F2026%2F&max-keys=100
版本:    /?versions&prefix=<P>
分块:    /?uploads&prefix=<P>
穿透:    /?prefix=static/（Deny 只写 static/2026/* 时）
```

入口：任意已知对象 URL 反推父前缀链逐层测（`static/2026/` → `static/` → 空）+ 403 子路径反查 + 运维目录字典（`debug/ logs/ backup/ .env/ dump/ id_rsa/`）+ 年份序列。endpoint 错配（website/加速/路径风格）同 payload 复测。

判定：200 + `x-*-request-id` + body 出现 `<ListBucketResult>` / `<Contents>` / `<CommonPrefixes>`（`IsTruncated=true` 也算）；`<Error>AccessDenied` 不算。

## 收益判据（决定「先刷哪个」，不决定「刷不刷」）

- **优先打**：有业务数据、有写权限、有凭证（STS/预签名/AK）的桶；纯静态/CDN 本职内容不写（模式已列）—— **不写 ≠ 不测**：桶本身该测的面（列表/prefix/写探测/ACL）一个都不能少，只是不单独成洞。
- **接管类**：只识别不实操；先确认平台收不收（`memory_recall` 该厂商 verdict + `（外部知识库未随包提供）\src\规则\`）—— 收不收影响的是**提交与排序**，不是跳过识别。
- **500 请求上限内先喂高 tier**：已知桶名 > 业务种子 > 种子×region > 泛字典（现有分层已写，别颠）。
- **命中回填**（防下一个目标重复劳动）：桶名 / 厂商 / 权限态（根列表？prefix 绕？写？）/ 是否已报 → `memory_memorize(scope="temp"|"campaign", namespace="ammo")`；同厂商第二次命中，先 `memory_recall` 查是否已提交过（防 duplicate）。

## 产出

- hit 分级映射与清理自检清单：见 `references/ossfuzz-full.md` §8.6 / 报告收尾自检。
- 报告落 `hunts\报告\<厂商>\<目标>\`，格式认 `（外部知识库未随包提供）-mode/kb/report-format.md`；每桶命中后回到权限面完整测一遍再定级。
- 收尾必做：清理台账 DELETE + 复核、`?uploads` 残留 abort、versioning 桶删版本、CDN 刷新提示（如需）。
