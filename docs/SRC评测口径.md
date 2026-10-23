# SRC 模式评测口径

> 目的：回答"`mode=src` 到底比普通渗透好在哪"，以及回归时"这次改动有没有把 SRC 做坏"。
> 方案文档 §10.4 的口径在这里落成可执行的清单；数字来自 `GET /api/src/stats`、
> `GET /api/tasks/{id}/src` 与提交台账，不需要额外埋点。

## 一、对照怎么跑

同一目标、同一授权范围、同一 LLM 配置，各跑一次：

| 组 | 创建方式 | 说明 |
|---|---|---|
| A（基线） | `mode=""` | 普通渗透：证明目标、打深、拿据点 |
| B（SRC） | `mode="src"` | 按「站点 × 风险面」补格，收尾门判完成 |

要点：

- 两组**必须用同一份任务范围**（同样的 `task_scope`），否则覆盖分母不同、数字不可比；
- 时间预算拉到同一条线（`timeout_seconds` 相同），否则 SRC 的广度优势会被时长掩盖；
- 靶场或历史目标优先，别拿生产站点做对照；
- 各跑 2–3 次取中位数：单次结果受模型随机性影响很大。

## 二、指标（看什么）

| 指标 | 口径 | 从哪取 |
|---|---|---|
| 覆盖格完成率 | `cells_terminal / cells_total` | `/api/tasks/{id}/src` → `summary` |
| 实测率 | `cells_measured / cells_total`（只算 tested_*） | 同上 |
| 缺口清零时长 | 首次 `finishable=true` 的时间 − `first_run_at` | `summary` + 任务 DTO |
| 有效发现数 | 台账里 `severity in (critical,high,medium)` 且 `status in (ready,submitted,accepted,paid)` | `/api/src/submissions` |
| 报告可提交率 | `findings_report_ready / findings_medium_plus` | `/api/src/stats` |
| 重复率 | 台账标 `duplicate` 数 ÷ 台账总数 | `/api/src/stats` |
| 交付节奏 | `submitted_at − created_at` 的中位数 | 台账 |
| 成本 | 任务 token 总量 / 有效发现数 | 任务 DTO `tokens` |
| 误报率 | 人工抽检 10 条 finding 中不成立的比例 | 人工 |

判读建议：

- **B 的覆盖格完成率应显著高于 A**（这是模式的核心假设）。若没有差异，先查
  `graph_overview.src` 是否真的进了 planner 的提示词、收尾门是否在拦 done；
- **B 的"有效发现数 ÷ token"不应低于 A 太多**：SRC 是广度换深度，单条成本会上升，
  但"每条可提交件的成本"应该可接受；
- **报告可提交率 < 80%** 说明交付门在起作用但 agent 没跟上——看 `src_state` 的
  `delivery` 缺口里是 `report_missing`（没写正文）还是 `duplicate`（没定案）；
- **重复率突然变高**：查是否同一目标被多任务重复挖（指纹跨任务命中），这时应该合并任务，
  而不是让两个任务各交一份。

## 三、回归（每次改动后）

1. `go build ./... && go vet ./... && staticcheck ./...`；
2. `gofmt -l`（CI 同一口径：只查已跟踪的 `.go`）；
3. `go test ./...`：与改动前的失败清单**逐条比对**（本机无 PostgreSQL 时，DB 集成测试会
   skip，这是既有约定，不是通过）；
4. `mode=""` 的任务行为必须与改动前一致：新建一个普通任务，跑一轮规划，确认
   `graph_overview` 里没有 `src` 块、工具列表里没有 `src_*`；
5. 前端 `npx tsc --noEmit` + `next build`（静态导出）。

## 四、已知不可自动评测的部分

- **证据是否真的来自目标**：合规检查只抓"包长对不上 / 请求 id 重复 / 越界 / 空响应"这些
  机器可见的形状，抄得像的整包仍要靠人抽检；
- **危害描述是否夸大**：八段正文的"危害"只能人工判；
- **平台是否收**：不同平台的受理尺度不同，最终以平台结论回填台账
  （`accepted/rejected/duplicate`）为准——这也是台账存在的意义。
