# src-dig 范围约束 × BLADEPRO 融合方案

> 版本：v1.0（2026-10-23）
> 依据：`~/.claude/skills/src-dig/`（`SKILL.md`、`rules/` 13 篇、`工具/` 校验器、`知识库/bytesrc-rules.md`）
> 适用：BLADEPRO 的 `mode="src"`；`mode=""` 的普通渗透一字节不变
> 定位：**实现契约**。下面每一条都写了"落在哪个文件、什么状态"，不是理念清单。F1–F4 已落地（2026-10-23）。

---

## 0. 一句话

技能负责回答三个问题——**测够没有、能不能交、有没有越线**；BLADEPRO 已有"补格 + 收尾门 + 交付台账"，
本方案把技能里**可判定的部分**编译成工程约束，不可判定的（定级尺度、平台受理、证据真伪）明确交给人：

| 谁 | 管什么 |
|---|---|
| 技能（src-dig） | 目标函数、范围口径、红线、交付版式 |
| BLADEPRO（机器） | 把口径变成格子、数字门槛、命令级硬规则、提交前校验 |
| 人 | 定级、平台表单、截图取舍、放行 |

## 1. 依据：技能里到底有哪些"范围约束"

| 文件 | 管什么 | 融合成 |
|---|---|---|
| `SKILL.md` §安全红线 1–4（L23–33） | 越权最小伤害、禁登出/吊销、CORS 不挖、平台红线 | 内建红线段 + 命令门规则 |
| `SKILL.md` §自由跳节奏 / `dig-scope` §1.0.1 | 一种子闭环、禁停工问句 | `focus_asset` 排序 + 提示词 |
| `rules/site-close-checklist.md` §1–§6 | **收尾闸 8 项**（JS 抽全/路径≥200/非404复测/方法矩阵/参数变体/防护对照/判死三条件/截图） | `src_checks` 数据模型 + 门 |
| `rules/site-close-checklist.md` §0.1 | 平台档豁免（只豁免批量枚举，不豁免测试深度） | `check_tier=platform` |
| `rules/dig-scope-workflow.md` §4.2 / §4.2.1 | 类型矩阵、四件套（注入/SSRF/XSS/RCE）不得空窗 | 已有必做面 + 排序与告警 |
| `rules/dig-scope-workflow.md` §4.2.2 / §4.2.3 | 认证口、对象图换 id、别人账号只探不发 | 红线段 + session/authz 面 items |
| `rules/src-value-hunting.md` §1.1 / §2.2 / §3 | 价值排序、类型矩阵、挖洞偏好禁令 | 面权重 + 提示词 |
| `rules/vuln-report-format.md` 一 / 〇 | 8 块版式、标题规则、提交块 | `SrcMarkdown` 增强 + `ValidateTitle` + `SubmitInfo` |
| `rules/report-polish.md` 第三步 | 提交前七项校验（含报文自洽） | `CheckCompliance` 修正 + `CheckReportMarkdown` |
| `工具/report_validate.py`（L29–35、L113–144） | 唯一标识头清单、Content-Length 判据 | 同上（**修正现有头清单**） |
| `工具/packet_rules.py` L18–28 | 超长报文 100k + 5 个省略标记 | 渲染器自动缩减 |
| `工具/站点覆盖矩阵模板.md` | 10 类矩阵 + 注入/SSRF/RCE 参数表 | 面 items 扩充 |
| `知识库/bytesrc-rules.md` 第二章（L33–72） | 平台红线样例（禁扫描器/禁内网/≤5 组/AK·SK 只提交） | `program.rules` 扩字段 + 模板 |
| `rules/cors-vuln-report-priority.md` | CORS 永久不挖 | 显式排除 |
| `rules/anti-over-moralization.md` | 不盘问、不说教、不停工 | 提示词纪律 |
| `rules/desktop-task-folder.md` | 任务夹与产物结构 | 已有 runDir/artifacts，不重复造 |

## 2. 三条原则

1. **可判定的进机器，判不了的写清边界**。
   能写成"数字 / 布尔 / 白名单码"的，进数据模型与门；写不成的（"危害有没有夸大"）留在报告与人工复核，
   并在文档里点名，不用一句"已覆盖"糊过去。
