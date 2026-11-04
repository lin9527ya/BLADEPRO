---
name: android-stealth-hook
description: Android 真机无痕 Hook（rustFrida + wxshadow + hide-maps/kload）：反调试/反 Frida/完整性校验对抗、证书钉死绕过、无痕断点与寄存器修改、无 ptrace 注入、属性伪装、银行/金融 App 抓包与加密分析。当 App 测试遇加固、反调试、抓不到包，或需要对 App 做 native/Java hook、内存修改、无痕注入时使用。脱壳（DEX/so dump）走 android-dex-dump 技能。**加固/反调试/钉死是上本技能的触发条件，不是跳过理由**；常规代理能过时按顺序排在后面。
origin: quiver
upstream-license: AGPL-3.0-only
---

> 来源：quiver（AGPL-3.0-only），原文件 `skills/android-stealth-hook/SKILL.md`；移植时改写了工具名与外部路径。

# Android 无痕 Hook（rustFrida + wxshadow + hide-maps/kload）

基于 APatch（KernelPatch）root 的无痕 hook 工作流。**脱壳**（eBPF DEX/so dump）见 `android-dex-dump` 技能，本技能只管 hook 与对抗。

> **预载规则（2026-10-12 起）**：游戏包（TP/ACE/tss/反外挂类）与"注入 / 抓包 / 反调试"场景，**开工段即载入本技能**，不等困境——wec 场教训：5 小时 frida-server/TP 对抗，技能里的注入器优先级与对抗姿势全程没用上（存量武器要上战场）。

