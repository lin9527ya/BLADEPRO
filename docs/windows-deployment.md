# BLADEPRO Windows 原生部署指南

> 适用：Windows 10/11 amd64，**原生运行**（非 Docker/WSL），本机 PostgreSQL 18。
> 目标：先在 Windows 宿主机完成通用部署，再接入浏览器、MCP 和桌面 APP 测试。

## 1. 支持边界

原生 Windows 适合：

- Web UI、API、任务、SRC 报告和 PostgreSQL
- Windows 本地 stdio MCP（`.exe`）
- 本机 HTTP/SSE MCP
- Chrome/Edge + Playwright 浏览器测试
- 需要 Windows 交互式桌面的 APP MCP（WeChat、WxTap 等）
- ADB、Android 模拟器、Frida/JADX 等本地工具

仍建议 WSL2、Git Bash 或 Docker：

- `ffuf`、`nmap`、`nuclei`、`sqlmap`、交互式 PTY
- `poc.sh`、Bash 技能和 Unix 路径脚本
- 依赖 Linux 用户/进程组/资源限制的任务

> **不要用 Windows Service 运行主程序。** Session 0 没有交互式桌面，浏览器和 GUI APP 自动化会失效。请使用登录用户的 `start.bat` 或 Task Scheduler 的 Interactive principal。

## 2. 推荐拓扑

```text
Windows 10/11 amd64
├─ PostgreSQL 18 Windows 服务        127.0.0.1:5433
│  └─ database/role: bladepro
├─ C:\BLADEPRO\                     可写目录，不要放 Program Files
│  ├─ bladepro.exe                  embedui 构建
│  ├─ start.bat                     守护/自更新重启
│  ├─ config.json                   DB 连接；skill_dir 留空
│  ├─ skills\
│  └─ data\
│     ├─ traffic\                   SQLite 索引、blob、MITM CA
│     ├─ evidence\
│     └─ tasks\
├─ UI/API                            127.0.0.1:9898
├─ 录制代理                          127.0.0.1:9899
├─ Chrome/Edge                       截图、浏览器测试
└─ Node LTS + npx（可选）             Playwright MCP
```

所有服务默认只监听回环地址。不要改成 `0.0.0.0:9898`，也不要使用 `-allow-open-proxy`。

## 3. 前置条件

| 组件 | 要求 | 用途 |
|---|---|---|
| Windows | 10/11 amd64 | 当前发布只有 windows/amd64 |
| PostgreSQL | 18（16+ 通常也可） | 主数据存储，必须外部安装 |
| PowerShell | 5.1+ | Bash 工具在 Windows 下的执行壳 |
| Node.js | LTS（可选） | Playwright/`npx` 类 MCP |
| Chrome/Edge | 可选但推荐 | 截图、浏览器测试 |
| Python | 3.x（可选） | 自定义 Python 工具、部分技能脚本 |
| Git Bash/WSL2 | 可选 | Linux 工具链和 bash 脚本 |

安装目录必须对当前用户可写：`jwt.key`、`data\`、自更新暂存文件都写在 exe 同级目录。

## 4. 安装 PostgreSQL 18

1. 使用 EDB Windows 安装器安装 PostgreSQL 18。
2. 建议端口 `5433`，避免和已有 PostgreSQL 5432 冲突。
3. 只监听 localhost；`pg_hba.conf` 对 `127.0.0.1/32` 和 `::1/128` 使用 `scram-sha-256`。
4. 启动 `postgresql-x64-18` 服务。

创建应用角色和数据库（密码交互输入，不要写进命令行历史）：

```sql
CREATE ROLE bladepro LOGIN;
\password bladepro
CREATE DATABASE bladepro OWNER bladepro;
REVOKE ALL ON DATABASE bladepro FROM PUBLIC;
```

验证：

```powershell
& 'C:\Program Files\PostgreSQL\18\bin\psql.exe' `
  -h 127.0.0.1 -p 5433 -U bladepro -W -d bladepro `
  -c "select current_user, current_database(), version();"