2. **只增不改，默认更严、都能关**。
   `mode=""` 行为不变；新增约束都有开关（`check_tier=off`、`skip_check_gate`、`skip_report_gate`、
   人工 `force-finish`），因为真实项目里"这次不测这块"是常态，堵死等于逼人绕过工具。
3. **快照语义**。平台项目规则在**创建任务时**编译进 `mode_doc` 与探索策略；之后改项目不影响在跑的任务
   （与 P20 策略快照、P3 program 一致）。

## 3. 约束 → 机制映射

状态列：**已实现** = 当前代码就用；`Fx` = 在哪个阶段落的（F1–F4 均已落地，见 §11）。

### 3.1 覆盖：测够没有

| 技能约束 | 现状 | 融合机制 | 状态 |
|---|---|---|---|
| 10 类必做矩阵（未授权/越权/注入/SSRF/XSS/RCE/敏感路径/认证会话/上传穿越/逻辑提权） | `srcmode/taxonomy.go` 13 area，按资产类型给必做面；`src_cells` 四终态 | 保留；补齐 items（ES DSL / HQL·SpEL / XPath·LDAP / OOB / 存储型 XSS / 模板·表达式·WS 执行 / SCM·Actuator / reset·rebind·2FA / 越权`对象图` / 逻辑`privilege`·`money`） | 已实现（部分 items）+ F1 补 items |
| **收尾闸 8 项**（空格的第二层） | 无 | 新表 `src_checks`（任务 × 资产 × 收尾项），只存终态；门里多一类 `missing_check` 缺口 | **已实现（F1）** |
| N/A 只允许"无入口"，且必须写原因 | `na` 只校验 reason 非空 | `src_cells.na_code` + 按 area 的白名单（§5.2）；非法码直接拒绝 | **已实现（F1）** |
| 四件套不得连日空窗 | 四件套已是必做面 → blank 本身是缺口 | 面权重已让注入/RCE 排在前面；补"四件套全 N/A"告警 | 已实现（排序）+ F4 告警 |
| 平台档豁免（手工等价物） | 只有限速/时间窗 | `check_tier=full|platform`；platform 档把 `paths≥200` 换成 `manual_paths≥1`、方法矩阵只要求状态变更端点 | **已实现（F2）** |
| CORS 永久不挖 | 无显式表达 | 不作为 area（矩阵里不出现）；提示词"勿开 CORS"，`cors` 报告一律 N/A | **已实现（F1）** |

### 3.2 红线：有没有越线

| 技能约束 | 现状 | 融合机制 | 状态 |
|---|---|---|---|
| 禁登出/注销/吊销（`/logout` `/signout` `/revoke`） | 仅文档约定 | **内建命令级规则**（策略 `Custom` + `Layer=command`），所有 `mode=src` 任务自动带 | **已实现（F2）** |
| 别人账号只探不发 / 改密改绑过即改回 | 无 | worker/planner 红线段（8 条），并要求报告判读用语（"服务端接受了把 uid 换成 X 的请求"） | **已实现（F2）** |
| 券/库存/金额只改小/负/小数，禁真实资损 | 策略已禁 `bruteforce`/`dos` 等 | 红线段 +（可选）项目自定义命令规则 | 部分已实现 + F2 |
| 禁批量、禁内网横向、禁社工 | 策略词表（bruteforce/dos/social_engineering/persistence） | 保留；平台项目可加 `no_automation` 规则 | 部分已实现 + F2 |
| 平台红线：禁扫描器批量、越权≤5 组、AK/SK 只提交 | `program.rules` 有禁用手法/写/命令规则 | 新增 `no_automation`、`max_records_read`、`redlines[]` | **已实现（F2）** |

### 3.3 交付：能不能交

