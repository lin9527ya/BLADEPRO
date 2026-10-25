// 推荐 MCP 目录（P28 续）：**只做预填，不自动安装**——用户点哪一条才把那条的命令/参数
// 填进「添加 MCP」弹窗，仍然由人过一眼再保存。新增推荐项只影响这个列表，不动任何已有配置。
//
// 收录标准：与 BLADEPRO 既有能力**互补**，不重复它已经有的东西。
// 刻意不收：filesystem（已有 Read/Write/Bash + 工作区）、memory / knowledge-graph（已有公司知识层
// 与图谱）、搜索类（平台内建搜索后端）、puppeteer（与 playwright 重复）、直连平台自己数据库的。

export type MCPCatalogItem = {
  /** 服务器名（预填进表单的 name） */
  name: string;
  /** 场景标签（Web / APP / 小程序），只用于列表分组提示 */
  tag?: string;
  /** 中文标题 */
  title: string;
  /** 一句话：它补的是哪块能力 */
  desc: string;
  transport: "stdio" | "http" | "sse";
  command?: string;
  /** 空格分隔的参数（与表单一致） */
  args?: string;
  url?: string;
  /** 环境变量/请求头预填（每行 KEY=VALUE；密钥留空待人填） */
  env?: string;
  /** 前置条件（key、常驻服务等） */
  needs?: string;
  /** 注意事项 */
  note?: string;
};

