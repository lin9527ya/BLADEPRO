# BLADE 改造为 SRC 挖掘 Agent：设计与实施方案

> 版本：v0.1（2026-10-22）
> 范围：在 BLADE 现有 Go 单体 + Next.js 前端上，增加一个 `src` 任务模式，把“全链渗透”改造成“SRC 挖掘”。
> 结论先行：**做模式，不做 fork；改目标函数、收尾判据、交付面三层，不是只改提示词。**

---

## 0. 结论

1. **不改造成 SRC-only 分支**。BLADE 的引擎是通用的，SRC 是一套运行配置。做成 `tasks.mode = 'src'` 的模式，可回退、可对比、不丢上游；验证透了再把 SRC 设为默认。
2. **三处硬改造**，缺一不可：
   - **规划层**：planner 的输入里加入“覆盖空格”，把“往深打”改成“补格优先”；
   - **判定层**：显式覆盖格 `src_cells` + engine 收尾门，空格没清零不能 done；
   - **交付层**：报告正文写全 + 证据合规 + 跨任务去重 + 提交台账。
3. **只改提示词会失败**。BLADE 现在的 planner 奖励“证明目标、拿据点”，提示词压不住；必须同时改态势输入和 engine 的收尾判据。
4. **建议工期 3–4 周**（单人）；其中 P0+P1 约 2 周，就能看到“像 SRC”的行为。另有“一周最小版”见 §10.5。
5. **先硬编码一个 `src` 模式**，不要一上来做通用 plugin 内核。接口留薄，跑通再抽象。

---

## 1. 现状分析

### 1.1 架构与 SRC 相关链路

BLADE 是 LLM 多 agent 驱动的自主渗透系统：

- Go 单体后端 + 内嵌 Next.js；PostgreSQL 存双图（探索图 + 资产图）；
- 每任务一个 `plannerLoop` + N 个 worker；planner 派意图，worker 领意图执行并写回；
- 工具经 `guard`/`intercept` 审批门；流量经记录型 MITM 代理留痕；
- 交付面已有 SRC 单条导出、批量交付、证据门。

### 1.2 已经具备的能力（不要重做）

| 能力 | 位置 | 说明 |
|---|---|---|
| 探索策略 | `policy/*`、`server/policy_compile.go` | 禁用手段/敏感写/自定义规则，任务创建时落 effective 快照；默认就是 SRC 姿态（禁 bruteforce/dos/webshell/persistence 等） |
| 覆盖矩阵 | `db/task_scope.go` `CoverageMatrix`、`agent/tools_insert.go` `coverage_matrix` | 范围内资产 × 意图 `risk_area` 的已测/未测成/进行中/范围外计数 |
| SRC 报告版式 | `report/src.go` | 8 段 + 全角＃分隔；`SrcInput` 已含 Description/Impact/Fix 字段 |
| docx | `report/docx.go` | 纯标准库生成 |
| 单条导出/证据门 | `server/src_export.go` | `srcWarnings` 检查无绑定流量/无 proof/已撤回/长任务未收束 |
| 批量交付 | `server/src_batch.go` | 交付计划、疑似重复分组、zip |
| 指纹 | `report/fingerprint.go` | 漏洞类型 + 归一化端点 → sha256 前 16 位 |
| 报告 Agent | `agent/promptcatalog.go` `ReporterDefaultPrompt` | 负责把 finding 写成详细 Markdown |
| 流量证据 | `traffic/*`、`finding_traffic.go` | 请求/响应快照、绑定版本 |
| 资产图 | `db/assets.go`、`enrich/*` | 域名/子域/服务/端点，DNS/HTTP 异步补全 |
| 任务范围 | `db/task_scope.go` | company/root_domain/subdomain/ip/cidr/icp/keyword |

### 1.3 和 SRC 挖掘的差距

| 维度 | 现在 | SRC 需要 | 差距性质 |
|---|---|---|---|
| 目标 | 证明目标、拿据点、走完攻击链 | 单位时间可提交的有效漏洞数 | **目标函数** |
| 规划 | 深度优先；`prove_goal` 是收尾驱动 | 广度优先；按“站点 × 风险面”补缺 | **planner 输入/输出** |
| 完成 | goals met 即 done | 覆盖格清零 + 发现分诊 + 报告就绪 | **engine 硬门** |
| 覆盖 | 从 intent 推导；没 intent = 不显示 | 空格 = 没做，必须显式可见 | **数据模型** |
| 产出 | 报告/战果/遗留物 | 可提交件 + 提交台账 | **交付面** |
| 去重 | 任务内指纹分组 | 跨任务/跨项目 | **数据模型** |
| 证据 | 流量绑定 + 缺项警告 | 抄包检测 + 整包可复现 | **校验器** |
| 低危 | 通常不追 | 全收台账；低危/信息级不出报告 | **策略** |
| 节奏 | 意图总数上限 | 每站/每格尝试上限、速率、时间窗 | **预算/拦截规则** |

### 1.4 为什么现在能改

BLADE 已经有三处“可插拔”的缝，改造成本比外挂插件低：

1. **任务级策略快照**：`tasks.exploration_policy` 的模式可以照抄成 `tasks.mode` + `tasks.mode_doc`；
2. **planner/worker 的按任务装配**：`server/task_llm.go` `agentsForTask` 已经按任务注入 policy resolver、exec service、guard，加一个 mode 是同类改动；
3. **覆盖矩阵已有雏形**：`CoverageMatrix` 和 `coverage_matrix` 工具已经存在，只差“显式终态 + 收尾门”。

---

## 2. 目标与非目标

### 2.1 成功标准

一次 `mode=src` 的任务，在授权范围内做到：

1. 每个站点面资产的必做风险面都有终态（`tested_found` / `tested_clear` / `na` / `budget_stop`），没有空白；
2. 每条 medium 及以上 finding 有可复现整包、有 8 段报告、通过证据合规检查；
3. 没有未处理的重复（同指纹跨任务可查）；
4. 所有提交有台账（平台、状态、externalId）；
5. 任务收尾由 engine 判定，不由模型自称；
6. `mode=""` 的任务行为与改造前完全一致。

