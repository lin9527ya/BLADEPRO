# SRC 能力扩展实施计划（三项）

> 范围：①数据源接入　②确定性探针的存活/指纹 + 侦察任务主动开关　③业务逻辑面与双身份对照。
> 每项给出：目标、现状、改动点、数据/接口、验收、风险、工作量。默认按 ①→②→③ 推进，三者在代码上互不阻塞。

---

## 一、数据源接入（配置为主，无代码）

### 目标
把"面"的召回率从"单渠道企业面 + CT/DNS"抬到"有测绘引擎 + 双渠道企业面"。

### 现状（已核实）
- `settings.fofa_key` 存的是失效 key：中转站返回 `-102 会员已到期`，官方返回 `-700 账号无效`；Claude Code 的 fofa MCP 同样失败。
- `intel_search` 已绑 `recon`，provider 只有 `intel/fofa.go`。
- ENScan `cookie_status` 显示 7 个渠道里只有 `rb` 已配置；`skills/src-recon/references/01` 记载 rb 的 `wx_app/copyright/job/wechat` 四维曾整段失效。

### 1.1 FOFA（优先）
1. 在「系统设置 → 情报源（FOFA）」填入**有效 key**；若用中转站，同页把 Base URL 改为对应地址（当前 `https://fofoapi.com`）。
2. 建议同时设置 `intel_daily_quota`（默认 200），防止一次侦察把额度打光。
3. 验收：
   - `curl "https://<base>/api/v1/search/all?key=...&qbase64=<基64>` 返回 `error:false`；
   - SRC 会话里让 agent 跑一次 `intel_search(expr='cert="<目标根域>"')`，返回命中数 > 0；
   - `intel_queries` 台账出现该次记录，配额计数 +1。

### 1.2 备选：Quake / Hunter（FOFA 拿不到时）
- 改动点：`intel/` 新增 provider 实现（实现现有 `Provider` 接口 + `init()` 注册），`server/intel_tools.go` 的工具描述随 `intel.Names()` 自动更新；设置项复用 `fofa_*` 那套命名（新增 `quake_key` 等）。
- 工作量：单个 provider 约 1 天（含归一化应答与错误码映射）；需要账号与配额确认。

### 1.3 ENScan 第二渠道
1. 在 `D:\Skills\enscan\config.yaml` 的 `cookies` 段补 `tyc`（天眼查）/`kc`（快查）任一渠道，重启 `enscan.exe --mcp`。
2. 用 `cookie_status` 确认 `configured:true`，再用 `search_company` 实测有效（configured 只代表已填写）。
3. 技能侧固定"双渠道交叉"规则：写入 `skills/src-recon/references/01-企业面.md` §0——`icp`/`copyright`/`job`/`wechat` 四维必须至少两个渠道各查一次，结果取并集，单渠道空结果不得判"无资产"。
4. 验收：同一企业两渠道各跑一次 `collect_company_info_async`，四个维度至少一维出现差异并被合入清单。

### 风险
- 第三方数据商可见查询词（公司名）；不要把内网域名/内部项目名写进 expr（已在 `intel_search` 描述与技能里约束）。
- 渠道 cookie 有时效，需运维：建议在技能里写"空结果先 `cookie_status`"的固定动作（已存在）。

---

## 二、确定性探针：存活 / 指纹 + 侦察任务主动开关

### 目标
让"资产清单"从"有解析"升级为"有存活、有指纹、有状态码"，同时保持默认只被动。

### 现状
- `srcprobe/probe.go` 只有 `paths/js/methods/non404` 四种 kind；`runner.do` 只发 GET/HEAD/OPTIONS，限速、1MiB 截断、同 host 约束都已具备。
- `server/src_probe.go` 负责范围/时间窗/身份/限速/12h 缓存/证据凭据（`src_probe_runs`）。
- `recon_only` 任务的 worker 提示明确禁止调用 `src_probe`。