| 技能约束 | 现状 | 融合机制 | 状态 |
|---|---|---|---|
| 8 块版式 + 全角＃ + 文件名=标题 | `report.SrcMarkdown` 已实现 | 保留；补提交块 / CRLF / 严格版式 | 已实现 + F3 |
| 标题规则（汉字≤25/字母≤50/总长≤100、禁 URL/编号/路径开头/METHOD 形式） | 无 | `srcmode.ValidateTitle`（移植 `report_validate.validate_title`） | **已实现（F3）** |
| 唯一标识头跨步骤不得重复 | 已实现，但头清单**误收** `traceparent`/`b3`/`amzn`（链级复用会误报） | 收紧为 `x-tt-logid / x-tt-trace-id / x-request-id / x-trace-id / request-id / x-traceid / x-log-id` | **已实现（F3，修正）** |
| Content-Length 不得大于正文实际 | 已实现（声明>实际即报） | 补容差（≤实际+换行数）与省略标记豁免 | **已实现（F3）** |
| 超长签名头截断事故 | 无 | `truncated_header`：签名头（`x-medusa/x-argus/x-tt-token/x-gorgon/x-tt-logid`）值 <64 或含 `.../…/(截断)` | **已实现（F3）** |
| 超长报文 100k + 省略标记 | 无 | 渲染时自动中段缩减并插入 `[中间无关请求内容已省略]` | **已实现（F3）** |
| 报文结构（请求行+Host / 状态行 / 禁整行省略号） | 无 | `srcmode.CheckReportMarkdown`（对成品再查一遍） | **已实现（F3）** |
| 提交块（平台/等级/测试源 IP/测试账号） | 台账有平台与映射等级 | `SubmitInfo` 从 `mode_doc` 生成，置于正文前 | **已实现（F3）** |
| 定级口径（严重/高危/中危） | `findings.severity` | **不自动判定**：报告与台账引用该等级；口径写进提示词，最终以平台结论回填 | 明确不做自动 |

### 3.4 节奏：别停、别偏科

| 技能约束 | 现状 | 融合机制 | 状态 |
|---|---|---|---|
| 一种子闭环（一个 host 挖完再换） | 全局按缺口优先级（广度） | `Report.focus_asset` + 提示词"先清零当前资产"；**只改排序不改门** | **已实现（F4）** |
| 禁停工问句（"要不要继续"） | planner 无此行为，但可能空转 | 收尾门已在工程上兜住；提示词补一句 | **已实现（F4）** |
| 中危同一对象先升链 | 缺口按权重排序 | 保留；报告备注提示 | 已实现（部分） |
| 任务夹 / 产物结构 | `tasks/<id>/i<intent>` runDir + artifacts | 不重复造；报告落 `报告/` 由导出面保证 | 已实现 |

## 4. 数据模型

### 4.1 新表：`src_checks`（收尾闸）

```sql
CREATE TABLE IF NOT EXISTS src_checks (
    id               BIGSERIAL PRIMARY KEY,
    task_id          BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    asset_id         BIGINT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    check_key        TEXT NOT NULL,
    state            TEXT NOT NULL CHECK (state IN ('done','na','budget_stop')),
    na_code          TEXT NOT NULL DEFAULT '',
    reason           TEXT NOT NULL DEFAULT '',
    evidence_node_id BIGINT REFERENCES exploration_nodes(id) ON DELETE SET NULL,
    -- metrics 是"数字"，不是形容词：{js_external:3,paths:214,non404:6,retested:6,...}
    metrics          JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_by       TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, asset_id, check_key)
);
CREATE INDEX IF NOT EXISTS idx_src_checks_task ON src_checks(task_id, asset_id);
```

### 4.2 扩列：`src_cells.na_code`

```sql
ALTER TABLE src_cells ADD COLUMN IF NOT EXISTS na_code TEXT NOT NULL DEFAULT '';
```

### 4.3 `mode_doc` 新字段（快照，创建时定死）

```jsonc
{
  "check_tier": "full",                 // full | platform | off
  "check_thresholds": {"paths": 200},   // 覆盖内建门槛（按指标名）
  "skip_check_gate": false,             // 一刀关掉收尾闸（应急出口）
  "require_screenshots": false,          // 平台要求截图时 = true
  "strict_layout": false,                // 严格 8 块：备注不进正文
  "egress_ip": "", "test_accounts": "",  // 提交块字段
  "platform_redlines": ["越权读取不超过 5 组"]  // 平台原始红线，进提示词
}
```

### 4.4 `src_programs.rules` 新字段（JSONB，向后兼容）

```jsonc
{
  "check_tier": "platform",
  "check_thresholds": {"paths": 0, "manual_paths": 1},
  "no_automation": true,                 // 编译：禁 nuclei/awvs/nessus/masscan/nmap -p-/ffuf -w…
  "redlines": ["AK/SK 只提交不深挖", "禁内网渗透/横向"],
  "max_records_read": 5,
  "require_screenshots": true,
  "strict_layout": true,
  "egress_ip": "1.2.3.4",
  "test_accounts": "账号A / 账号B（越权类必填）"
}
```

