# 本部署环境（BLADEPRO · Windows 原生）

> 技能根：`${SKILL_DIR}/src-recon`（默认 `D:\Code\BLADEPRO\skills`）。加载技能后 `${SKILL_DIR}` 才是绝对路径；
> 常驻注入（always）时不会替换，所以本技能一律按需调用 `Skill(src-recon)` 加载。

## 1. MCP

| 名称 | 地址 | 说明 |
|---|---|---|
| `mcp__ENScan__*` | `http://127.0.0.1:18081/mcp` | 企业面主引擎，8 个工具。由 BLADEPRO 的 MCP 设置页管理（name=`ENScan`）。 |
| `mcp__browser__*` | Playwright MCP（stdio） | 手采兜底。需先 `Skill(playwright-cli)` 解锁（该技能声明 `mcps: browser`）。 |

- 本技能 frontmatter 声明 `mcps: ENScan`：**调用 `Skill(src-recon)` 后才解锁**这些工具，不调用不连接。
- BLADEPRO 默认禁止 MCP 连回环地址，需要启动环境有 `BLADEPRO_MCP_ALLOW_LOCAL=1`（本机已设置）。
- 工具不存在时：确认 18081 在听、设置页里 ENScan 已启用；**不要自行起服务、不要伪造结果**。

## 2. 文件与 shell

- 工作目录：会话 `<data>/sessions/<会话id>/`；任务 `<data>/tasks/<任务id>/`。
- file-guard 只允许写 `data/` 与 `skills/`；`~/Desktop`、`/tmp` 不可写。
- Windows 上 Bash 工具实际执行 PowerShell；脚本里的 bash 片段用 `bash` 显式调用。

## 3. 本机工具实况

| 工具 | 状态 |
|---|---|
| python | 3.13（`python`） |
| httpx / subfinder / dnsx / naabu / katana / gau / webfuzz | Git Bash PATH 中可用（未逐个验证） |
| OneForAll | `D:\OneForAll-0.4.5`（`o4a_enum.py` 认 `O4A_HOME`） |
| nmap / nuclei | 未确认；缺省时降级为 CT + DNS + WebFetch |
| FOFA | 已接入平台工具 `intel_search`（只查不入库，计每日配额；key 在系统设置里配） |
| Quake / Hunter | 未接通（需要登录态浏览器手采） |

## 4. 强度边界

- 独立会话：只被动（ENScan、CT/DNS 查询、WebFetch/搜索、`dedupe.py --skip-probe`）。
- 任务：可在任务范围内做低强度验证，仍不得爆破/漏洞探测。
