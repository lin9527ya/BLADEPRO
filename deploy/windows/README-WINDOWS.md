# BLADEPRO Windows 原生部署（Windows 10/11 amd64）

> 仓库内的完整版本见 [`docs/windows-deployment.md`](../../docs/windows-deployment.md)；本文件是随 Release zip 分发的现场速查版。

> 推荐：**交互式用户会话 + 本机 PostgreSQL 18**。不要把主程序注册为 Windows Service：session 0 会让浏览器/桌面 APP/GUI MCP 自动化失效。

## 1. 目录与端口

```text
C:\BLADEPRO\
  bladepro.exe       embedui 构建
  start.bat
  config.json
  skills\
  data\
```

默认监听：

- UI/API：`127.0.0.1:9898`
- 录制代理：`127.0.0.1:9899`
- PostgreSQL：建议 `127.0.0.1:5433`

安装目录必须对当前用户可写（`jwt.key`、`data\`、自更新暂存文件都写在 exe 同级）。不要用 `C:\Program Files`，除非你明确配置了 ACL。

## 2. PostgreSQL 18

安装 PostgreSQL 18 Windows 版，设置：

- 端口 `5433`（与 `config.example.json` 一致；也可自行改配置）
- 只监听 localhost
- 用 `scram-sha-256` 认证

用管理员账号创建应用角色和数据库（密码交互输入，不要写进命令行）：

```sql
CREATE ROLE bladepro LOGIN;
\password bladepro
CREATE DATABASE bladepro OWNER bladepro;
REVOKE ALL ON DATABASE bladepro FROM PUBLIC;
```

验证：

```powershell
& 'C:\Program Files\PostgreSQL\18\bin\psql.exe' -h 127.0.0.1 -p 5433 -U bladepro -W -d bladepro -c "select current_user, version();"
```

如果数据库已预建，不需要给应用角色 `CREATEDB`。应用每次启动会自动执行幂等 schema；数据库必须已存在或 role 有 `CREATEDB`。

## 3. 解压与配置

```powershell
Expand-Archive .\bladepro-<version>-windows-amd64.zip C:\
Rename-Item C:\bladepro-<version>-windows-amd64 C:\BLADEPRO
cd C:\BLADEPRO
Copy-Item config.example.json config.json
notepad config.json
```

填写 `database.host/port/user/password/dbname/sslmode`。`skill_dir` 留空，表示使用 exe 同级的 `skills\`。

推荐直接运行安装辅助脚本（不写密码到 argv）：

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\windows\install.ps1 `
  -InstallDir C:\BLADEPRO -PgPort 5433 -PgUser bladepro -PgDatabase bladepro
```

需要脚本创建 role/database 时加 `-CreateDatabase`（会交互询问 PostgreSQL 管理员密码）；需要登录自启加 `-RegisterTask`。

## 4. 启动与登录

```powershell
cd C:\BLADEPRO
.\start.bat
```

浏览器打开 `http://127.0.0.1:9898`，首次进入设置管理员密码。用户名固定为 `BLADEPRO`。

开机自启请用 **Task Scheduler ONLOGON + Interactive principal**，不要用 Windows Service。日志可重定向：

```powershell
.\start.bat >> .\bladepro.log 2>&1
```

## 5. 可选运行依赖

### 浏览器/截图

安装 Chrome 或 Edge。截图会自动查找 `chrome.exe`/`msedge.exe`；也可显式指定：

```powershell
setx BLADEPRO_CHROMIUM "C:\Program Files\Google\Chrome\Application\chrome.exe"
```

### Playwright MCP

```powershell
winget install -e --id OpenJS.NodeJS.LTS
# 新开终端
npx -y @playwright/mcp@latest --help
npx -y playwright@latest install chromium
```

内置 `browser` MCP 的 `npx` 会在 Windows 上通过 `cmd.exe /c` 启动。Node 不在 PATH 时会给出明确错误。

### 本机 HTTP/SSE MCP

默认拒绝连接 loopback MCP。当前用户会话设置：

```powershell
setx BLADEPRO_MCP_ALLOW_LOCAL 1
```

重新登录/重启终端后生效。stdio `.exe` MCP 不受这个开关影响。

## 6. HTTPS 录制与 MITM CA

首次开启流量捕获后，CA 生成在：

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

注意事项：

- Chrome/Edge、Go 工具、.NET 使用 Windows 证书库；导入 CA 后 HTTPS 捕获才稳定。
- Go Windows 程序会忽略 `SSL_CERT_FILE`，必须依赖系统证书库。
- PowerShell 5.1/.NET 不一定遵守 `HTTP(S)_PROXY`；录制阶段优先使用 `curl.exe` 或 Go 工具。Chromium/Playwright 通过 `--proxy-server` 和 CA/忽略证书错误配置。
- 不要把代理绑定到非回环地址，不要使用 `-allow-open-proxy`。

## 7. MCP / APP 说明

原生 Windows 适合：

- Windows 本地 stdio MCP（`.exe`）、HTTP/SSE MCP
- Playwright/Chrome/Edge 浏览器测试
- 交互式 Windows 桌面 APP MCP（WeChat/WxTap 等）
- ADB/Android 模拟器、Frida/JADX 等本地工具

仍建议 WSL2/Git Bash/Docker：

- ffuf/nmap/nuclei/sqlmap/PTY 等 Linux 优先工具链
- `poc.sh`、Bash 技能和 Unix 路径脚本
- 需要 Linux 权限/进程组语义的任务

Windows 原生暂不提供：OS 级 Agent 降权、PTY、内置 APP 自动化、资源 cgroup/ulimit 等价限制。

## 8. 常见问题

| 现象 | 处理 |
|---|---|
| 启动失败，提示 `skill_dir /opt/...` | `config.json` 里把 `skill_dir` 设为空字符串 |
| DB 连接失败 | 确认 PG 服务、端口、role/database 和 `pg_hba.conf`；预建 DB 比依赖 CREATEDB 更稳 |
| 证据写入报目录 sync 错误 | 确认使用包含 `evidence/sync_dir_windows.go` 的 Windows 构建 |
| MCP 报 `%1 is not a valid Win32 application` | 安装 Node LTS，确认 `npx.cmd` 在 PATH；使用支持 `.cmd` 包装的构建 |
| 截图不可用 | 安装 Chrome/Edge，或设置 `BLADEPRO_CHROMIUM` |
| 浏览器 HTTPS 失败 | 导入 MITM CA；确认 capture 开启 |
| 自更新不动 | 必须通过 `start.bat` 启动；exe 目录必须可写 |
| 取消任务后仍有子进程 | 当前构建使用 `taskkill /T /F`；若仍残留，记录进程树以便后续 Job Object 加固 |