## 5. 判定口径

### 5.1 收尾闸目录（8 项）

| key | 适用资产 | `done` 判据（full 档） | platform 档 | 允许 `na` | 权重 |
|---|---|---|---|---|---|
| `js_enum` | app/service/endpoint | `js_inline+js_external+js_chunks+sourcemap > 0`；或 `degraded=1` + reason | 同左（**不豁免**，见技能 §0.1(3)） | 否 | 30 |
| `paths_enum` | app/service | `paths ≥ 200` | `manual_paths ≥ 1`（JS/网关/回包手工列） | 否 | 25 |
| `non404_retest` | app/service | `retested ≥ 2`；或 `non404=0` | 同左 | 否 | 25 |
| `method_matrix` | app/service/endpoint | `methods ≥ 7` | `state_methods ≥ 1` | 否 | 20 |
| `param_variants` | app/service/endpoint | `variants ≥ 4` | 同左 | 是：`no_business_param` / `no_object` | 25 |
| `defense_contrast` | app/service | `contrasts ≥ 1` | 同左 | 是：`no_defense_claim` | 20 |
| `unreachable_proof` | ip / 根域 / 子域 | `external_exits≥2 && dns_resolvers≥2 && local_unreachable=1` | 同左 | 是：`reachable` | 15 |
| `screenshots` | app/service | 证据节点指向截图产物 | 同左 | 是：`platform_not_required` | 10 |

硬要求：**`done` 必须带 `evidence_node_id`**（技能："每一项都要有执行记录"）。
数字不达标时 `src_mark_check` 直接拒绝，并把"`paths=37 < 200`"原样回给模型。

### 5.2 N/A 理由码（`na_code`）

| code | 含义 | 允许的 area |
|---|---|---|
| `no_input` | 无任何 query/body 业务参 | injection / logic / ssrf / xss |
| `no_url_param` | 清单+抓包零 URL 拉取/代理字段 | ssrf |
| `no_reflect` / `no_writer` | 无回显 / 无用户可控写入点 | xss |
| `no_exec_sink` | 清单+抓包+回包都无执行 sink | rce |
| `no_upload_entry` | 无上传/下载口 | upload |
| `no_object` | 纯登录墙、抽不出对象 | authz / logic |
| `no_login_entry` | 清单无发会话/重置/改绑/换票口 | session |
| `no_business_param` | 无可换的过滤参数 | 收尾项 `param_variants` |
| `static_page` | 纯静态页 | 全部 |
| `platform_exempt` | 平台档案明确不测（须 program 提供该豁免） | 全部 |
| `out_of_scope` | 越界资产（已有语义） | 全部 |
| `reachable` | 资产可达，无需判死 | 收尾项 `unreachable_proof` |

### 5.3 门逻辑

```
done 之前：
  ① 面缺口：范围内资产 × 必做面（已有）
  ② 收尾缺口：资产 × 收尾项（新增 missing_check）
  ③ 交付缺口：medium+ 发现无齐备件 / 未定案重复（已有 report_missing / duplicate）
  → 任一未清零：拒绝 done，写时间线，注入下一轮（同一份缺口只注入一次）
  → 连续 3 次同一份缺口：policy_blocked 交人工（已有）
```

优先级：`资产价值 × 面权重`（面缺口） > `资产价值 × 项权重 / 2`（收尾缺口）——
先补面、再补闸；同一资产内按权重降序。

### 5.4 放行出口（都必须留痕）

| 出口 | 用了会怎样 |
|---|---|
| `src_mark_check(na, code, reason)` | 收尾项记 N/A，报告里可见 |
| `mode_doc.skip_check_gate=true` | 不生成收尾缺口（应急） |
| `mode_doc.check_tier=off` | 同上，语义更明确 |
| `POST /src/force-finish` | 人工放行，必填理由，进审计（已有） |

## 6. 交互面

