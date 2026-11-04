---
name: supply-chain-osint
description: 开源供应链情报搜索专项。目标出现开源组件/内部 SDK/JS 库/npm 包/小程序包指纹，或怀疑"用了开源/魔改/抄代码"时加载。四渠道（npm registry / GitHub / grep.app / 镜像站）+ 横纵扩展（同作者其他项目、scope 全量、历史版本、旧 commit），专找：凭证密钥、内网端点、未授权接口、签名算法、调试入口。
origin: quiver
upstream-license: AGPL-3.0-only
---

> 来源：quiver（AGPL-3.0-only），原文件 `skills/supply-chain-osint/SKILL.md`；移植时改写了工具名与外部路径。

# supply-chain-osint —— 开源供应链横纵搜索

> 核心信念一：**一个团队/作者会把同样的错误复制到所有项目里**——找到一个包，就要把它作者/scope 的全部包翻一遍。
> 核心信念二：**发布过的凭证删不掉**——当前版本干净 ≠ 历史版本干净（npm 历史版本永久可下载）。

## 〇、红线（先记）

- **只读**：不往任何仓库/包推代码、不投毒、不占他人资源
- **凭证只验有效性即止**（TSRC 规则 9）：AK/SK 只做只读验证（GetCallerIdentity / 列 Bucket / `/me`），严禁深入业务——"深入验证会被处罚"
- 不下真敏感数据：列 1-2 个对象/字段证明有效即止
- 作者个人信息（邮箱/账号）只记录不公开、不社工

## 一、四渠道（按顺序过）

### 1. npm registry（发现包 → 全量枚举 → 历史版本）

```bash
# 搜包（关键词）
curl "https://registry.npmjs.org/-/v1/search?text=<关键词>&size=100"
# 包详情（versions / maintainers / repository 全在 JSON）
curl "https://registry.npmjs.org/<包名>"
# ★ scope 全量枚举（全文搜索 + 本地过滤；registry 不开放 _all_docs，勿用该法）
curl "https://registry.npmjs.org/-/v1/search?text=@<scope>&size=250" | jq -r '.objects[].package.name' | grep "^@<scope>/"
# ★ 按维护者/作者枚举其全部包（两个维度都试——author 可能不入 maintainer 索引）
curl "https://registry.npmjs.org/-/v1/search?text=maintainer:<username>&size=250"
curl "https://registry.npmjs.org/-/v1/search?text=author:<username>&size=250"
# ★ 历史版本：列全部版本 + 拉旧版 tarball 解包
npm view <包名> versions --json
curl -O "https://registry.npmjs.org/<包名>/-/<包名>-<旧版本>.tgz" && tar -xzf <包名>-<旧版本>.tgz
# 解包后：grep -riE "AKID|LTAI|secret|password|token|\.env|signkey"
```

重点：**versions 数组逐版本看差异**——新版删掉的文件（keys.json / config.js / .env / test 目录）在旧版里还在。

### 2. GitHub（仓库 / 组织 / 历史 / 代码搜索——**token 已配**）

Token：环境变量 `GITHUB_TOKEN`（用户级已设）+ 文件 `（外部知识库未随包提供）\src\opencode\tools\.github_token.json`；额度 core 5000/h · search 30/min · **code_search 10/min**（省着用：先 grep.app 粗筛，再来这里精查）；直连可用。

```bash
# 代码搜索（API，带 token）
curl -s -H "Authorization: Bearer $env:GITHUB_TOKEN" "https://api.github.com/search/code?q=<特征串>&per_page=30"
# 仓库搜索
curl -s -H "Authorization: Bearer $env:GITHUB_TOKEN" "https://api.github.com/search/repositories?q=<关键词>&per_page=30"
# 组织/作者仓库页
#   github.com/orgs/<org>/repositories ; github.com/<user>?tab=repositories
# 历史：已删除文件 git log --diff-filter=D；旧 tag；releases 旧资产
# git clone（带 token 免交互；私有库/防匿名额）
git clone https://x-access-token:$env:GITHUB_TOKEN@github.com/<org>/<repo>
```