### 2.1 新增探针 kind：`alive` 与 `fingerprint`
改动点：
- `srcprobe/probe.go`
  - 常量 `KindAlive = "alive"`、`KindFingerprint = "fingerprint"`；
  - `Options` 增加 `URLs []string`（默认取资产 URL，上限 100，只留同 host）；
  - `alive`：逐个 GET，记录 status / 最终 URL / title / content-type / content-length / 耗时；
    `Metrics`: `alive`、`status_2xx`、`status_3xx`、`status_4xx`、`status_5xx`、`titled`；
  - `fingerprint`：在 `alive` 基础上追加：`Server`/`X-Powered-By`/`Set-Cookie` 关键名、favicon（GET `/favicon.ico`，算 mmh3）、`robots.txt`/`sitemap.xml` 是否 200；
    `Metrics`: `tech`、`favicon`、`robots`、`sitemap`、`cms_hint`；单资产请求数上限 4；
  - 两者都沿用 `runner`（限速、UA、1MiB、同 host），**不跟随跳转**，不访问非根路径（除 favicon/robots/sitemap）。
- `server/src_probe.go`：kind 白名单放行；缓存沿用 12h（缓存 key 已含 kind 与参数 hash）；证据节点照旧。
- `agent/tools_src_probe.go`：schema 的 `kind` enum 增加两项；描述写明"只做存活与指纹，不发 payload、不做端口扫描"。
- `srcmode/checks.go`：可选新增收尾项 `asset_alive`（适用 `app/service`，metrics `alive`），平台档可豁免；若不新增，则只作为 planner 参考指标。

### 2.2 `recon_only` 的受控主动开关
改动点：
- `srcmode/mode.go`：`ModeDoc` 增 `ReconActive bool json:"recon_active,omitempty"`；`Resolve` 校验：仅当 `ReconOnly=true` 时允许；与 `NoAutomation=true` 互斥（平台禁自动化时直接拒绝）。
- `server/src_probe.go`：任务为 `recon_only` 时，只有 `recon_active=true` 且 kind ∈ {alive, fingerprint} 才放行；其余 kind 一律拒绝（沿用现有错误语义）。
- `srcmode/prompt.go`：`reconOnlyWorkerOverlay` 按 `ReconActive` 分支——允许调 `src_probe(alive/fingerprint)` 并把 `alive` 数字写进清单；仍需遵守 `RateLimit`/`TimeWindow`。
- 前端 `create-task-sheet.tsx`：侦察区块加勾选「允许低强度主动验证（存活/指纹）」，写 `modeDoc.recon_active`；说明文案写明"会在授权目标上发 GET，受速率与时间窗约束"。
- 任务详情 `/api/tasks/{id}/src` 已有 `recon_only`，可再加 `recon_active` 供 UI 展示。

### 验收
1. 单测：`srcprobe` 用 httptest 起 200/301/404/超时四种端点，断言 `alive` 与 `fingerprint` 的 metrics 与请求数上限。
2. 单测：`server` 层 `recon_only + recon_active=false` 调 `src_probe(alive)` 被拒；`true` 时放行且写 `src_probe_runs`。
3. 实测：建一个侦察任务勾选主动验证，得到"存活数 / 有标题数 / 指纹数"，清单第 4 表出现状态码与技术栈。

### 风险
- 主动 GET 会触达目标：默认关；必须过 `SrcAssetInScope`/`SrcHostInScope`/`RateLimit`/`TimeWindow`/`NoAutomation`（全部复用现有门）。
- favicon/robots/sitemap 是额外请求：单资产请求数硬上限 4，且只对根路径。
- 不要把它扩成端口扫描/目录爆破——那是任务内 `src_probe(paths)` 的事，侦察任务仍不开放。

### 工作量
`srcprobe` 两 kind + host 接线 + 开关 + 前端 ≈ 1.5–2 天；测试 0.5 天。

---

## 三、业务逻辑面与双身份对照