| 面 | 增量 |
|---|---|
| agent 工具 | `src_mark_check(asset_id, check_key, state, na_code, reason, metrics{}, evidence_node_id)`；`src_state` 返回 `checks`；`src_prepare_submission` 回显标题/报文问题 |
| HTTP | `POST /api/tasks/{id}/src/checks`（人工点收尾项，同一套校验）；`GET /api/tasks/{id}/src` 增 `checks`、`suspicious_na`、`focus_asset` |
| 前端 | SRC 页签：每个资产下 8 个收尾 chip（done 绿 / na 灰 / 空 虚线），点击弹窗填 metrics 与证据节点；统计条加"收尾项 x/8" |
| 工作台 | 平台项目表单加 `check_tier / no_automation / redlines / egress_ip / test_accounts / strict_layout / require_screenshots` |
| 提示词 | planner：focus_asset、收尾项、禁停工、四件套优先；worker：红线 8 条、收尾项怎么算做完 |

## 7. 平台档与规则模板

| 维度 | full（默认，非平台系） | platform（平台档案档） |
|---|---|---|
| 路径枚举 | ≥200 条字典+扩展 | 手工列出 JS/网关/回包里的真实 path |
| 方法矩阵 | 每端点 7 方法 | 只对状态变更端点换方法 |
| 扫描器 | 允许（仍受策略禁用项约束） | `no_automation=true` 编译命令级禁止 |
| JS 抽全 / 参数变体 / 防护对照 | 必做 | **同样必做**（技能明文：豁免的只是批量流量，不是测试深度） |
| 越权读取量 | 最小证明 | `max_records_read`（如 5 组） |

模板见 [平台规则模板.md](平台规则模板.md)：字节系 / 京东系 / 通用三份可直接粘贴的 JSON（含规则页链接与建档日期，
**以当日规则页为准**）。

## 8. 分阶段与验收

| 阶段 | 内容 | 估时 |
|---|---|---|
| F1 | `src_checks`（表/目录/工具/门/API/UI）+ `na_code` | 3–4 天 |
| F2 | 内建红线命令规则 + 红线段 + program 新字段 + 模板 | 1.5–2 天 |
| F3 | 合规修正 + 标题规则 + 报文结构 + 超长包缩减 + 提交块 + CRLF/严格版式 | 2 天 |
| F4 | `focus_asset` + 四件套全 N/A 告警 + 提示词 | 0.5 天 |

**验收场景**（每阶段回归同一套）：

1. 只点面不点收尾项 → 不 done，缺口点名 `paths_enum`。
2. `paths_enum done` 但 `paths=37` → 工具拒绝并回显"37 < 200 或改平台档"。
3. `injection na` 不填 code → 拒绝；填 `no_input` 通过。
4. 平台档：`manual_paths=12` 过 `paths_enum`；`state_methods=2` 过 `method_matrix`。
5. 同一 `x-tt-logid` 贴两步 → 准备交付被拒；`traceparent` 重复**不**报。
6. 标题带完整 URL / 以 `/` 开头 / 含编号 → 被拒。
7. 绑定项目：导出的 md 带【提交信息】、CRLF、无备注区（strict）。
8. `mode=""` 任务：无 `src_*` 工具、无新增命令规则、行为与改造前逐条一致。

## 9. 不做 / 风险 / 回退

**不做（本轮）**

- **白盒 0day 流程**（`Phase 0–6`、semgrep 树、四级验证/confidence）：需要 `assets.type='repo'`、代码挂载、
  semgrep 执行与结果落库，是独立子系统。本仓不假装支持。
- **截图自动生成**：只做"是否有截图证据"的登记与门控，不渲染图片。
- **定级自动判定**：严重/高危/中危 的口径太依赖上下文，机器只搬运等级、不裁决。

**风险**

| 风险 | 对策 |
|---|---|
| 收尾闸比面更难，任务更容易卡住 | 平台档 + `skip_check_gate` + 人工放行；错误文案写清"差多少、怎么补" |
| metrics 由模型自报 | `done` 必须挂证据节点；文档写明"机器不校验 fact 内容"这条诚实边界 |
| 平台规则改版 | 模板注日期与链接；不 seed 进库 |
| 命令门误伤 | 只拦技能明文禁止的动作（如 `/logout`）；误报可在任务策略层看到 |
| 改动面大 | F1–F4 独立可交付；每阶段跑 `go build/vet/staticcheck/gofmt` + 前端 `tsc/next build` |

**回退**：全部新增列/表 `IF NOT EXISTS` + `DEFAULT`；行为回退只需 `check_tier=off` / `skip_check_gate`，
不清数据、不删列。

## 10. 附录：关键原文（摘录）

