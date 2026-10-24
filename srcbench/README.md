# SRC 金标靶场（srcbench/）

目的：给 `mode=src` 一个**可复现、可打分**的基准——不是"感觉变好了"，而是
precision / recall / 等级一致 / 覆盖率 / token 成本。

## 组成

| 文件 | 作用 |
|---|---|
| `target/` | 本地故意有漏洞的 Web 靶场（IDOR / 未授权 / 反射 XSS / SSRF / 路径穿越 / 开放重定向 / 缓存投毒）。只监听本地，SSRF 端点只代取 loopback。 |
| `targets.json` | 金标清单：目标、预算、每条金标的漏洞类型 + 端点 + 期望等级。 |
| `score/` | 打分器：发现列表 vs 金标，算 TP/FP/FN、P/R/F1、等级一致、覆盖率、token/命中。 |

## 跑一次基准

1. **启动靶场**（本机、无真实数据，天然在授权范围内）：

   ```bash
   go run ./srcbench/target -addr 127.0.0.1:18080
   ```

2. **在 BLADEPRO 建一个 SRC 任务**（UI 或 API）：
   - 目标/范围：`http://127.0.0.1:18080`（只填这一个资产）；
   - 模式：`src`；
   - 预算建议：`timeout_seconds=1800`、`max_intents_total=80`、`max_intents_per_asset=12`
     （与 `targets.json` 的 `budget` 对齐）；
   - 不绑平台项目（或绑一个 `check_tier=full` 的空项目）。

3. **导出发现**（任务结束后）：

   ```bash
   # 发现页 API 的形状 {items:[...]}，scorer 直接认
   curl -s -H "Authorization: Bearer $TOKEN" \
     "http://127.0.0.1:9898/api/exploration/findings?task=$TASK_ID&page=1&limit=200" \
     > /tmp/findings.json

   # 收尾覆盖率（可选）：/src 快照里的 summary 对象
   curl -s -H "Authorization: Bearer $TOKEN" \
     "http://127.0.0.1:9898/api/tasks/$TASK_ID/src" | jq '.summary' > /tmp/src.json
   ```

4. **打分**：

   ```bash
   go run ./srcbench/score \
     --target local-vuln \
     --golden srcbench/targets.json \
     --findings /tmp/findings.json \
     --src /tmp/src.json \
     --tokens "$(curl -s -H "Authorization: Bearer $TOKEN" \
        "http://127.0.0.1:9898/api/tasks/$TASK_ID" | jq -r '.tokens // 0')"
   ```

   输出 Markdown（加 `--json` 输出机器可读结果）。

## 指标怎么读

- **TP / FP / FN**：金标与发现按「受控漏洞类型 + 归一化端点（对象 ID 已折叠）」匹配；
  同一金标只匹配一条发现，未匹配的发现算 FP，未匹配的金标算漏报。
- 状态为 `false_positive` / `duplicate` / `retracted` / `ruled_out` / `ignored` /
  `risk_accepted` 的发现不计入（它们不是待提交件）。
- **等级一致**：命中的发现 `severity` 与金标一致的比例（只提示，不参与 P/R）。
- **覆盖格完成率 / 收尾闸完成率**：来自 `/api/tasks/{id}/src` 的 `summary`。
- **Token / 每条命中**：任务 token 总量 ÷ TP；SRC 是广度换深度，关注这个数的趋势。

## 扩展

- 新增靶标：在 `targets.json` 里加一个 target（`base_url` + `golden`），并在
  `target/` 或独立容器里提供对应应用；金标端点写相对路径即可（`GET /path?key`）。
- 真实靶场（Juice Shop / DVWA / vulhub）建议用 Docker 独立起，再按同样字段登记
  金标；**不要把生产站点写进 bench**。
- CI 不跑 LLM 基准（成本/不确定性），只跑 `go test ./srcbench/...`：
  靶场自检（每个金标漏洞真的存在）+ 打分器纯逻辑测试。