### 目标
把 `logic` 从"一个粗格子"变成"可验收、有工具支撑的测试面"，并把越权/IDOR 的对照从手工改为半自动。

### 现状
- `srcmode/taxonomy.go`：`logic` 面已有 4 个细分项 `state-skip / race / privilege / money`，但没有对应的收尾项（`checks.go` 里无 logic 检查）。
- `src_identity_diff`（`agent/tools_src_identity.go` + `server/src_identity.go`）已能对**单条路径**用 A/B 身份各发一次 GET 并落事实；凭据来自 `credential_env`，不进提示词；但没有批量、没有会话缓存、没有 ID 枚举。
- `src_probe` 已支持 `identity` 参数（从 `test_identities` 取 label）。

### 3.1 logic 面拆成可验收子项
1. `srcmode/taxonomy.go`：`logic.Items` 扩为 `state-skip / race / privilege / money / workflow / idempotent`（前端 `SrcAreaPicker` 与 `taxonomy` 校验共用同一份常量，自动生效）。
2. `srcmode/checks.go`：新增收尾项
   `logic_flows`：Title「业务逻辑流程」，适用 `app/service`，metrics `flows`（已走通的业务流程数）、`evidenced`（带证据的条数）；full 档 `MinFull: {"flows":2}`，platform 档 `MinPlatform: {"manual_flows":1}`。
3. `srcmode/na.go`：新增理由码 `no_state_machine`（无多步流程）、`no_business_object`（无业务对象），避免用 `no_input` 硬套。
4. `srcmode/prompt.go`：planner overlay 增一段"logic 面先列业务流程（登录→下单→改单→退款等），每条流程派一条意图"；worker overlay 要求每条流程给出请求序列与响应差异。
5. 前端：无改动（面/收尾项都是数据驱动）。

### 3.2 双身份对照自动化（分两级）
**L1：批量对照（低风险，先做）**
- `agent/tools_src_identity.go`：`path` 支持数组 `paths[]`（上限 20），或新增 `from_endpoints`（自动从 `list_assets` 取该资产的 endpoint）。
- `server/src_identity.go`：循环复用现有单路径实现；汇总 `distinct[]`、`same[]`、`errors[]`；请求数按 `RateLimit` 排队。
- 证据：仍写 fact（`IdentityDiffFacts` 已有），metrics 汇总可挂到 `src_mark_cell` 的 authz 面说明里。

**L2：ID 枚举（中等，后做）**
- `srcprobe` 新增 kind `id_enum`：入参 `template`（含 `{id}`）、`ids`（列表或区间，上限 50）、`identity_a`/`identity_b`；逐个 ID 用 B 取一次、A 再取一次（只 GET），输出"A 不可读、B 可读"的候选集合与差异摘要。
- 严格约束：只读、同 host、QPS 走 runner、命中数上限、结果落 `src_probe_runs` 作为可信凭据。
- 判定仍是"线索"：要变 finding 必须 `report_finding` + 证据，规则不变。

### 3.3 不做自动登录
`credential_env` 约定为**可直接使用的 Cookie/Token**；表单登录、验证码、2FA 保持人工。若要自动化，需单独立项（浏览器 MCP + 凭据保险箱），不在本次范围。

### 验收
1. `logic_flows` 收尾项出现在 `src_state` 与 `src_finish_check` 的缺口里；未做时收尾门拒绝 done。
2. 批量 `src_identity_diff` 对 10 条路径返回 `distinct` 列表，请求数 = 20 且受限速约束。
3. `id_enum` 在 httptest 假服务上：A 身份 403、B 身份 200 时输出该 ID；A 也可读时不进候选。
4. 完整链路：选中一个真实 SRC 任务，用 logic 面跑通一条业务流程并留下证据，收尾门通过。

### 风险
- 批量对照会让请求数翻倍：必须按 `RateLimit` 串行，且默认上限 20 条路径。
- ID 枚举容易被识别为爬取：默认关闭，需显式参数、且受平台 `no_automation`/`rate_limit`/时间窗约束。
- 越权"差异"不等于"越权"：工具只给线索，判定与报告仍走独立复验。