### 2.2 非目标

- 不做 SRC 平台自动提交（只生成可提交件与台账）；
- 不做多租户隔离（BLADE 仍是单操作者）；
- 不在本期重写资产图/流量代理/LLM 池；
- 不做通用插件市场/动态加载；`src` 先硬编码；
- 不追求“零误报”——证据门允许人工 override，但必须留痕。

---

## 3. 总体设计

### 3.1 模式而不是 fork

```go
// tasks.mode: ''（普通渗透） | 'src'
// tasks.mode_doc: 模式快照（JSONB），创建时写死，运行中不回溯
type ModeDoc struct {
    DocVersion    int             `json:"doc_version"`
    Mode          string          `json:"mode"`
    ProgramID     *int64          `json:"program_id,omitempty"`
    RequiredAreas map[string][]string `json:"required_areas,omitempty"` // asset_type -> areas
    AllowBudgetStop bool          `json:"allow_budget_stop"`
    MaxAttemptsPerCell int        `json:"max_attempts_per_cell"`
    MaxIntentsPerAsset int        `json:"max_intents_per_asset"`
    HardItems     []string        `json:"hard_items,omitempty"` // 如 "paths/enum-200"
}
```

快照语义照 `ExplorationPolicy`：创建时解析，运行中只读。模式不可中途切换；要换就复制任务。

### 3.2 三层改造

```
┌─────────────────────────────────────────────────────────────┐
│ 规划层：planner 输入加 src_gaps；输出按“补格”排序              │
│  - graphOverviewData 增加 src 块（top gaps / counts）          │
│  - prompt overlay：空格清零前不 prove_goal                     │
│  - worker：结束前必须 src_mark_cell                            │
├─────────────────────────────────────────────────────────────┤
│ 判定层：src_cells + engine 收尾门                              │
│  - 显式终态（tested_found/tested_clear/na/budget_stop）        │
│  - 空格/缺理由/缺证据 → 拒绝 done，注入待办                     │
│  - 连续拒绝有上限，避免死循环                                   │
├─────────────────────────────────────────────────────────────┤
│ 交付层：报告正文 + 证据合规 + 去重 + 提交台账 + 批量包           │
│  - src_prepare_submission 渲染 8 段并校验                      │
│  - 指纹跨任务查重                                              │
│  - src_batch 打包提交说明 + 报告 + 证据 + 台账                  │
└─────────────────────────────────────────────────────────────┘
```

### 3.3 运行时链路

```
任务创建(mode=src)
  → mode_doc 快照
  → planner 唤醒：读 src_gaps → 派“补格”意图（带 asset_id + area/item）
  → worker 领意图：执行 → 写 fact/finding → src_mark_cell → 结束
  → engine 唤醒 planner：gaps 变化
  → planner 判 met → engine 调 srcmode.CheckFinish
       ├─ 有空格：拒绝 done，注入 gaps 提示，继续
       └─ 无空格：done
  → reporter 写 8 段正文 → src_prepare_submission
  → 人工/主 Agent 记提交状态 → src_batch 导出
```

### 3.4 兼容与回退

- `mode` 默认 `''`，所有现有任务/查询/归档不受影响；
- 新增表全部 `IF NOT EXISTS`，旧库启动即迁移；
- 新增列全部 `DEFAULT`，旧二进制读到多余列不受影响（但要先确认归档恢复的显式列清单，见 §10.2）；
- 回退：任务 `mode` 置空即可回到普通渗透；已写的 `src_*` 表保留不读。

---

## 4. 模式机制

### 4.1 数据模型（最小）

```sql
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS mode_doc JSONB NOT NULL DEFAULT '{}'::jsonb;
CREATE INDEX IF NOT EXISTS idx_tasks_mode ON tasks(mode) WHERE mode <> '';
```

`db.Task` 增加：

```go
Mode    string          `json:"mode,omitempty"`
ModeDoc json.RawMessage `json:"mode_doc,omitempty"`
```

`TaskCreateOptions` 增加 `Mode string`、`ModeDoc json.RawMessage`。

### 4.2 传播点（必须逐个改，漏一个就会出现“创建是 SRC、恢复后变普通”）

| 位置 | 改动 |
|---|---|
| `db/tasks.go` `taskCols` / `scanTask` / `CreateTaskWithOptions` INSERT | 加 `mode, mode_doc` |
| `server/manager.go` `Task` 结构 + `taskFromPG` | 复制两个字段 |
| `server/llm_api.go` `createTaskReq` | 加 `Mode string`、`ModeDoc`（可选） |
| `server/tasks_api.go` `createTask` | 校验模式、解析默认 `mode_doc`、传给 `CreateTaskWithOptions` |
| `server/dto.go` `TaskDTO` / `taskDTO` | 暴露 `mode`、`mode_doc` 摘要 |
| `server/task_archives_restore.go` | 归档/恢复的显式列清单加两个字段 |
| `server/schedules.go` `scheduleSpec` / `createTaskFromSpec` | 计划任务可带 mode（P3） |
| `server/orchestration.go` `spawn_task` | 子任务可显式继承/指定 mode（默认不继承） |
| `web/src/lib/types.ts` `Task` | 加 `mode` |

### 4.3 模式校验

创建任务时：

```go
func ResolveModeDoc(mode string, raw json.RawMessage) (json.RawMessage, error) {
    if mode == "" { return nil, nil }
    if mode != "src" { return nil, fmt.Errorf("未知任务模式：%s", mode) }
    // 解析默认 + 覆盖；未知字段忽略，未知 area 拒绝
    // 校验 required_areas、hard_items、budget 上限
}
```

模式不可变。需要改模式 → 复制任务（复用 `source_task_ids` 或新建）。

---

## 5. 规划层：从“深挖”到“补格”

### 5.1 planner 输入

现在 `graphOverviewData` 已有 `coverage` 粗粒度块。SRC 模式增加一个 `src` 块（只放摘要，控制体积）：