- 收尾闸铁律："**缺项 = 没测，不是「测了没有」**；只凭「看到验证码」「返回 401」就下防护结论 = 违规。"（`site-close-checklist.md` L8–9）
- 平台档："豁免的是**会产生批量流量的枚举手段**，不是**测试深度**。"（同上 L35）
- 四件套："连续多轮/跨自然日只有未授权读、四件套没对有差分面的真实参数发过 payload → 违规偏科。"（`dig-scope-workflow.md` L714）
- 别人账号："**只到提交出去、看服务端回包接不接受**为止。不在第三方账号上完成最终写入。"（同上 L739–741）
- 报文自洽："唯一标识头跨步骤不得重复；`Content-Length` 不得大于正文实际字节数。"（`vuln-report-format.md` §一）
- 报文逐字节："每一份报文都必须是当场抓下来的真包，逐字节照抄——不许回忆、不许改写、不许拼凑。"（同上）

## 11. 落地记录（2026-10-23）

F1–F4 已实现并推送。这一节记"实际落了什么、和设计有什么出入"，供后来人对照。

### F1 覆盖语义（收尾闸 + N/A 码）

- 数据：`src_checks` 表、`src_cells.na_code`（`db/schema.sql`、`db/src_checks.go`、`db/src_cells.go`）。
- 判定：`srcmode/checks.go`（8 项目录、full/platform 两档门槛、`checkSatisfied` 数字判据）、
  `srcmode/na.go`（11 个理由码 + 按面白名单）。门里多一类 `missing_check` 缺口。
- 交互：工具 `src_mark_check`、`POST /api/tasks/{id}/src/checks`、SRC 页签「收尾闸」卡片（按资产 chips + 弹窗填数字/证据/理由码）。
- 与设计的出入：`done` 强依赖 `evidence_node_id`（设计里写的是"必须有"，落地时确认对 UI 也要生效，
  所以人在弹窗里同样要填节点 id）；`metrics` 用 `map[string]int` 而不是任意 JSON——只收数字，便于判门槛。

### F2 红线

- 内建会话红线编译成命令级策略规则（`srcmode/redlines.go` + `srcBuildinPolicyFloor`），
  所有 `mode=src` 任务自动带，不依赖 program。
- 最小伤害/平台红线走提示词（planner/worker 的 `redlineNote`），没有硬拦——它们的语义太细，
  硬拦会误伤（例如"改密后改回"本身就是允许的动作）。
- program 新字段：`check_tier / check_thresholds / no_automation / redlines / max_records_read /
  require_screenshots / strict_layout / egress_ip / test_accounts`，创建任务时编译进 `mode_doc`。
- 与设计的出入：`no_automation` 的扫描器规则锚定为"命令位置"（`^` 或 `;`/`&`/`|` 之后），
  避免 `grep nuclei readme.md` 这类误伤（单测固定了这条）。

### F3 交付校验

- 合规：唯一标识头收紧为 7 个按请求唯一的头（去掉 traceparent/b3/amzn）；新增 `truncated_header`。
- 标题：`ValidateTitle` 并入 `ValidateReportText`（长度/链接/路径开头/编号/METHOD 形式）。
- 成品：`CheckReportMarkdown` 在准备交付件时对渲染后的 markdown 再查一遍（Host、整行省略号、跨块重复 id、Content-Length）。
- 渲染：正文没内联时补 `[中间无关请求内容已省略]`；单条报文超 100k 自动中段缩减；
  `.md` 导出改 CRLF；严格版式下备注退出正文。
- 提交块：`srcmode.SubmitInfo` 从 `mode_doc` 生成（平台/项目/等级/测试源 IP/测试账号），置于正文前。

### F4 节奏

- `Report.focus_asset`（按资产聚合缺口权重）+ `suspicious_na`（四件套全 N/A 计数）；
  两者只影响排序/告警，不新增阻塞条件。
- planner/worker 覆盖段：先清零 focus_asset、四件套优先、禁"问用户要不要继续"。

### 还没做的（诚实边界）

- DB 集成测试仍未在真库跑过（本机无 PostgreSQL），`src_checks` 的 SQL 只经过编译期检查。
- 截图不生成，只登记；定级不自动判定（机器只搬运等级）。
- `max_records_read` 只在合规提示层，没有硬拦"第 6 组读取"——那需要流量级计数，超出本轮范围。