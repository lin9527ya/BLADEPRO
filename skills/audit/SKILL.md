---
name: audit
description: 源码安全审计。对泄露代码（.git/源码包/前端 bundle）做审计，定位可利用漏洞并给证据。Use when analyzing leaked source code, source maps, .git directories, or reviewing framework magic-modified code.
origin: quiver
upstream-license: AGPL-3.0-only
---

> 来源：quiver（AGPL-3.0-only），原文件 `skills/audit/SKILL.md`；移植时改写了工具名与外部路径。

# 源码安全审计

## 切入点

### .git 泄露

```bash
# 还源码
./.git/ → git log --all → git checkout <commit> -- .
# 或用 git-dumper / GitHack
# 常看：.env / config.* / database.yml / settings.py
```

### 前端 bundle（source map / JS）

```bash
# 查 source map
find . -name "*.map" -o -name "*.js.map"
# 还原后找 API 路由、硬编码 key、隐藏功能
grep -r "api_key\|secret\|password\|token" --include="*.js" -i
# 用 api-recon skill 做系统化提取
```

### 框架魔改识别

```bash
# 对比开源原版
# 1. 看 package.json / composer.json / requirements.txt 的版本号
# 2. 看路由命名约定（Rails/Django/Laravel/ThinkPHP 各有特征）
# 3. grep 已知框架的路由/中间件/配置文件名
# 4. 原版已知 CVE → 检查魔改版是否修补
```

## 常见审计模式

| 模式 | 检索 |
|---|---|
| SQL 注入 | `->where($` / `raw(` / `.format(` / `%s` 拼接 / `$_GET` 直入 |
| 命令注入 | `system(` / `exec(` / `shell_exec(` / `Runtime.exec` / `subprocess` + 用户输入 |
| 文件操作 | `file_get_contents(` / `readfile(` / `include(` + 用户路径 / `move_uploaded_file` |
| SSRF | `curl_init(` / `requests.get(` + 用户 URL / 代理配置 |
| 反序列化 | `unserialize(` / `pickle.loads(` / `ObjectInputStream` / `yaml.load` |
| 越权 | 查 ID 参数 → 找 model 查询 → 看 WHERE 是否带 user_id / owner 约束 |
| 硬编码凭据 | `api_key = ` / `secret = ` / `password = "` / `.env` 文件 |

## 产出落库

- 确认漏洞 → `report_finding`（含文件位置+触发路径+可复现 PoC）
- 疑似（无法验证）→ `record_fact(kind=inferred)`
- 审计经验 → `propose_knowledge(kind=param_rule)`

## 红线

- 审计对象限于授权目标相关代码
- 不审计、不留存与目标无关的第三方代码
- 结论分级：可直接利用 / 疑似（说明缺什么验证）；不把"理论存在"写成确定