```json
{
  "src": {
    "gaps_total": 37,
    "gaps_by_area": { "authz": 12, "injection": 9, "upload": 5, "other": 11 },
    "top_gaps": [
      { "asset_id": 12, "asset": "api.example.com", "area": "authz", "item": "*", "priority": 9 },
      { "asset_id": 18, "asset": "admin.example.com", "area": "upload", "item": "*", "priority": 8 }
    ],
    "cells_total": 120,
    "cells_done": 83,
    "submissions": { "draft": 2, "submitted": 1 },
    "blocked_finish": true
  }
}
```

只在 `mode=src` 时注入；`top_gaps` 最多 10 条，完整清单走 `src_state`。

优先级公式建议：

```
priority = asset_value × area_weight ÷ attempts_so_far
asset_value: 主域/后台/登录口/API > 普通子域 > 静态资源
area_weight: injection/authz/rce/upload > xss/ssrf > disclosure/paths
```

### 5.2 planner 输出

在 `plannerDefaultTmpl` 后追加 `src` 模式覆盖段（代码固定，不按轮注入状态）：

- 本轮只派能**点亮空格**或**把 tested_clear 变 tested_found** 的意图；
- 同一格最多 `max_attempts_per_cell` 次（默认 2），超过必须 `budget_stop` 并写原因；
- `src_state` 显示 gaps=0 之前不调 `prove_goal`；
- 允许“矩阵外面”的探索，但产出必须落格（新面先 `src_mark_cell` 再干）；
- 串行链仍然分步派，但链的终点必须对应一个格；
- 不追 foothold/凭据/持久化（策略已禁，提示词再强调）。

### 5.3 预算与公平

- `policy.MaxIntentsTotal` 继续可用；
- `mode_doc.MaxIntentsPerAsset` 防止一个资产吃掉全部预算；
- `mode_doc.MaxAttemptsPerCell` 防止一格反复试；
- 任务级 timeout 继续生效；SRC 建议 2–4 小时/任务，按平台规模配。

### 5.4 worker 纪律

`workerDefaultTmpl` 后追加：

- 领到的意图若带 `cell`（asset_id + area + item），结束前必须 `src_mark_cell`；
- `tested_clear` 必须附 `evidence_node_id`（一条 fact）或说明；
- `na` / `budget_stop` 必须写原因；
- 只读证明：命令执行证明到 `whoami`/`id` 即止；不写 webshell、不建账号、不改配置；
- 整包当场抓、逐字照抄；报告里要能直接贴；
- 结束语固定格式：测了什么 / 结论 / cell 状态 / 证据位置。

---

## 6. 覆盖矩阵与收尾门

### 6.1 现有 `coverage_matrix` 的问题

`db/task_scope.go` 的 `CoverageMatrix` 是从 **intent 的 `risk_area`** 推导的：

- 没有 intent 的风险面 → 矩阵里**不出现**，看起来像“没这个面”，而不是“没测”；
- 无法表达 `tested_clear`（测了没洞）与 `na`（不适用）；
- 没有“谁在什么时候因为什么放弃”的留痕；
- 不能作为收尾判据（planner 可以直接 `prove_goal`）。

**结论：保留它作为“意图统计”的只读视图，新增显式 `src_cells` 作为 SRC 的判定源。**

### 6.2 `src_cells` 模型

```sql
CREATE TABLE IF NOT EXISTS src_cells (
    id               BIGSERIAL PRIMARY KEY,
    task_id          BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    asset_id         BIGINT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    area             TEXT NOT NULL,
    item             TEXT NOT NULL DEFAULT '*',
    state            TEXT NOT NULL CHECK (state IN ('tested_found','tested_clear','na','budget_stop')),
    reason           TEXT NOT NULL DEFAULT '',
    evidence_node_id BIGINT REFERENCES exploration_nodes(id) ON DELETE SET NULL,
    finding_id       BIGINT REFERENCES findings(id) ON DELETE SET NULL,
    attempts         INT NOT NULL DEFAULT 0,
    updated_by       TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, asset_id, area, item)
);
CREATE INDEX IF NOT EXISTS idx_src_cells_task ON src_cells(task_id, asset_id);
CREATE INDEX IF NOT EXISTS idx_src_cells_state ON src_cells(task_id, state);
```

语义：

- `(area, item)`：`item='*'` 表示整组终态；否则是细分项（如 `paths/enum-200`）。
- 空格 = 没有行；**不预填**。只存终态，避免资产 × 面爆炸。
- `tested_found` 必须带 `finding_id`；
- `tested_clear` 必须带 `evidence_node_id` 或非空 `reason`；
- `na` / `budget_stop` 必须带 `reason`；
- `attempts` 由派意图/收尾时累加，用于“同一格最多 N 次”。

### 6.3 风险面 taxonomy

SRC 用一套**粗粒度、可判定**的面，和 `db.RiskAreas` 的细粒度 intent 标签分开：

| area | 含义 | 典型 item |
|---|---|---|
| `unauth` | 未授权/未认证可达 | `*`, `swagger`, `actuator`, `graphql` |
| `authz` | 越权/IDOR | `*`, `idor`, `vertical`, `mass-assign` |
| `injection` | SQLi/NoSQLi/SSTI/命令/表达式 | `*`, `sql`, `nosql`, `ssti`, `cmdi` |
| `ssrf` | SSRF | `*`, `url-param`, `webhook`, `cloud-meta` |
| `xss` | XSS | `*`, `reflect`, `stored`, `dom` |
| `rce` | 反序列化/上传 getshell/依赖 RCE | `*`, `deser`, `upload-chain` |
| `cred` | 凭证/密钥/会话泄露 | `*`, `js-hardcoded`, `repo-leak` |
| `paths` | 敏感路径/备份/配置 | `*`, `enum-200`, `non404-retest` |
| `session` | 登录/重置/JWT/OAuth/默认口令 | `*`, `login-bypass`, `jwt` |
| `oauth` | OAuth/OIDC 流程与配置 | `*`, `redirect-uri`, `state`, `pkce` |
| `graphql` | GraphQL 内省/批量/别名滥用 | `*`, `introspection`, `batching`, `authz` |
| `websocket` | WebSocket/实时通道 | `*`, `auth`, `origin`, `message-injection` |
| `supply` | 第三方 JS/SDK/供应链 | `*`, `third-party-js`, `sdk-key`, `callback` |
| `cache` | Web 缓存投毒/欺骗 | `*`, `poison`, `deception`, `vary` |
| `desync` | HTTP 请求走私/desync | `*`, `cl-te`, `te-cl`, `h2` |
| `upload` | 上传/穿越/解析差异 | `*`, `bypass`, `lfi` |
| `logic` | 状态机/金额/竞态/批量赋值 | `*`, `state-skip`, `race` |
| `host` | 端口/服务暴露/已知 CVE | `*`, `exposed`, `cve` |
| `takeover` | 子域接管 | `*` |