```

推荐预建数据库，不依赖应用角色的 `CREATEDB`。如果数据库不存在且角色无建库权限，启动会失败并给出提示。

## 5. 解压与配置

从 Releases 下载 `bladepro-<version>-windows-amd64.zip`：

```powershell
Expand-Archive .\bladepro-<version>-windows-amd64.zip C:\
Rename-Item C:\bladepro-<version>-windows-amd64 C:\BLADEPRO
cd C:\BLADEPRO
Copy-Item config.example.json config.json
notepad config.json
```

`config.json` 至少填写：

```json
{
  "database": {
    "host": "127.0.0.1",
    "port": 5433,
    "user": "bladepro",
    "password": "<你的数据库密码>",
    "dbname": "bladepro",
    "sslmode": "disable"
  },
  "skill_dir": ""
}
```

`skill_dir` 留空表示使用 exe 同级的 `skills\`。不要保留示例里旧的 `/opt/bladepro/skills`。

限制配置目录 ACL：

```powershell
icacls C:\BLADEPRO /inheritance:r /grant:r "${env:USERDOMAIN}\${env:USERNAME}:(OI)(CI)F"
```

建议同时限制 `config.json`、`jwt.key` 和 `data\` 只允许运行账户访问。

## 6. 使用安装辅助脚本

Release zip 包含 `deploy\windows\`：

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\windows\install.ps1 `
  -InstallDir C:\BLADEPRO `
  -PgHost 127.0.0.1 -PgPort 5433 `
  -PgUser bladepro -PgDatabase bladepro
```

脚本会：

- 检查安装目录是否可写
- 生成/更新 `config.json`
- 把 `skill_dir` 设为空
- 尝试收紧安装目录 ACL
- 检查 Node/Chrome（可用 `-SkipPrereqCheck` 关闭）
- 可选 `-CreateDatabase`：交互输入 PostgreSQL 管理员密码后创建 role/database
- 可选 `-RegisterTask`：注册登录时启动的交互式 Task Scheduler 任务

不会把数据库密码写进命令行参数。

## 7. 启动

```powershell
cd C:\BLADEPRO
.\start.bat
```

浏览器打开：

```text
http://127.0.0.1:9898
```

首次进入设置管理员密码。登录用户名固定为 `BLADEPRO`。

需要记录日志时：

```powershell
.\start.bat >> .\bladepro.log 2>&1
```

`start.bat` 处理：

- 退出码 `0`：正常停止
- 退出码 `75`：页面一键更新/回滚后立即重启
- 其他退出码：指数退避后重启（最多 60 秒）

请通过 `start.bat` 启动，不要直接运行 `bladepro.exe`，否则自更新后不会自动拉起。

### 登录时自启（可选）

使用 Task Scheduler，**Interactive** principal，而不是 Windows Service：

```powershell
$action = New-ScheduledTaskAction -Execute 'cmd.exe' `
  -Argument '/c ""C:\BLADEPRO\start.bat" >> "C:\BLADEPRO\bladepro.log" 2>&1"' `
  -WorkingDirectory 'C:\BLADEPRO'
$user = "$env:USERDOMAIN\$env:USERNAME"
$principal = New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Limited
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $user
Register-ScheduledTask -TaskName 'BLADEPRO' -Action $action -Trigger $trigger -Principal $principal -Force
```

## 8. 浏览器、截图与 MCP

### Chrome/Edge

截图会自动查找 Chrome/Edge；也可以显式指定：

```powershell
setx BLADEPRO_CHROMIUM "C:\Program Files\Google\Chrome\Application\chrome.exe"
```

重新打开终端后生效。

### Playwright MCP

```powershell
winget install -e --id OpenJS.NodeJS.LTS
# 新开终端
node --version
npx --version
npx -y @playwright/mcp@latest --help
npx -y playwright@latest install chromium
```

内置 `browser` MCP 使用 `npx @playwright/mcp --headless`。Windows 上服务会自动通过 `cmd.exe /c` 启动 `.cmd` shim；Node 不在 PATH 时会返回明确错误。

### 本机 HTTP/SSE MCP

默认禁止连接 loopback。需要连接 Windows 本机 MCP 时，在当前用户环境变量中设置：

```powershell
setx BLADEPRO_MCP_ALLOW_LOCAL 1
```

重新登录或重开终端后生效。stdio `.exe` MCP 不受此开关影响。

## 9. 流量录制和 MITM CA

录制代理默认：

```text
127.0.0.1:9899
```

首次开启 capture 后生成：

```text
C:\BLADEPRO\data\traffic\_ca\mitmproxy-ca-cert.pem
```

导入当前用户 Root（不需要管理员）：

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\windows\import-ca.ps1 -InstallDir C:\BLADEPRO
```

删除：

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\windows\import-ca.ps1 -InstallDir C:\BLADEPRO -Remove
```

说明：

- Chrome/Edge、Go Windows 程序、.NET 使用 Windows 证书库；导入 CA 后 HTTPS 才能正常验证。
- Go Windows 程序忽略 `SSL_CERT_FILE`，必须使用系统证书库。
- PowerShell 5.1/.NET 不总是遵守 `HTTP(S)_PROXY`；录制阶段优先用 `curl.exe` 或 Go 工具。
- Chrome/Edge 通过 Playwright MCP 的代理参数和忽略证书错误选项工作。
- 不要信任来源不明的 CA；不再使用 capture 时移除 CA。

## 10. 密码重置

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\windows\reset-password.ps1 `
  -InstallDir C:\BLADEPRO