export const MCP_CATALOG: MCPCatalogItem[] = [
  {
    name: "chrome-devtools",
    title: "Chrome DevTools MCP",
    desc: "浏览器深度调试：控制台报错、网络请求、DOM/存储。SRC 前端面（DOM XSS、未授权接口、越权参数）验证比 playwright 更贴行为。",
    transport: "stdio",
    command: "npx",
    args: "-y chrome-devtools-mcp@latest",
    note: "与已启用的 playwright MCP 互补，不重复：它给的是 DevTools 视角（网络/控制台），不是自动化脚本。",
  },
  {
    name: "github",
    title: "GitHub MCP",
    desc: "代码搜索、仓库历史与 **补丁差分**——白盒找 0day 的高效路径（看上游什么时候、怎么修的）。",
    transport: "stdio",
    command: "npx",
    args: "-y @modelcontextprotocol/server-github",
    env: "GITHUB_PERSONAL_ACCESS_TOKEN=",
    needs: "GitHub PAT（fine-grained 即可，按需给 repo / public_repo 读权限）",
    note: "token 填在环境变量里；建议只给只读权限的 PAT。",
  },
  {
    name: "semgrep",
    title: "Semgrep MCP",
    desc: "规则化静态分析 + 语义匹配，直接服务白盒审计：把「可疑点」从几万行里筛出来再深读。",
    transport: "stdio",
    command: "uvx",
    args: "semgrep-mcp",
    needs: "本机有 uv/uvx（没有的话用 pip 装 semgrep-mcp 后把命令换成 semgrep-mcp）",
  },
  {
    name: "burp",
    title: "Burp Suite MCP",
    desc: "把 Burp 的 sitemap / 主动扫描 / Repeater 交给 agent。BLADEPRO 自带 MITM 记录与拦截，Burp 的扫描器与生态是差异面。",
    transport: "sse",
    url: "http://127.0.0.1:9876/sse",
    needs: "Burp 常驻，并安装官方 MCP 扩展（扩展里会给出实际的 host/port）",
    note: "端口以 Burp 扩展里显示的为准；不同版本可能是 http 而非 sse，按实际改。",
  },
  {
    name: "shodan",
    title: "Shodan MCP",
    desc: "与 FOFA 互补的测绘源：`vulns:` 过滤、历史 banner、组织维度，是 FOFA 没有的差异面。",
    transport: "stdio",
    command: "npx",
    args: "-y shodan-mcp",
    env: "SHODAN_API_KEY=",
    needs: "Shodan API key（付费账号才有完整过滤与历史）",
  },
  {
    name: "context7",
    title: "Context7 MCP",
    desc: "按版本取库/框架文档，写代码与写规则时不再凭记忆——减少 agent 自己造错 API 的概率。",
    transport: "stdio",
    command: "npx",
    args: "-y @upstash/context7-mcp",
  },
  // ---- Web 渗透：代理生态与扫描器（Burp 已在上面；Caido/ZAP 是差异面） ----
  {
    name: "caido",
    tag: "Web",
    title: "Caido MCP",
    desc: "把 Caido 的代理历史（HTTPQL 搜索）、Replay、Automate 模糊测试、Findings、Sitemap、Tamper/Intercept 交给 agent；与 Burp 二选一即可。",
    transport: "stdio",
    command: "caido-mcp-server",
    args: "serve",
    env: "CAIDO_URL=http://127.0.0.1:8080",
    needs: "先安装 caido-mcp-server（install.sh / Release 二进制 / go install），并让 Caido 桌面端或 CLI 常驻",
    note: "默认对 Authorization/Cookie 等敏感头脱敏；确需原值重放时再加 CAIDO_ALLOW_SENSITIVE_HEADERS=true（凭据会进入模型上下文）。",
  },
  {
    name: "zap",
    tag: "Web",
    title: "MCP ZAP Server",
    desc: "把 OWASP ZAP 的爬虫/被动与主动扫描/告警/报告封装成受控工具，适合需要「扫描器结论」而不只是手工验证的场景。",
    transport: "http",
    url: "http://127.0.0.1:7456/mcp",
    env: "X-API-Key=",
    needs: "Docker 20.10+ 与 Docker Compose v2；先 clone 仓库执行 ./bin/bootstrap-local.sh --start，并把 .env 里的 MCP_API_KEY 填到 X-API-Key",
    note: "社区实现，非 ZAP 官方；主动扫描会真实发包，SRC 项目先确认平台允许扫描器并调低并发。",
  },

  // ---- APP：静态（JADX/APKTool）+ 动态（Frida）+ 设备面（DroidMind/mobile-mcp） ----
  {
    name: "jadx-headless",
    tag: "APP",
    title: "JADX Headless MCP",
    desc: "无 GUI 的 JADX：类树、反编译源码/方法/smali、字符串与类名搜索、交叉引用，直接吃 APK，适合批量静态审计。",
    transport: "stdio",
    command: "java",
    args: "-jar /opt/jadx-headless-mcp/jadx-headless-mcp-all.jar",
    needs: "JDK 17+；从 GitHub Release 下载 jadx-headless-mcp-<ver>-all.jar",
    note: "把 args 里的 jar 路径换成本机实际位置；可追加 --apk /path/app.apk 预载目标。",
  },
  {
    name: "apktool",
    tag: "APP",
    title: "APKTool MCP",
    desc: "解包/回编译 APK，读写 AndroidManifest、smali 与资源；用于确认 exported 组件、权限、深链、网络安全配置与重打包验证。",
    transport: "stdio",
    command: "uvx",
    args: "apktool-mcp",
    needs: "Java + apktool CLI 在 PATH（apktool -version 能跑）",
    note: "decode/build 会在本地生成目录并可能改动包；只处理自有或已获书面授权的 APK。",
  },
  {
    name: "frida",
    tag: "APP",
    title: "Frida MCP",
    desc: "动态插桩：hook Java/native 方法、绕过 SSL pinning/root 检测、导出加解密与签名参数，补静态分析看不到的运行时行为。",
    transport: "stdio",
    command: "uvx",
    args: "frida-mcp",
    needs: "Frida 16+；目标设备已 root/越狱或可注入 frida-server，frida-ps -U 能列出进程",
    note: "只对授权 App 注入；优先用测试包/副本，避免影响生产账号与真实用户数据。",
  },
  {
    name: "droidmind",
    tag: "APP",
    title: "DroidMind MCP",
    desc: "ADB 设备面：设备/应用管理、logcat、文件拉取、截图、UI 操作与受控 shell——APP 测试的底座能力。",
    transport: "stdio",
    command: "uvx",
    args: "droidmind --transport stdio",
    needs: "adb + 已授权 USB 调试的真机或模拟器；uvx 会按需取 Python 3.13",
    note: "shell 命令有风险分级与路径保护；仍建议在专用测试机/模拟器上运行。",
  },
  {
    name: "mobile-mcp",
    tag: "APP",
    title: "Mobile MCP",
    desc: "跨 iOS/Android 的 UI 自动化与抓屏：点击/输入/滑动、启停应用、读屏，适合复现步骤与关键截图取证。",
    transport: "stdio",
    command: "npx",
    args: "-y @mobilenext/mobile-mcp@latest",
    env: "MOBILEMCP_DISABLE_TELEMETRY=1",
    needs: "Node 20+；iOS 需 Xcode CLT，Android 需 platform-tools；已启动模拟器或已授权的真机",
    note: "只做设备侧交互，不替代抓包；目标流量仍走 BLADEPRO 的记录代理。",
  },

  // ---- 微信小程序：开发调试 + 安全调试 ----
  {
    name: "wechat-devtools-mcp",
    tag: "小程序",
    title: "WeChat DevTools MCP",
    desc: "驱动微信开发者工具：编译/预览/上传、点击输入、页面元素与日志、长截图，适合功能面与前端逻辑验证。",
    transport: "stdio",
    command: "uvx",
    args: "wechat-devtools-mcp",
    env: "WECHAT_DEVTOOLS_CLI=\nWECHAT_PROJECT_PATH=",
    needs: "微信开发者工具常驻，设置→安全设置→服务端口开启；填 CLI 与小程序项目绝对路径",
    note: "开发者工具 2.x 也内建 MCP，两者可共存（名字别撞）；小程序网络流量仍走记录代理。",
  },
  {
    name: "wxtap",
    tag: "小程序",
    title: "WxTap MCP",
    desc: "小程序安全调试台：运行时调试、WxAPI/云函数捕获与重放、wxapkg 还原、敏感信息扫描、session_key/微信 AK 提取。",
    transport: "http",
    url: "http://127.0.0.1:9527/mcp",
    needs: "Windows 10/11（主）或 macOS Apple Silicon（能力受限）；微信桌面端已登录并打开目标小程序；Node.js 22+；先在 WxTap「系统→MCP 服务」启动",
    note: "Linux 桌面微信不在支持范围；仅限自有或已获书面授权的小程序。",
  },
];