必做面按资产类型（`mode_doc.required_areas` 可覆盖）：

| 资产类型 | 必做 area |
|---|---|
| `app` / `service`（HTTP） | unauth, authz, injection, xss, paths, session, upload, logic |
| `endpoint` | unauth, authz, injection, ssrf, logic |
| `ip` | host, cred |
| `root_domain` / `subdomain` | takeover, host, paths |
| `repo`（白盒，P3） | cred, injection, authz |

“必做”是下限，不是上限；矩阵外的面照样能报，只是不占格。

### 6.4 收尾门算法

```go
type Gap struct {
    AssetID int64  `json:"asset_id"`
    Asset   string `json:"asset"`
    Area    string `json:"area"`
    Item    string `json:"item"`
    Kind    string `json:"kind"` // blank|missing_reason|missing_evidence|missing_finding|report_missing|duplicate
}

// srcmode 不 import server（避免循环依赖）：任务字段由调用方传入。
func CheckFinish(ctx context.Context, d *db.DB, taskID int64, doc ModeDoc) (ok bool, gaps []Gap, total int)
```

判定：

1. 取任务范围内、承载站点面的资产（`task_scope` × `assets` 的 `app/service/endpoint/ip/root_domain/subdomain`）；
2. 对每个资产、每个必做 area：
   - 有 `(asset, area, '*')` 或全部所需 item 的终态 → 过；
   - 无行 → `blank`；
   - `na`/`budget_stop` 无 `reason` → `missing_reason`；
   - `tested_clear` 无 `evidence_node_id` 且无 `reason` → `missing_evidence`；
   - `tested_found` 无 `finding_id` → `missing_finding`；
3. 所有 medium+（且不是 retracted/false_positive/duplicate）finding：
   - 没有 `src_submissions` 或 `report_ready=false` → `report_missing`；
4. 未处理的同指纹重复 → `duplicate`；
5. `budget_stop` 默认也算“未测完”；只有 `mode_doc.allow_budget_stop=true` 才放行，并在报告备注里写明。

返回 `total`（总缺项）与 `gaps`（最多 50 条，供提示词/UI）。

### 6.5 engine 集成

`server/engine.go` 的 `case met:` 分支：

```go
case met:
    if t.Mode == "src" {
        ok, gaps, total := srcmode.CheckFinish(ctx, s.m.pg, tidOfTask(t), srcmode.ParseModeDoc(t.ModeDoc))
        if !ok {
            // 1) 不落 done
            // 2) 落一条 activity（kind=src_gate），写 total + 前几条
            // 3) 给 planner 注入一次 gaps 提示（同一份 gaps 哈希去重）
            // 4) 连续拒绝计数 +1；达到上限转 policy_blocked，交人工
            break
        }
    }
    e.m.SetTaskStatus(t.ID, "done")
```

防死循环：

- 同一份 gaps 哈希连续注入不超过 1 次；
- 连续拒绝 ≥3 次 → `SetTaskStatus("policy_blocked")`，理由写“SRC 收尾门连续拒绝，需人工处理”；
- 人工可在任务详情页“强制收尾”，写理由并落审计。

### 6.6 人工放行

`POST /api/tasks/{id}/src/force-finish`：

- 只允许终态前置检查失败时调用；
- 必填 `reason`；
- 落 `src_cells` 的 `budget_stop` 或 activity 审计；
- 任务转 `done`，报告备注写“人工放行，缺口见台账”。

---

## 7. 交付层

### 7.1 报告正文写全

`report.SrcInput` 已有 `Description/Impact/Fix`，只是 `buildSrcInput` 没填。SRC 模式：

- 新增 `src_submissions` 行，存 `description/impact/fix/endpoints/steps`（或从 finding 的 report 解析）；
- `buildSrcInput` 优先读 submission，其次回退 `finding.Summary + finding.Report`；
- `SrcMarkdown` / `SrcDOCX` 不变，保证版式一致。

reporter 提示词追加：

- 描述：第一人称大白话，讲“做成了什么”，不写步骤清单；
- 危害：每种伤害单独一行，写最终伤害，不写过程；
- 修复：贴合本漏洞、可落地，不写套话；
- 证据：整包原文，不转述；
- 禁止占位符（`（待补）`）——缺正文直接拒绝保存。

### 7.2 证据合规

在 `srcWarnings` 之外增加 `srcmode.CheckCompliance(finding)`：

1. medium+ 至少一条 `proof` 或 `verification` 绑定；
2. 每步 `Content-Length` 不得大于正文实际字节数（跳过 chunked/gzip/二进制）；
3. 按请求唯一的标识头（`x-request-id`/`x-trace-id`/`x-tt-logid`/`x-b3-traceid` 等）不得跨步骤重复；
4. 请求 Host/URL 在任务 scope 内；
5. 响应不能为空（状态行 + 头 + 体至少有一项）。

结果存 `src_finding_meta.compliance`（JSONB），`report_ready` 由它和正文完整性共同决定。

### 7.3 去重

- 指纹算法直接复用 `report.Fingerprint`；
- 在 finding 导入/提交时算一次，存 `src_finding_meta.fingerprint`；
- 跨任务查询：同 company + 同 program 下 `fingerprint` 相同的 finding/submission；
- 命中 → `dedupe_of` + `duplicate` 状态；提交前必须人工确认“不是同一个洞”才能解除；
- `src_batch` 的重复分组继续保留（任务内视图），跨任务去重走台账。

