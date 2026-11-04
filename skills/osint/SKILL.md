---
name: osint
description: 公开情报收集。从 GitHub/搜索引擎/npm 等公开源检索目标信息——同作者项目、框架魔改来源、泄露文档、人员标识（姓名/手机号/工号/openid）。Use when doing reconnaissance, searching for leaked data, finding author's other projects, or collecting personnel identifiers.
origin: quiver
upstream-license: AGPL-3.0-only
---

> 来源：quiver（AGPL-3.0-only），原文件 `skills/osint/SKILL.md`；移植时改写了工具名与外部路径。

# OSINT 公开情报收集

## 检索面

- **GitHub**：`site:github.com <目标域名>` / `<框架名> leak` / `<公司名> password` / `"<api_key>"` / `filename:.env <域名>`
- **搜索引擎**（Google/Bing/百度/搜狗微信）：`site:<域名> filetype:pdf|doc|xlsx` / `site:<域名> admin|login|test|api` / `"<手机号>"` / `"<工号>"`
- **npm / PyPI / Maven**：`<公司/团队名>`——搜内部包名；泄露的 token 常在 `.npmrc`/`package.json` 里
- **网盘/文库**：百度文库/道客/豆丁 `site:<域名>`——内部 PPT/手册常泄露接口与架构
- **同作者项目**：GitHub 搜作者名/邮箱前缀，看其他 repo——常共用密钥/框架/代码
- **框架魔改**：`<框架名> fork OR 原版` ——确认是否基于知名开源改的，原文档/已知 CVE 可复用

## 产出落库

- 人员标识（姓名/手机号/工号/openid）→ `record_knowledge(kind=id|username|phone|openid)`
- 接口规律 / URL 模式 → `record_knowledge(kind=url_pattern|param_pattern)`
- 泄露凭据/token → `record_knowledge(kind=cookie|token)`
- 可复用经验 → `propose_knowledge`

## 红线

- 只做公开渠道检索；不社工、不撞库、不破解任何数据集
- 每条产出注明来源链接
- 与当前任务无关厂商的人员信息不收集
