# 第三方来源：quiver（AGPL-3.0）

BLADEPRO 的部分能力移植自同系谱的个人魔改分支 **QUIVER**（基于 ARTEX v0.3.15 的 AGPL-3.0 衍生作品）。
本文件记录来源、许可证与移植清单，供合规与回溯使用。

- 来源路径：`D:\Code\quiver`（非 git 仓库，无提交历史）
- 许可证：GNU Affero General Public License v3.0（与 BLADEPRO 一致）
- 移植日期：2026-11
- 移植原则：**只取能力与文本，不取任务模型**；BLADEPRO 的 `srcmode/`、`srcprobe/`、`srcbench/`、
  `intercept/`、`report/`、`traffic/`、`evidence/`、`intel/`、`policy/` 继续作为主干实现。
- 修改说明：所有移植文件在文件头标注 `Ported from quiver (AGPL-3.0)`；import 路径改写为
  `github.com/lin9527ya/bladepro/...`，SDK 由 `Autumn-27/norma` 改为 `lin9527ya/Norma`；
  技能正文中的工具名/绝对路径按 BLADEPRO 约定改写。

## 源文件清单（sha256）

| 源文件 | sha256 |
|---|---|
| `skills/android-dex-dump/SKILL.md` | `06aabf9a556447bceaf2774678143b824c92bd710bef30bbe1b6c4016cdb4671` |
| `skills/android-stealth-hook/SKILL.md` | `2d1d9388101a0748e3496d7a2139a8d687ed6d613ea73de4914c0dc7d427bd30` |
| `skills/api-recon/SKILL.md` | `f49b3168635148a0168df8af9c99e5b2ce06aa5ef7c1714706b6e8eae3d63b61` |
| `skills/audit/SKILL.md` | `b48365ccd14089a87dfb469d13bcf86173a373a6651ef08b61001152ccd21792` |
| `skills/osint/SKILL.md` | `5cda21a3e3d364433c32f347f368ab29418fc27d0a29d08acb7cae871e5626c8` |
| `skills/ossfuzz/SKILL.md` | `01e2a92bf7f0cbc9a8abcad4251a7ae077d4f566f7b9e4243c99c7d643dba091` |
| `skills/playwright-cli/SKILL.md` | `29c0b669a524d42b51934d7db9144c1c07a566d0549ae6fb6ccd810e5a3cf61b` |
| `skills/reverse/SKILL.md` | `9c1c0ad081d2624266317b6d567dec78f55516e0c1d08d1a8c61c791643f5d4e` |
| `skills/scopesentry/SKILL.md` | `7653202f58317264771c46c8dd3c81d67b3aa1e0f81a8fba33380de83e8e7026` |
| `skills/src-authz/SKILL.md` | `294315876b14334f793a97d13bbb780cc74b532b77a3a9fd8c674fa8028e6249` |
| `skills/src-bypass/SKILL.md` | `8dc3d4eb18dcd21fdafcc510becf36a610a3432f2a6a913d3bfce19d1b3dc668` |
| `skills/supply-chain-osint/SKILL.md` | `266c0a6072d6198dc377ebdd0c902729342a4b2697a3f299b498a03f24d63dfe` |
| `agent/reversetools.go` | `8f34fd8ce657e8f826f03c2ee8171314e6d9e2b42d749334246603b5fc17fbee` |
| `agent/visiontool.go` | `5e26d3cf6b2548670ab57cb61ca643d94a60420505f992df2a4e7f5d7b98b781` |
| `agent/diverge.go` | `1074d072c372276728365031f2e56e6d8b30fc794c89e192355179491e9e9853` |
| `agent/feishutools.go` | `48c20819fab7809f6349a1e9179ed27e2526b88d9cc4d993106698c24f60fcdc` |
| `agent/rulestool.go` | `138e724cb8f49e132ab663fcf3f7f069fdf045d762fa6ee82319842326d6d007` |
| `agent/memory.go` | `219bdee4ef60884aff68282096e03549178cad1c3d861d361b0c0ecf14bf39e6` |
| `agent/constraints.go` | `8d47f5f4907bba872817ea79a07f8b155dc796d270d8cdcb692fb396e51e8ada` |
| `server/memory_api.go` | `d65c08957967083fb5bda0d2a465ab7435c93bf73d8c43eaa89b37e895cf525d` |
| `server/quiver_api.go` | `70b8aebe61ff6555de87b9b691d1b414a1ac10f7751c93904d38b245b8563eea` |
| `server/devices_api.go` | `ea03752c57bea73cdf6968413446bc2bb2b9311b5eb5d297d66e356d038c238c` |
| `server/retrospect.go` | `7aad044d6c6a1e75718b4feb5cee72c9785b01cf1bf7070a27a5469aed9cf582` |
| `server/knowledge_ingest.go` | `02b95273617aff351907a8e92d725b1a8ef5da986635df481f1cafd5246f312c` |
| `server/watchdog.go` | `1f6ba71749f38650b9ad3d7f5483241fa5d1743f6589a98eebeae9d12c228568` |
| `docingest/docingest.go` | `90e7ff36faf4a69d0b0c003bd847a33d9b0357db85e74a9d16bd9f968c26bb59` |
| `notify/feishuapp.go` | `e2aac4ec27e7c5b792d744c5995636a0658cbefcda46ce098684d7fa8fdfcae1` |
| `db/schema.sql` | `1e24bdeab4d1c8b62ed7a2cfd106a67bf3ba9678d7383a769b81e6fbe17fb248` |

## 已移植 / 计划移植

| 阶段 | 内容 | 状态 |
|---|---|---|
| P0 | 本文件 + NOTICE | 已完成 |
| P1 | `db/schema.sql` Q 段（knowledge_entries/approvals/test_accounts/rating_rules/devices、companies.profile_*） | 已完成 |
| P2 | 知识工具面（lookup/record/propose/feedback）、注入块、审批 API、知识页 | 已完成（docingest/ingest 与 retrospect 待做） |
| P3 | 深度分级、厂商档案归因、测试账号池、定级规则 | 已完成 |
| P4 | 9 个技能（src-authz/src-bypass/ossfuzz/supply-chain-osint/osint/audit/reverse/android-*） | 已完成 |
| P5 | jadx/ghidra/dnslog、view_image、飞书工具、diverge、docingest+ingest、devices、watchdog、retrospect、意图质量字段 | 已完成；文档喂入与任务复盘使用激活 LLM profile 蒸馏（`server/distill.go`），无 profile 或调用/解析失败时回退结构化输出 |
| P6 | socint/reverse/codeaudit/reviewer/ops 五个 persona | 已完成（提示词/技能可见性/联网开关；前端为对话页自动出现） |

> 注：quiver 中 BLADEPRO 已具备同等或更强实现的部分（任务模式、覆盖矩阵、收尾闸、交付台账、
> 拦截层、报告导出、出口限速、测绘引擎）**不在移植范围**，避免重复建设与语义冲突。