### 工作量
3.1 ≈ 1 天；3.2-L1 ≈ 1.5 天；3.2-L2 ≈ 2–3 天（含护栏与测试）。

---

## 四、推进顺序与验收口径

| 顺序 | 项 | 依赖 | 工作量 | 首要验收 |
|---|---|---|---|---|
| 1 | FOFA key / ENScan 第二渠道 | 账号可用性 | 0.5 天（配置） | `intel_search` 有命中；两渠道交叉出差异 |
| 2 | alive / fingerprint + recon_active | 无 | 2–2.5 天 | 侦察任务产出存活数与指纹，默认关闭 |
| 3 | logic 子项与收尾项 | 无 | 1 天 | 收尾门对 logic 产生可验收缺口 |
| 4 | 身份对照 L1 批量 | 3 | 1.5 天 | 批量 diff 出 distinct 列表 |
| 5 | 身份对照 L2 ID 枚举 | 4 | 2–3 天 | 假服务上正确识别越界可读 |

整体验收不看"扫了多少"，看三件事：
- **资产质量**：侦察任务产出的清单里，有存活状态与指纹的比例；
- **逻辑面**：logic 收尾项的终态率与证据条数；
- **越权线索**：`src_identity_diff` 产生的 distinct 候选中，经复验成立的条数。

---

## 五、实施状态

| 项 | 状态 | 落地内容 |
|---|---|---|
| ② alive / fingerprint 探针 | **已实现（2026-11-01）** | `srcprobe/probe_meta.go`（`alive`/`fingerprint` 两 kind、`metaURLs`、响应头指纹、favicon mmh3、robots/sitemap）、`srcprobe/favicon.go`（MurmurHash3 + FOFA 口径哈希）、`Options.URLs/MaxURLs`、`Result.Tech/FaviconMMH3`；单测 `srcprobe/probe_meta_test.go`（含 mmh3 向量与 favicon 哈希对照） |
| ② recon_active 开关 | **已实现** | `srcmode.ModeDoc.ReconActive` + `Resolve` 校验（只能配 recon_only；与 no_automation 互斥）；`server/src_probe.go` 对 recon_only 任务只放行 alive/fingerprint；`srcmode/prompt.go` worker overlay 按开关分支；`/api/tasks/{id}/src` 返回 `recon_active`；创建页新增勾选 |
| ① 数据源 | **待账号** | 接线已完成（`intel_search` 绑 recon、设置项、技能文档），缺有效 FOFA key；ENScan 第二渠道待配置 |
| ③ logic 面 | **已实现（2026-11-01）** | `taxonomy` logic 细分项加 `workflow` / `idempotent`；`checks` 新增收尾项 `logic_flows`（full：flows≥2 且 evidenced≥2；platform：flows≥1；要求证据节点）；`na` 新增 `no_state_machine` / `no_business_object`；planner/worker overlay 增业务流程段；单测 `srcmode/logic_test.go` |
| ③ 双身份 L1（批量） | **已实现** | `src_identity_diff` 支持 `paths[]`（≤20 条，与 `path` 二选一），返回逐条结果与 `distinct_count`；宿主按任务 QPS 排队、每条路径各落一条事实证据；单测新增批量用例（需 `BLADEPRO_TEST_PG_DSN`） |
| ③ 双身份 L2（ID 枚举） | **已实现（2026-11-01）** | `srcprobe` 新增 `id_enum`：`{id}` 模板 + 两个身份各 GET 一次，按 `same_body/diff_body/a_only/b_only/neither` 归类，输出 `id_candidates`；ID ≤50、字符集受限、模板必须同 host 且恰好一个 `{id}`；宿主解析 A/B 身份（label 不同、凭据只来自环境）并按任务 QPS 排队；不缓存、走 `src_probe_runs` 凭据 |