### 3. grep.app / 全网代码搜索

- **grep.app 程序化访问受 Cloudflare 挑战限制**（2026-09-30 实测：429 `challenge` 错误）——浏览器/人工查，或改用下方 API
- **GitHub code search（程序化主力，token 已配）**：

  ```bash
  curl -s -H "Authorization: Bearer $env:GITHUB_TOKEN" "https://api.github.com/search/code?q=<特征串>&per_page=30"
  ```

- 补充渠道：sourcegraph.com 公开搜索 · socket.dev（包依赖/安全评分）· libraries.io

用于：内部域名、AK 前缀（AKID/LTAI/STS）、API 路径、组件类名 → 找全网泄露源与关联仓库。

### 4. 镜像/归档补充

- `unpkg.com/<包>@<版本>/`、`cdn.jsdelivr.net/npm/<包>@<版本>/`——直接读包内文件（免下载 tarball）
- `web.archive.org`——已删除的文档/页面
- Gitee（国内项目）/ PyPI / Maven 同理枚举

## 二、横纵扩展（找到第一个包后必做）

| 方向 | 动作 |
|---|---|
| **横向：作者/组织** | 枚举 maintainers 的全部包 + **scope 下全部包**——同一人的代码习惯会复制粘贴 |
| **横向：monorepo 邻包** | 同仓库 `packages/*/package.json` 里的其他包 |
| **纵向：历史版本** | 逐版本拉（重点：首发版、0.0.x 早期、大版本前）→ grep 密钥/端点/debug |
| **纵向：旧 commit/tag** | 已删除文件、旧分支 |
| **关联：fork/克隆仓** | 搜同代码的其他仓库（fork 可能没同步删除凭证） |

## 三、找什么（审计清单）

| 目标 | 具体 | 后续 |
|---|---|---|
| 云凭证（AK/SK/STS/COS 令牌） | 硬编码在 config/keys/.env/示例代码 | **认钥闸**：假值对照 + 只读验证即止 |
| 内网域名/端点/环境 | `test.` `dev.` `internal.` `svn` `ip:port` | 记入记忆 + 可达性探测 |
| 未授权接口 | API 路径 + 是否带认证 | 直接测（对照接口清单） |
| 签名算法/密钥 | `computeSignature`/secret/salt | 用于伪造请求（对照记忆的签名公式） |
| 调试/后门入口 | dev server、`/run_server`、`evaluate`、debugger 端口 | 判远程可达性（127.0.0.1 绑定 = 无远程面） |
| 源码逻辑 | 协议实现/枚举/判权 | 读源码找缺失校验（比黑盒快 10 倍） |

## 四、案例（两个已实证）

1. **@pixui-dev/pxw（l2m，2026-09-25）**：游戏 JS 指纹 → npm 搜 `@pixui-dev` → 审计 pxw dev server → `killProcessById` 命令注入 + `/run_server` 任意脚本执行 → **开发者主机 RCE**（本地自建实例实证，报告已出）。
   教训：建"发布者图谱"（sliphua/xtma/ryougi/yanggwcn/xkazer…）+ 逐包审计。
2. **@pixui-dev/htmlpage2pixui@0.0.3（2026-09-30）**：同 scope 的**另一个项目**、**历史版本** → **腾讯云 AK/SK 泄露**。
   教训：**关键词搜索会漏包**（htmlpage2pixui 没被上轮关键词命中）——必须 **scope/作者全量枚举 + 逐个拉历史版本**；发布过的凭证删不掉。

## 五、产出与回填

- 每发现：记忆（`vendor/ammo`：包名/版本/泄露物/端点）+ 接口清单追加
- 可疑凭证 → 认钥闸验证 → 有效才进报告（`payload_source: npm 包` / `GitHub`）
- **负结果也记**（哪个包/版本干净）——防重复审计