### 7.4 提交台账

```sql
CREATE TABLE IF NOT EXISTS src_submissions (
    id             BIGSERIAL PRIMARY KEY,
    task_id        BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    finding_id     BIGINT NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    program_id     BIGINT, -- P3 才加外键：REFERENCES src_programs(id) ON DELETE SET NULL
    platform       TEXT NOT NULL DEFAULT '',
    external_id    TEXT NOT NULL DEFAULT '',
    title          TEXT NOT NULL,
    severity       TEXT NOT NULL,
    platform_level TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'draft'
                     CHECK (status IN ('draft','ready','submitted','accepted','duplicate','rejected','paid','withdrawn')),
    fingerprint    TEXT NOT NULL DEFAULT '',
    report_md      TEXT NOT NULL DEFAULT '',
    report_path    TEXT NOT NULL DEFAULT '',
    submitted_at   TIMESTAMPTZ,
    verdict_at     TIMESTAMPTZ,
    reward         TEXT NOT NULL DEFAULT '',
    note           TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_src_submission_finding_platform
    ON src_submissions(finding_id, platform);
CREATE INDEX IF NOT EXISTS idx_src_submissions_status ON src_submissions(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_src_submissions_fingerprint ON src_submissions(fingerprint);
```

状态机：

```
draft → ready → submitted → accepted | duplicate | rejected
accepted → paid
任意 → withdrawn
```

规则：

- `ready` 需要报告 + 证据合规通过；
- `submitted` 需要 `external_id` 或提交凭证；
- `duplicate` 必须关联 `dedupe_of`；
- 低危/信息级可以 `draft`，但不出报告（只进台账）。

### 7.5 批量包

`src_batch` 扩展为：

```
提交说明.md          # 清单 + 证据门 + 重复分组 + 提交状态
findings/<id>-<标题>.md|.docx
evidence/<id>/<n>.txt   # 整包原文
submissions.json        # 台账快照
```

`提交说明.md` 必须写清：哪些进包、哪些没进包及原因、哪些疑似重复、哪些缺口。

---

## 8. 工具与提示词

### 8.1 工具清单（mode-only，不 seed 进 tools 表）

| 工具 | 角色 | 说明 |
|---|---|---|
| `src_state` | planner/mainagent | 读 gaps 摘要 + 明细 + 提交统计 |
| `src_mark_cell` | worker/planner | 点亮一格；终态校验 |
| `src_finish_check` | planner | 只读收尾门预检，返回 gaps |
| `src_prepare_submission` | planner/reporter | 建/更新台账 + 渲染 8 段 + 合规检查 |
| `src_dedupe` | planner/reporter | 按指纹查重 |
| `src_program` | planner | 读平台规则/范围/等级映射（P3） |

**为什么不做成 DB seed 工具**：`wireTools` 会把「DB 里绑定了该 agent、但 base 列表里没有」的域工具注入回去（`server/assembly.go` 的注入分支）。如果 `src_*` 进了 `tools` 表并绑定 planner/worker，非 SRC 任务也会被注入。因此：

- 不加入 `BuiltinToolSeeds` / `builtinToolsByAgent`；
- 只在 `ToolSet.mode == "src"` 时由 `PlannerTools`/`WorkerTools` 追加；
- 代价：UI 里看不到/不能改这些工具的描述；可接受，后续要可编辑再加 `mode` 维度。

### 8.2 planner 提示词要点

```text
【SRC 模式】你的首要目标不是“把一条链打穿”，而是“把覆盖矩阵的空格清零”。
- 每轮派意图前，先读 src 态势（gaps_total / top_gaps）。
- 只派能点亮空格、或把 tested_clear 变 tested_found 的方向。
- 同一格最多尝试 <N> 次；超过必须 budget_stop + 原因。
- gaps_total > 0 时不得 prove_goal；gaps 清零后由 engine 收尾门复核。
- 矩阵外的新面可以打，但必须先 src_mark_cell 建格，再派意图。
- 不追据点/凭据/持久化；只读证明。
```

### 8.3 worker 提示词要点

```text
【SRC 模式】你领到的意图绑定了一个覆盖格（asset_id/area/item）。
- 结束前必须 src_mark_cell：tested_found（附 finding_id）/ tested_clear（附 fact id）/ na / budget_stop（附原因）。
- 只读证明：命令执行证明到 whoami/id 即止；不写 webshell、不建账号、不改配置、不横向。
- 整包当场抓、逐字照抄；报告要能直接贴。
- 结论按证据分级；未复现的写成 fact，不写成 finding。
```

### 8.4 reporter 提示词要点

```text
【SRC 报告】八段正文，缺一段/缺证据不得保存：
- 描述：第一人称，讲做成了什么；
- 危害：每种伤害单独一行，写最终伤害；
- 修复：贴合本漏洞、可落地；
- 证据：整包原文；
- 禁止占位符、禁止“疑似/可能”当结论。
```

### 8.5 mainagent

- 能读 `src_state`，向人解释“还差哪些格”；
- 不直接改 cells（除非人工指令），避免绕过 planner；
- 可以 `src_prepare_submission` / 改提交状态。

---

## 9. API / DTO / Web