```

脚本从 `config.json` 读取数据库连接，使用 PostgreSQL `pgcrypto` 在库内生成 bcrypt，不把新密码放进命令行参数。

## 11. Windows 行为与已知限制

已完成 Windows 适配：

- 证据目录 fsync 不再对目录执行不可靠的 Windows sync
- `npx`/`.cmd` stdio MCP 通过 `cmd.exe /c` 启动
- Chrome/Edge 自动发现
- `run_execution` 使用 PowerShell，与 Bash 工具一致
- 取消/超时会用 `taskkill /T /F` 终止进程树
- Windows 破坏性命令拦截规则
- Windows 不注入不可用的 PTY 工具
- Windows 路径边界大小写不敏感
- 自定义工具参数按 PowerShell 单引号规则转义

仍然存在的限制：

- **没有 Windows 原生 APP 自动化**：WeChat/WxTap/Android 等依赖外部 MCP 或工具。
- **没有 OS 级 Agent 降权**：`BLADEPRO_AGENT_USER` 在 Windows 不可用；使用专用非管理员账户、ACL 和回环绑定。
- **没有资源限制**：`ulimit` 等 Unix 限制在 Windows 是 no-op；失控扫描器可能占满 CPU/内存。
- **Linux 工具链不完整**：`ffuf`/`nmap`/`nuclei`/`sqlmap`/PTY 建议 WSL2 或 Git Bash。
- **CA 必须手动信任**：不会自动修改证书库。
- **Windows Service 不支持 GUI 自动化**：只能用于无桌面需求的场景。
- **Windows 目录 fsync 弱于 Linux**：掉电时证据 rename 元数据的耐久性依赖 NTFS 日志；请备份 `data\evidence` 和 `data\traffic`。
- **当前发布只有 windows/amd64**，不支持 ARM64 原生发布。

## 12. 排障

| 现象 | 处理 |
|---|---|
| 启动失败，提示 `skill_dir /opt/...` | 把 `config.json` 的 `skill_dir` 改为空字符串 |
| 连不上 PostgreSQL | 检查服务、端口、role/database、`pg_hba.conf`；优先预建 DB |
| `data\evidence` 写入失败 | 使用包含 Windows `syncDir` 修复的构建；检查目录 ACL |
| MCP 报 `%1 is not a valid Win32 application` | 安装 Node LTS，确认 `npx.cmd` 在 PATH |
| 截图不可用 | 安装 Chrome/Edge，或设置 `BLADEPRO_CHROMIUM` |
| 浏览器 HTTPS 失败 | 导入 MITM CA，确认 capture 已开启 |
| 自更新不生效 | 必须通过 `start.bat` 启动；exe 目录必须可写 |
| 取消任务后仍有子进程 | 当前构建使用 `taskkill /T /F`；记录进程树便于后续 Job Object 加固 |
| PowerShell 命令不走代理 | .NET 可能忽略 `HTTP(S)_PROXY`；改用 `curl.exe` 或 Go 工具 |
| 任务删除/归档报文件占用 | 先停止相关执行，关闭占用 `data\tasks` 的进程 |

## 13. 上线验证清单

在 Windows 宿主机上，用 `127.0.0.1` 本地测试，不要对真实目标扫描：

- [ ] `pg_isready` 和 `select version()` 正常
- [ ] `start.bat` 启动，`/api/health` 返回 200
- [ ] 登录页和嵌入静态资源正常
- [ ] 创建本地 SRC 任务，绑定 proof 流量，证据写入无目录 sync 错误
- [ ] `run_execution` 长命令取消后无残留子进程
- [ ] 启用 browser MCP，`npx.cmd` 握手和工具发现成功
- [ ] 截图产出 PNG
- [ ] 导入 CA 后 Chrome/Edge 和 `curl.exe` 的 HTTPS 录制正常
- [ ] 自更新暂存、重启和回滚正常
- [ ] Task Scheduler 运行在交互式用户会话
- [ ] 9898/9899/5433 只监听回环地址

## 14. 安全基线

- 保持 UI/API 和代理绑定 `127.0.0.1`，不要暴露到局域网/公网。
- 不要使用 `-allow-open-proxy`。
- 使用专用 Windows 账户运行；限制安装目录和数据目录 ACL。
- 把数据库密码、`jwt.key`、MITM CA 私钥当作敏感文件处理。
- 只导入自己生成的 MITM CA；卸载时移除。
- 不在验证阶段运行真实目标扫描。