- 来源：看雪 thread-290304《linux/android 利用 shadow 内存无痕 hook 方法》+ [kkkbbb/rustFrida](https://github.com/kkkbbb/rustFrida) + [kkkbbb/mkpms](https://github.com/kkkbbb/mkpms)（wxshadow/hide-maps）+ [dbcyyds/mkpms-stealth](https://github.com/dbcyyds/mkpms-stealth)（融合模块 + kload/dbc-rw）。
- 核心原理：**读/执行分离**（W^X shadow page）。目标进程读到原始物理页（CRC/MD5/逐字节比对全过），执行的是 shadow 页（BRK 或自定义 patch）。

## 环境清单（已部署，直接用）

| 项 | 值 |
|---|---|
| 设备 | Pixel 6 Pro（raven），Android 16（BP2A.250605.031.A2），内核 6.1.124，槽位 b，bootloader 已解锁 |
| Root 方案 | APatch v11224（KernelPatch 0.13.3），修补版已刷 boot_b |
| PC adb/fastboot | `E:\old\adb\platform-tools\adb.exe`（已入 PATH） |
| 网络代理 | `http://127.0.0.1:7897`（GitHub 直连抽风时 git/curl 加 `-c http.proxy=...` / `-x ...`） |
| 手机端文件 | `/data/local/tmp/`：`wxshadow.kpm`、`wxshadow_client`、`hide-so.kpm`、`rustfrida`、`capture.js`；`/sdcard/Download/` 有 kpm 副本。**开工先校验一遍**（重启/清理后可能丢）： |
| 设备端产出约定 | 工具只放 `/data/local/tmp/`（名单见 `paths.json` → `deviceTools`）；**当轮脚本/日志落 `/data/local/tmp/<目标>-<日期>/` 子目录**；大件 dump/apk 落 `/sdcard/Download/_srchunter/<目标>/`（pull 回 PC 后删设备侧）。**防复发：别把一次性脚本/大文件堆在 tmp 根**（2026-09-29 盘点：tmp 根曾堆 191 项 / 2.8GB ）。 |
| 拉起脚本 | **没有通用 runner，每次按目标现写**（包名 / profile / 日志名一堆写死参数，存下来只会过期）。`capture.js` 缺了从本技能 `scripts\` push：`adb -s <serial> push .opencode\skills\android-stealth-hook\scripts\capture.js /data/local/tmp/` |
| 源码（本机） | `（外部知识库未随包提供）\src\opencode\tools\gh-src\`：`rustFrida/`、`mkpms/`（wxshadow + hide-maps）、`mkpms-stealth/`（融合 + kload + dbc-rw）、`wxshadow/`（6.12 适配版）、`KernelPatch/`、`kpmanager/` |
| root 调用 | `su -c '...'`（不是 `kp`；重启后入口是 su） |

> 设备上的 `hide-so.kpm` 是 hide-maps 系的旧构建；源码最新版是 `mkpms-stealth/kpms/hide-maps`（v1.5，prctl 接口更全）。要升级按 `references/kpm-stealth-stack.md` 重新编。

## 每次开工前：状态检查

```bash
adb devices
adb shell "su -c id"                                   # 必须 uid=0
# ① KPM 开机自载状态（首选，不受 dmesg 轮转影响）——两行 rc: 0 才叫就绪
adb shell 'su -c "grep -i \"KP load kpm\" /data/adb/ap/log/dmesg.log"'
# ② 运行时探针（设备端展开 pid）——能列可执行区域 = KPM 活
adb shell 'su -c "/data/local/tmp/wxshadow_client -p $(pidof system_server) -m"'
# ③（可选）断点实证——下/删探针断点后：dmesg | grep wxshadow 应出现 [set_bp]/[del bp]
```

> **两个坑（都踩过）**：
> ① **引号坑（本环境 shell=pwsh）**：`adb shell "su -c 'dmesg | grep \"...\"'"` 双层双引号会被吃掉内层引号，`su -c` 只拿到 `dmesg`、grep 落到非 root 侧 → **静默返回空，误判成 KPM 没加载**。用外层单引号写法或脚本跑。
> ② **轮转坑（2026-09-29 实锤）**：`dmesg` 是运行时环形缓冲（本机 log_buf_len=1024K），**开机约 1 小时后开机消息被轮转掉**——`dmesg | grep "KP load kpm"` 返回 0 行**不代表 KPM 没加载**（实测：dmesg 最早 142337s vs uptime 145890s → 假阴性）。**开机自载状态一律查 `/data/adb/ap/log/dmesg.log`（APatch 持久化日志）**；只有运行时事件（`[set_bp]`/Breakpoint Hit）才查 dmesg。
> 一键三判据：`pwsh .opencode\skills\android-stealth-hook\scripts\kpm-status.ps1`。

## KPM 持久化（已嵌入内核镜像，重启自动加载）

wxshadow / hide-so 通过**「嵌入」**写进修补镜像，开机由内核自动加载：

| KPM | 加载事件 | 原因 |
|---|---|---|
| hide-so | `pre-kernel-init`（默认） | 不扫 mm，越早越好 |
| **wxshadow** | **`post-init-second-stage`（必须改）** | 初始化要扫描带 mm 的任务，太早全是内核线程会失败 |

验证：`grep -i "KP load kpm" /data/adb/ap/log/dmesg.log` 两行都 `rc: 0`（**开机自载状态查持久化日志；运行时 dmesg 会轮转，勿用它判加载**）；wxshadow 若 `rc: -1` + `failed to detect task_struct offsets` = 事件被改回 pre-kernel-init。

嵌入流程（新增 KPM/重修补）：APatch → KPM 页 → ＋ → 「嵌入」→ 选原厂 boot.img + SuperKey → 附加文件加 kpm 并**逐个设置加载事件** → **必须点「开始修补」**（只改事件不点修补 = 没写进镜像）→ 安装刷入 → 重启验证。嵌入的 KPM 不能动态卸载；「加载」是运行时临时加载，仅调试用。

## 入场 / 转化判据（顺序，不是省略）

**什么时候值得上真机无痕**：常规代理过不去（证书钉死 / 双向校验 / native 网络栈）、或加固/反调试导致常规 frida 起不来、或疑似高危线索藏在 App 内（签名算法、密钥、本地校验、越权入口）。

**顺序（不是"挑便宜的做"）**：默认代理 → **rustFrida + wxshadow（默认即上，不等被检测）**——`Hook.WXSHADOW` + hide-maps + 属性伪装就是主配置；只有"明确无检测 + 单次快速验证"才降用 `Hook.NORMAL`。**加固、反调试、抓不到包都不是"这个目标算了"的理由** —— 它们恰恰是上本技能的触发条件；上了就要把该目标打穿。

**注入器选择（写死，不纠结）**：默认/优先 = **rustFrida + wxshadow**。仅以下情形才动 frida-server：① 需要 frida-python 生态自动化（预言机 / 批量 hook / `lev_test.py` 类脚本——rustFrida 不兼容 frida 协议，只兼容 JS 语法子集）；② 需要 frida-tools 开箱工具（frida-ps / trace / dexdump 等）；③ rustFrida 注入失败兜底（实锤：qnchat 进程 `pwrite EINVAL`）。两套都在固定位（`paths.json` → `toolchain`），不搜磁盘。

**拿到产出 = 转到下一步，不是收工**：拿到 key / IV / 算法 / 明文接口 / 凭据 → **立刻回业务面**（重放 → 改包 → 越权 / 逻辑 / 批量导出），业务面才是终点；逆向做漂亮不算成果。

**产出落 `dig\`**：密钥与解密结果、raw 请求/响应包、hook 日志片段（报告里标 `payload_source: JS 逆向 / 函数名`）。别事后补打 —— 补打要冒二次打点风险。

## 三层无痕（组合拳）

```
① 注入       rustFrida（--spawn / --watch-so）—— 也可换 kload（无 ptrace）
② hook       rustFrida 原生 stealth 模式（Hook.WXSHADOW）或 wxshadow_client 手挂
③ 抹痕       hide-maps（maps/smaps/map_files）+ rustFrida hide_soinfo（solist）
```

### ① 注入：rustFrida

```bash
su -c '/data/local/tmp/rustfrida --spawn com.example.app'             # 启动前注入（能 hook Application.onCreate）
su -c '/data/local/tmp/rustfrida --name com.example.app -l s.js'      # 按包名注入 + JS
su -c '/data/local/tmp/rustfrida --pid 1234'                          # 直注进 REPL
su -c '/data/local/tmp/rustfrida --watch-so libnative.so'             # eBPF 监听 SO 加载自动附加
su -c '/data/local/tmp/rustfrida --server --rpc-port 9191'            # daemon + HTTP RPC 多 session
```

- **保活坑**：`-l script.js` 后 REPL 退出会卸载全部 hook（cut_java_hooks）。无人值守必须保活 stdin —— 直接写进现场那条命令，**不要维护固定 runner 脚本**：
  ```sh
  su -c 'tail -f /dev/null | /data/local/tmp/rustfrida --spawn <包名> --profile <profile> -l /data/local/tmp/capture.js > /data/local/tmp/rf.log 2>&1 &'
  ```
  （`pkill -x rustfrida` 清进程，别用 `pkill -f` 会误杀 adb 命令行）
- **RPC**：`rpc.exports` + `POST /rpc/<sid>/<method>`（legacy 固定 sid=0），`adb forward tcp:9191 tcp:9191` 后本机 curl；同 session 串行、超时 30s、仅同步。
- **属性伪装**：`--dump-props bank` → `--set-prop bank ro.boot.verifiedbootstate=green` … → `--spawn --profile bank`（按进程 profile 比全局 resetprop 稳，防属性区一致性检测）。
- 完整参数、JS API、三种 hook 模式见 `references/rustfrida-reference.md`。

### ② hook：rustFrida stealth 模式（首选）或 wxshadow_client

rustFrida 原生支持把 inline hook 写进 shadow 页（需设备已加载 wxshadow KPM）：

```js
// JS 侧：第三参数 = stealth 模式
hook(addr, function (a0) { return 1; }, Hook.WXSHADOW);          // 1 = shadow 页
Interceptor.attach(addr, { onEnter: fn }, Hook.WXSHADOW);
Java.setStealth(1);                                              // Java hook 也走 shadow
// Hook.NORMAL=0（mprotect 直写，可见）；Hook.RECOMP=2（重编译重定向，需 recomp KPM）
```

手动挂断点/改寄存器（wxshadow_client，全参数见 `references/wxshadow-kpm.md`）：

```bash
su -c '/data/local/tmp/wxshadow_client -p <pid> -m'                          # 列可执行区域
su -c '/data/local/tmp/wxshadow_client -p <pid> -a 0x7b5c001234'             # 下断点
su -c '/data/local/tmp/wxshadow_client -p <pid> -b libc.so -o 0xdb580'       # 库+偏移
su -c '/data/local/tmp/wxshadow_client -p <pid> -a 0x... -r x0=9 -r x1=0x100' # 断点+改寄存器（每断点 ≤4）
su -c '/data/local/tmp/wxshadow_client -p <pid> -a 0x... -d'                 # 删该断点（-d 不带 -a = 删全部）
su -c '/data/local/tmp/wxshadow_client -p <pid> -a 0x... --patch d503201f'   # NOP 等任意 patch（单页内）
su -c '/data/local/tmp/wxshadow_client -p <pid> -a 0x... --read'             # 准备 shadow 页(rw-)
su -c '/data/local/tmp/wxshadow_client -p <pid> -a 0x... --active'           # 激活(--x)，配合写跳板
su -c '/data/local/tmp/wxshadow_client -p <pid> --release'                   # 释放全部 shadow
su -c 'dmesg | grep wxshadow | tail -20'                                     # 命中日志
```

命中标志：`BRK handler ENTER pc=... esr=f2000007` + `Breakpoint Hit` + 寄存器 dump + `single-step enabled`。

**限制（内核 6.1.124 实测）**：自读代码区域下不了断点（同页读+执行死锁）；PATCH 不能跨页；每页最多 128 断点/128 patch、每断点 ≤4 个寄存器修改；断点只捕寄存器不 dump 内存（要看数据用 rustFrida hook 或 READ/ACTIVE 跳板）；App force-stop 后按新 pid 重挂。

符号偏移不知道时读 `references/symbol-resolution.md`（PowerShell 解析 ELF 动态符号表）。

### ③ 抹痕：hide-maps + hide_soinfo

- **hide-maps**（设备上的 hide-so.kpm）：过滤 `/proc/<pid>/maps`、`smaps`、`map_files` 里的路径/关键词/地址区间。做不到：内存扫描、`dl_iterate_phdr`、调用栈、linker solist。
- **rustFrida hide_soinfo**：agent 的 `.init_array` constructor 在 dlopen 时把自身从 linker soinfo 链表摘除（版本无关：解析 `solist_add_soinfo` 机器码推导 next 偏移 + 调 linker 自身 `solist_remove_soinfo`），绕过 V-OS 等 `dl_iterate_phdr` 枚举。Android 7–15 兼容。
- 自检：`cat /proc/$pid/maps | grep -iE 'dbc|rustfrida|libagent'` 期望无输出；`grep TracerPid /proc/$pid/status` 期望 0（kload 路径）。

## 无 ptrace 注入（kload，可选升级）

`stealth.kpm` 融合模块的 kload：内核在目标任意 syscall 返回用户态前改写 `pc=dlopen/x0=path`，目标自己 dlopen——**TracerPid=0**，so 拷到 `code_cache/<随机hex>` 注入后 unlink。

```bash
su -c '/data/local/tmp/stealth_inject -p com.example.app -s /data/local/tmp/libx.so --start --wait-lib libUE4.so --key <SUPERKEY>'
```

失败可回退 tinjector；详见 `references/kpm-stealth-stack.md`。

## App 对抗测试流程（银行/金融类实战，以安徽农金跑通）

1. **环境前置检查（缺一不可）**：
   - **系统时间必须正确**（刷机后无 SIM + Google NTP 被墙能差一年多）→ 时钟错 = HTTPS 证书校验全失败：
     ```bash
     adb shell "su -c 'date -s @<当前UTC秒>'"
     adb shell "su -c 'settings put global ntp_server ntp.aliyun.com; settings put global auto_time 1'"
     ```
   - **captive portal 检测**（Pixel 国内「网络受限」）：
     ```bash
     adb shell "su -c 'settings put global captive_portal_http_url http://connectivitycheck.gstatic.cn/generate_204; settings put global captive_portal_https_url https://www.google.cn/generate_204; settings put global captive_portal_mode 1'"
     ```
     改完 `cmd wifi set-wifi-enabled disabled/enabled` 重连；`dumpsys connectivity | grep "WIFI CONNECTED"` 应有 IS_VALIDATED 无 PARTIAL_CONNECTIVITY。
   - SELinux 拦 App 读 `/proc/net/arp`：`magiskpolicy --live "allow untrusted_app proc_net file { read open getattr }"`
2. **属性伪装**：`--dump-props bank` → 三条 `--set-prop`（verifiedbootstate=green / flash.locked=1 / vbmeta.device_state=locked）→ `--spawn --profile bank`。
3. **注入启动**：现场写一条带保活的命令（见「① 注入」的保活坑示例；capture.js 含 SecretKeySpec/Cipher/MessageDigest/Mac/URL/OkHttp/Apache HttpClient/SSL_write 全套 hook）；Java worker 初始化偶发超时 → 重试一次。
4. **诊断**：`netstat -t` 看 SYN_SENT（被墙）/TIME_WAIT（连上）；`logcat -d -s NetworkMonitor` 区分探测失败（超时=被墙 vs Chain validation failed=时钟错）；172.217.x.x SYN_SENT 是系统 Google 噪音。
5. **全 native 网络栈排查**（Java hook 全空）：拉全部 .so → 扫字符串找 TLS 痕迹（"OpenSSL"/"mbed TLS"/"TLS 1.2"）定位网络库（实测 libhke.so 6.4MB 静态 OpenSSL 零导出）→ 无符号时 jadx 解包找 JNI bridge → 加密嵌套 jar 需脱壳（走 `android-dex-dump`）。
6. **安全键盘定位**：密码控件一般是独立 .so（名含 Keyboard），导出 AES 查表特征（sBox/expandKey/invSBox/Rcon）即实锤；实际加密入口常是 `cipherByKey` 这类带 Key 后缀的重载。wxshadow 断点单步输出服务端可正常解密（无损性可在生产闭环验证）。
7. **抓取脚本模板**：`scripts/capture.js`（本技能目录内）—— 只是模板：按目标的网络栈/加密库改（native/魔改栈要换 hook 点），拉起命令每次现写。

## 故障排查

- **`kp: not found` / root 失效**：手机重启过 → 用 `su -c`；su 也没有就开一次 APatch App
- **`fopen maps: Permission denied`**：客户端没套 root
- **断点设置成功但不命中**：目标地址是冷代码 → 换热函数（mmap/writev）或制造目标进程活动
- **`prctl(RELEASE) failed: ENODATA`**：正常，删断点时 shadow 页已自动释放
- **wxshadow 下断后卡死**：该页有自读（CRC 一类）→ 换页
- **手机变砖/内核异常**：fastboot 刷回原厂 boot（boot_b 槽）；**别降级固件**（bootloader 已抬防回滚计数）

## 参考文件

- `references/rustfrida-reference.md` — rustFrida 全参数 / 三种 hook 模式 / JS API / RPC / 源码地图
- `references/mitm-capture-stack.md` — **mitm 抓包链路**（工具位置 / 端口约定 8899 / 证书 / MIUI 坑 / 标准启动照抄）
- `references/ghidra-bin-analysis.md` — **Ghidra 二进制分析**（纯命令行：analyze 全量导出 / query 增量查询；无需 GUI，不占 8080）
- `references/wxshadow-kpm.md` — wxshadow 内核模块原理、prctl ABI、客户端全参数、版本差异与限制
- `references/kpm-stealth-stack.md` — hide-maps / kload / dbc-rw / stealth.kpm + KernelPatch 基础与部署
- `references/symbol-resolution.md` — libc 符号偏移解析（PowerShell ELF 解析）+ /proc/mem 读隐藏验证
- 源码（本机克隆）：`（外部知识库未随包提供）\src\opencode\tools\gh-src\{rustFrida,mkpms,mkpms-stealth,wxshadow,KernelPatch}`