### 9.1 API

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/tasks` | body 加 `mode`、`mode_doc`（可选覆盖） |
| GET | `/api/tasks/{id}/src` | `{cells, gaps, findings, submissions, summary}` |
| POST | `/api/tasks/{id}/src/cells` | 人工点格/重置，body 带 reason |
| POST | `/api/tasks/{id}/src/force-finish` | 人工放行，必填 reason |
| GET | `/api/src/submissions` | `?task=&status=&platform=` 台账 |
| PATCH | `/api/src/submissions/{id}` | 改状态/external_id/reward/note |
| GET/POST | `/api/src/programs` | P3 |
| GET | `/api/src/programs/{id}` | P3 |

### 9.2 DTO

`TaskDTO` 增加：

```go
Mode      string `json:"mode,omitempty"`
SrcGaps   int    `json:"src_gaps,omitempty"`
SrcCells  int    `json:"src_cells,omitempty"`
SrcDone   int    `json:"src_done,omitempty"`
SrcSubs   int    `json:"src_submissions,omitempty"`
```

列表页只查聚合，不拉明细。

### 9.3 Web

- **创建任务**：`create-task-sheet.tsx` 加“模式”选择（普通渗透 / SRC 挖掘）；选 SRC 时显示必做面、预算、是否允许 budget_stop、平台/项目。
- **任务列表**：mode 角标 + gaps 数。
- **任务详情**：新增「SRC」tab（`web/.../tasks/detail/_tabs/src-tab.tsx`）：
  - 覆盖矩阵（资产 × area，颜色区分终态/空格）；
  - gaps 清单（点击跳到资产/意图）；
  - 发现（severity/confidence/证据/报告状态）；
  - 提交台账（状态机操作）；
  - 导出按钮（md/docx/zip）。
- **全局**：P3 加「SRC 提交」看板。
- **设置**：P3 加平台/项目规则页。

前端是静态导出，改完必须 `npm run build:static` 并重建二进制（或 dev 模式）。

---

## 10. 分阶段实施

### 10.1 阶段总览

| 阶段 | 内容 | 验收 | 估算 |
|---|---|---|---|
| **P0 模式骨架** | `tasks.mode/mode_doc`、创建/传播/DTO、planner/worker 提示词覆盖、创建页选择、`src_state`（先用现有 `coverage_matrix` 推导） | 同一目标，SRC 任务的 planner 开始按“资产×面”派意图 | 3–5 天 |
| **P1 覆盖矩阵** | `src_cells` 表、`src_mark_cell`、`src_finish_check`、`src_gaps` 注入、engine 收尾门、任务详情 SRC tab（矩阵部分） | 有空格的任务不能 done；`src_state` 能列出待补格 | 5–7 天 |
| **P2 交付闭环** | `src_submissions`、`src_finding_meta`、报告正文、合规检查、跨任务去重、批量包扩展 | 一条 finding 走完复核→正文→报告→台账→zip；缺证据被拒 | 5–7 天 |
| **P3 平台面** | `src_programs`、范围/规则/速率/时间窗、SRC 看板、评测 | 选项目自动灌范围与策略；提交看板可用 | 4–6 天 |

**合计 3–4 周**（单人，熟悉代码的前提下）。

### 10.2 文件级改动清单

**数据层**

- `db/schema.sql`：`tasks.mode/mode_doc`、`src_cells`、`src_submissions`、`src_finding_meta`（P2）、`src_programs`（P3）。
- `db/tasks.go`：`Task` 字段、`taskCols`、`scanTask`、`CreateTaskWithOptions`。
- 新 `db/src_cells.go`：CRUD、`ListCells`、`Gaps` 查询。
- 新 `db/src_submissions.go`：状态机、按指纹查重。
- `db/task_archives_restore.go`：归档/恢复列清单。

**模式/服务层**

- 新 `srcmode/` 包（**不要 import `server`**，避免循环依赖；任务字段由调用方传入）：
  - `mode.go`：`ModeDoc`、`ParseDoc`、`Resolve`、`RequiredAreas`、`PromptFor(role)`；
  - `taxonomy.go`：area/item 清单与资产类型映射；
  - `gate.go`：`CheckFinish`；
  - `compliance.go`：证据校验；
  - `report.go`：`BuildSrcInput`（读 submission + finding）。
- `server/tasks_api.go`：`createTask` 接 mode；新 `getTaskSrc`/`markCell`/`forceFinish`。
- `server/llm_api.go`：`createTaskReq.Mode`。
- `server/dto.go`：`TaskDTO`。
- `server/manager.go`：`Task`、`taskFromPG`。
- `server/task_llm.go`：`agentsForTask` 里 `pl.SetMode/wk.SetMode/main.SetMode`。
- `server/engine.go`：`met` 分支接 `CheckFinish` + 防循环。
- `server/policy_compile.go`：SRC 模式的策略下限（可在 `newTaskPolicy` 里合并 mode 约束）。
- `server/src_export.go` / `src_batch.go`：正文来源、合规门、台账、批量包。
- `server/routes_tasks.go` / 新 `routes_src.go`：注册 `/src/*`。

**Agent 层**

- `agent/planner.go`：`plannerSystem` 加 mode；`Planner` 字段/`SetMode`。
- `agent/worker.go`：`workerSystem` 加 mode；`Worker` 字段/`SetMode`。
- `agent/mainagent.go`：`MainAgent` 字段/`SetMode`。
- `agent/tools.go`：`ToolSet.mode`、`SetMode`；`PlannerTools/WorkerTools` 按 mode 追加 `src_*`；`graphOverviewData` 注入 `src` 块。
- `agent/tools_insert.go`：`coverage_matrix` 在 SRC 模式下补 `src_cells` 覆盖层（或单独 `src_state`）。
- `agent/promptcatalog.go`：`src` 覆盖段常量（planner/worker/reporter）。

**前端**

- `web/src/lib/types.ts`：`Task.mode`、`SrcCell`、`SrcSubmission`。
- `web/src/lib/api.ts`：`taskSrc`、`markCell`、`submissions` 等。
- `web/.../create-task-sheet.tsx`：模式选择。
- `web/.../tasks/detail/page.tsx`：SRC tab。
- 新 `web/.../tasks/detail/_tabs/src-tab.tsx`。
- `web/.../tasks/page.tsx` + 列表组件：mode 角标/gaps。
- P3：`web/.../src/page.tsx` 提交看板。

### 10.3 P0 的最小验证

只加 `tasks.mode/mode_doc` 两列，**不建 `src_cells` 等新表**，用现有 `coverage_matrix` 推导：

- 创建 `mode=src` 任务；
- planner 提示词改成“先读 coverage_matrix，按未触及的面派意图”；
- engine 在 `met` 时调用一个临时 `CheckFinish`：若 `coverage_matrix` 里还有必做面缺失 → 拒绝 done；
- 任务详情先不做 UI，用 `GET /api/tasks/{id}/src` 返回 JSON。

这版能在一周内验证“planner 是否真的按补格思考”，再决定是否投入 P1。

### 10.4 评测

- 选 1–2 个授权靶场/历史目标，跑 `mode=""` 与 `mode=src` 对照；
- 指标：覆盖格完成率、有效发现数、重复率、报告可提交率、token/时长；
- 人工抽检 10 条 finding 的证据与报告；
- 回归：`mode=""` 任务的行为、现有 acceptance 全绿。

### 10.5 一周最小版（如果只想先试水）

只做：

1. `tasks.mode`（不落 mode_doc，默认值写死）；
2. planner/worker 提示词覆盖；
3. `src_state` 用 `coverage_matrix` 推导 gaps；
4. engine 收尾门（缺面就拒绝 done）；
5. 创建页加一个 checkbox。

不建 `src_cells`、不做报告正文、不做台账。**能验证方向，但不能交付。**

---

## 11. 测试计划

### 11.1 单元测试（`srcmode` 包）

- `Resolve`：未知 mode/未知 area/负数预算 → 拒绝；默认值正确；
- `RequiredAreas`：各资产类型映射；
- `CheckFinish`：
  - 无资产 → 空 gaps；
  - 有资产无格 → blank；
  - `na`/`budget_stop` 无 reason → missing_reason；
  - `tested_clear` 无证据 → missing_evidence；
  - `tested_found` 无 finding → missing_finding；
  - medium+ 无报告 → report_missing；
  - 同指纹 → duplicate；
  - `allow_budget_stop` 开关；
- `CheckCompliance`：Content-Length、请求 ID 重复、chunked 跳过、scope 外；
- `BuildSrcInput`：submission 优先、缺正文拒绝、占位符检测；
- 指纹：默认端口/大小写/尾斜杠/query 键序。

### 11.2 集成测试（Postgres）

- 创建 `mode=src` 任务 → `mode_doc` 快照正确；
- `src_mark_cell` 幂等 upsert；终态校验；
- engine `met` → 有 gaps 不落 done；补齐后落 done；
- 连续拒绝 3 次 → `policy_blocked`；
- `src_prepare_submission` → 行 + 报告 + 合规结果；
- `src_batch` zip 文件清单/内容；
- 归档/恢复后 mode 保留；
- `mode=""` 回归。

### 11.3 验收场景

1. 建 SRC 任务，planner 第一轮派出的意图带 `asset_id + area`；
2. worker 结束后 `src_state` 的 gaps 减少；
3. 有空格的站，`prove_goal` 后任务不 done，界面显示待补格；
4. 补齐后任务 done；
5. 一条 finding 的报告八段完整，docx 可打开；
6. 缺整包的 finding 被 `src_prepare_submission` 拒绝；
7. 同指纹重复被标 duplicate，提交前需人工确认；
8. `mode=""` 任务行为不变。

---

## 12. 风险与取舍

| 风险 | 影响 | 对策 |
|---|---|---|
| planner 目标函数难改 | 提示词改了仍在深挖 | P0 就用 engine 门 + gaps 注入；评测对照 |
| 收尾门死循环 | planner 反复 prove_goal 被拒 | 同 gaps 只注入一次；连续 3 次转 `policy_blocked`；人工放行 |
| 覆盖表膨胀 | 资产 × 面 × item 行数大 | 只存终态，不预填；空格靠计算；分页/索引 |
| 派生矩阵与显式格打架 | 两个“已覆盖”口径 | `coverage_matrix` 只作意图统计；SRC 判定只认 `src_cells` |
| 预算被单资产吃掉 | 其它站没测 | `MaxIntentsPerAsset` + 优先级公式 |
| 速率/时间窗无硬执行 | 平台封号 | 把规则编译成任务级拦截规则（禁 `-t 100`/`--flood` 等）；P3 加 `src_http` 限速包装 |
| 报告正文质量 | 交付被拒 | reporter 提示词 + 正文完整性门 + 人工抽检 |
| 去重误判 | 把两个洞当一个 | 指纹只由类型+端点算；命中只提示，最终由人确认 |
| 上游合并冲突 | 维护成本 | 新逻辑放 `srcmode/`；核心只加列/分支；不动双图 |
| 成本上升 | SRC 广度更烧 token | 规划用便宜模型/独立 profile；限制每轮意图数；冷压缩 |
| AGPL | 分发/托管义务 | 自用无碍；对外分发或托管需按 AGPL 提供源码 |
| 数据敏感 | 报告含目标数据 | 沿用现有保留/清理策略；报告目录权限收紧 |

---

## 13. 附录

### A. 完整 DDL（P0–P2）

```sql
-- P0
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS mode_doc JSONB NOT NULL DEFAULT '{}'::jsonb;
CREATE INDEX IF NOT EXISTS idx_tasks_mode ON tasks(mode) WHERE mode <> '';

-- P1
CREATE TABLE IF NOT EXISTS src_cells (
    id               BIGSERIAL PRIMARY KEY,
    task_id          BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    asset_id         BIGINT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    area             TEXT NOT NULL,
    item             TEXT NOT NULL DEFAULT '*',
    state            TEXT NOT NULL CHECK (state IN ('tested_found','tested_clear','na','budget_stop')),
    reason           TEXT NOT NULL DEFAULT '',
    evidence_node_id BIGINT REFERENCES exploration_nodes(id) ON DELETE SET NULL,
    finding_id       BIGINT REFERENCES findings(id) ON DELETE SET NULL,
    attempts         INT NOT NULL DEFAULT 0,
    updated_by       TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, asset_id, area, item)
);
CREATE INDEX IF NOT EXISTS idx_src_cells_task ON src_cells(task_id, asset_id);
CREATE INDEX IF NOT EXISTS idx_src_cells_state ON src_cells(task_id, state);

-- P2
CREATE TABLE IF NOT EXISTS src_finding_meta (
    finding_id  BIGINT PRIMARY KEY REFERENCES findings(id) ON DELETE CASCADE,
    fingerprint TEXT NOT NULL DEFAULT '',
    compliance  JSONB NOT NULL DEFAULT '{}'::jsonb,
    report_ready BOOLEAN NOT NULL DEFAULT false,
    dedupe_of   BIGINT REFERENCES findings(id) ON DELETE SET NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_src_finding_meta_fp ON src_finding_meta(fingerprint);

CREATE TABLE IF NOT EXISTS src_submissions (
    id             BIGSERIAL PRIMARY KEY,
    task_id        BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    finding_id     BIGINT NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    platform       TEXT NOT NULL DEFAULT '',
    external_id    TEXT NOT NULL DEFAULT '',
    title          TEXT NOT NULL,
    severity       TEXT NOT NULL,
    platform_level TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'draft'
                     CHECK (status IN ('draft','ready','submitted','accepted','duplicate','rejected','paid','withdrawn')),
    fingerprint    TEXT NOT NULL DEFAULT '',
    report_md      TEXT NOT NULL DEFAULT '',
    report_path    TEXT NOT NULL DEFAULT '',
    submitted_at   TIMESTAMPTZ,
    verdict_at     TIMESTAMPTZ,
    reward         TEXT NOT NULL DEFAULT '',
    note           TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_src_submission_finding_platform
    ON src_submissions(finding_id, platform);
CREATE INDEX IF NOT EXISTS idx_src_submissions_status ON src_submissions(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_src_submissions_fingerprint ON src_submissions(fingerprint);

-- P3
CREATE TABLE IF NOT EXISTS src_programs (
    id           BIGSERIAL PRIMARY KEY,
    company_id   BIGINT REFERENCES companies(id) ON DELETE SET NULL,
    name         TEXT NOT NULL,
    platform     TEXT NOT NULL DEFAULT '',
    scope_note   TEXT NOT NULL DEFAULT '',
    rules        JSONB NOT NULL DEFAULT '{}'::jsonb,
    rate_limit   INT NOT NULL DEFAULT 0,
    time_window  TEXT NOT NULL DEFAULT '',
    severity_map JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS src_program_id BIGINT REFERENCES src_programs(id) ON DELETE SET NULL;
ALTER TABLE src_submissions ADD COLUMN IF NOT EXISTS program_id BIGINT REFERENCES src_programs(id) ON DELETE SET NULL;
```

### B. 必做面配置示例

```json
{
  "mode": "src",
  "doc_version": 1,
  "allow_budget_stop": false,
  "max_attempts_per_cell": 2,
  "max_intents_per_asset": 12,
  "required_areas": {
    "app": ["unauth","authz","injection","xss","paths","session","upload","logic"],
    "service": ["unauth","authz","injection","xss","paths","session","upload","logic"],
    "endpoint": ["unauth","authz","injection","ssrf","logic"],
    "ip": ["host","cred"],
    "root_domain": ["takeover","host","paths"],
    "subdomain": ["takeover","host","paths"]
  },
  "hard_items": ["paths/enum-200"]
}
```

### C. `src_mark_cell` 参数

```json
{
  "asset_id": 12,
  "area": "authz",
  "item": "*",
  "state": "tested_found",
  "reason": "跨租户读取他人订单，已复现",
  "evidence_node_id": 345,
  "finding_id": 67
}
```

约束：

- `state=tested_found` 必填 `finding_id`；
- `state=tested_clear` 至少给 `evidence_node_id` 或 `reason`；
- `state=na|budget_stop` 必填 `reason`；
- `area` 必须在 taxonomy 内；`item` 允许 `*` 或 taxonomy 子项；
- 只有任务范围内资产可写；跨任务拒绝。

### D. 收尾门伪代码

```go
// d 是 db.DB；doc 是任务创建时的 ModeDoc 快照。
func CheckFinish(ctx context.Context, d *db.DB, taskID int64, doc ModeDoc) (bool, []Gap, int) {
    assets := InScopeHostAssets(d, taskID)
    gaps := []Gap{}
    for _, a := range assets {
        for _, area := range RequiredAreas(doc, a.Type) {
            cell := Cells.Get(d, taskID, a.ID, area, "*")
            if cell == nil {
                // 若该 area 下所有 item 都有终态，也算过
                if !allItemsTerminal(d, taskID, a.ID, area) {
                    gaps = append(gaps, Gap{a.ID, a.Label, area, "*", "blank"})
                }
                continue
            }
            switch cell.State {
            case "na", "budget_stop":
                if cell.Reason == "" { gaps = append(gaps, ..., "missing_reason") }
            case "tested_clear":
                if cell.EvidenceNodeID == 0 && cell.Reason == "" { gaps = append(gaps, ..., "missing_evidence") }
            case "tested_found":
                if cell.FindingID == 0 { gaps = append(gaps, ..., "missing_finding") }
            }
        }
    }
    for _, f := range MediumPlusFindings(d, taskID) {
        if !ReportReady(d, f.ID) { gaps = append(gaps, ..., "report_missing") }
    }
    for _, du := range UnresolvedDuplicates(d, taskID) {
        gaps = append(gaps, ..., "duplicate")
    }
    return len(gaps) == 0, head(gaps, 50), len(gaps)
}
```

### E. 与“外挂 DSH 插件”方案的关系

- 如果 BLADE 自己成为 SRC agent，DSH 插件只剩“遥控器/对话前端”的价值；
- 建议先把 BLADE 的 `src` 模式跑通，再决定要不要做薄连接器；
- 不要同时做两套，否则两边的覆盖/报告/去重口径会分叉。

---

## 14. 参考

- `report/src.go`：SRC 8 段版式
- `server/src_export.go` / `server/src_batch.go`：证据门与批量交付
- `report/fingerprint.go`：指纹
- `policy/policy.go`：策略与默认 SRC 姿态
- `db/task_scope.go`：`CoverageMatrix`
- `agent/promptcatalog.go`：planner/worker/reporter 提示词
- `server/engine.go`：planner 收尾分支
- `docs/漏洞流量证据.md`：流量证据约定
