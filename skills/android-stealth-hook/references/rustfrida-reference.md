# rustFrida 完整参考（源码分析版）

仓库：`https://github.com/kkkbbb/rustFrida`（★435）· 本机克隆：`（外部知识库未随包提供）\src\opencode\tools\gh-src\rustFrida`
ARM64 Android 动态插桩框架：ptrace 注入 agent.so，QuickJS 脚本、inline hook、Stalker 追踪、Java hook、HTTP RPC。设备上 `/data/local/tmp/rustfrida`，全部要 root 跑。

## 源码地图（要改/要读时按这个找）

| 路径 | 作用 |
|---|---|
| `rust_frida/src/` | 主程序：`main.rs`（入口/REPL）、`args.rs`（参数）、`injection.rs`（注入）、`spawn.rs`（spawn/zymbiote）、`server.rs`（多 session）、`http_rpc.rs`（RPC）、`props.rs`（属性伪装）、`selinux.rs`、`process.rs` |
| `agent/src/` | 注入的 agent：`lib.rs`、`quickjs_loader.rs`（脚本生命周期）、`recompiler.rs`（重编译 hook，prctl 0x5243）、`stalker.rs`/`recompiler.rs`（追踪）、`hide_soinfo.c`（solist 摘除）、`crash_handler.rs`、`raw_thread.rs`、`safepoint.rs`、`linker.rs` |
| `quickjs-hook/` | JS 引擎与 API：`src/jsapi/hook_api/`（三种 stealth 模式）、`jsapi/memory/write.rs`（stealth 写内存） |
| `frida-gum/` + `frida-gum-sys/` | Gum 的 Rust 移植（Interceptor/Stalker/Memory/Module） |
| `ldmonitor*/` | eBPF SO 加载监控（`--watch-so` 的底座） |
| `zymbiote/` | Zygote 载荷：hook `setArgV0`/`selinux_android_setcontext`，App fork 出来时暂停等注入（spawn 模式底层） |
| `qbdi/` + `qbdi-helper/` | 可选指令级 trace（`--features qbdi`） |
| `loader/` | 裸机 ARM64 shellcode（bootstrapper + loader），被主程序 `include_bytes!` 内嵌 |
| `README.md` | 66KB 全量文档（本文件是提炼 + 索引） |

**构建顺序（强依赖）**：`python3 build_helpers.py`（loader）→ `cargo build -p agent --release`（libagent.so）→ `cargo build -p rust_frida --release`（内嵌前三者，单文件产物）。子模块 `quickjs-hook/third_party/tinycc` 供 CModule 用（kkkbbb 的 `rf/cmodule-runtime` 分支）。

## 注入模式

```bash
rustfrida --pid 1234                    # 直注 PID
rustfrida --name com.example.app        # 按进程名
rustfrida --spawn com.example.app       # 启动前注入（zymbiote：hook Zygote，fork 后暂停→注入→恢复）
rustfrida --watch-so libnative.so       # eBPF(ldmonitor) 监听 SO 加载自动附加
rustfrida --server                      # daemon：多 session 并发，server REPL
```

常用参数：

| 参数 | 说明 |
|---|---|
| `-l, --load-script <FILE>` | 注入后执行 JS 脚本 |
| `-t, --timeout <SEC>` | 监听超时（默认无限等） |
| `--connect-timeout <SEC>` | 等 agent 连接超时（默认 10） |
| `-v, --verbose` | 详细注入信息（地址、偏移） |
| `-o, --output <FILE>` | 日志同步写文件 |
| `-s, --string NAME=VALUE` | 覆盖字符串表（sym_name/dlsym_err/cmdline/output_path） |
| `--profile <NAME>` | 属性伪装 profile（spawn/server 模式） |
| `--rpc-port <PORT\|IP:PORT>` | HTTP RPC（见下） |
| `--dump-props/--set-prop/--del-prop/--repack-props` | profile 管理 |

注入后进 REPL：`jsinit` / `jseval <expr>` / `loadjs <script>` / `jsrepl` / `exit`。

**保活**：REPL 退出会 `cut_java_hooks` 卸载全部 hook；无人值守用 `tail -f /dev/null | rustfrida ... -l x.js`。

## 三种 Hook 模式（源码：quickjs-hook/src/jsapi/hook_api）

| 模式 | 值 | 机制 | 可见性 | 依赖 |
|---|---|---|---|---|
| `Hook.NORMAL` | 0（默认） | mprotect 直写跳板 | 读自己代码可见 | 无 |
| `Hook.WXSHADOW` | 1 / `true` | 内核 shadow 页（prctl 0x5758） | `/proc/mem`、CRC 不可见 | wxshadow KPM |
| `Hook.RECOMP` | 2 | 取指经 prctl 重定向到重编译页（原页不动） | 原页完全不动 | recomp KPM（prctl 0x5243） |

```js
hook(target, callback, Hook.WXSHADOW);
Interceptor.attach(target, { onEnter: fn }, Hook.WXSHADOW);
Interceptor.replace(target, replacement, Hook.WXSHADOW);
hookNative(target, cbPtr, data, Hook.WXSHADOW);   // CModule 高频路径
Memory.writeBytes(addr, bytes, 1);                 // stealth=1：wxshadow_patch 写（单页内）
Java.setStealth(1);                                // Java hook 走 shadow（必须在 Java.use().impl 之前）
```

## JS API 速查（与 Frida 兼容子集）

**Native**：`hook(target, cb, stealth?)`、`unhook`、`Interceptor.attach/replace/detachAll`、`CModule(source, symbols?)`、`attachNative/hookNative`、`callNative`（整数/指针 ≤6 参）、`NativeFunction(addr, ret, args)`、`diagAllocNear`。

**Java**：`Java.use`（`method.impl`、`$init`、`$orig`）、`Java.ready(fn)`（spawn 必用）、`Java.choose`（wrapper 只在 onMatch 内有效）、`Java.classLoaders()`、`Java.setStealth`、`Deopt` 接口、Managed DSL（`method.dslImpl = script`，高频 hook 动态编译）。

**其它**：`Module.findExportByName/enumerateSymbols/load`、`Memory.alloc/protect/read*/write*/flushCodeCache`、`ptr()`/NativePointer、`Jni.addr/structs`（如 `Jni.addr("RegisterNatives")` 监控 JNI 注册）、`console`、`File`、`rpc.exports`、QBDI `registerTraceCallbacks()`。

## HTTP RPC

```bash
rustfrida --pid 1234 -l rpc.js --rpc-port 9191          # legacy：sid=0
rustfrida --server --rpc-port 127.0.0.1:9191            # 多 session
adb forward tcp:9191 tcp:9191
curl -X POST http://127.0.0.1:9191/rpc/0/ping
curl -X POST http://127.0.0.1:9191/rpc/0/add -d '[3,4]'
curl http://127.0.0.1:9191/sessions
```

约束：返回值必须 JSON-safe（Java wrapper 要自己 `String()`/组 plain object）；同 session 串行、跨 session 并行；超时 30s；**不支持 async/Promise**。

## 属性伪装（props.rs）

```bash
rustfrida --dump-props default                                  # dump 本机属性到 profile
rustfrida --set-prop default ro.build.fingerprint=google/...
rustfrida --set-prop default ro.debuggable=0
rustfrida --del-prop default ro.kernel.qemu                     # 清零使其不可读
rustfrida --repack-props default                                # 重排消除空洞
rustfrida --spawn com.app --profile default
```

原理：复制 `/dev/__properties__/` 二进制区域到 profile 目录，直接 patch 属性文件（类似 magisk resetprop，但作用于注入 agent 视角）；按进程 profile 比全局改稳（防属性区一致性检测）。

## hide_soinfo（agent/src/hide_soinfo.c）

`.init_array` constructor 在 dlopen 时把自己从 linker soinfo 链表摘除，绕过 `dl_iterate_phdr` 枚举（V-OS 等安全 SDK 的检测点）：
- 解析 `solist_add_soinfo` 机器码自动推导 `soinfo::next` 偏移（不硬编码，兼容 Android 7–15）；
- 调 linker 自身 `solist_remove_soinfo`（正确处理 sonext 尾指针）；
- 约束：`.init_array` 执行时 linker 持 `g_dl_mutex`，不能调 `dl_iterate_phdr` 或再次加锁。
- 与 hide-maps 互补：hide_soinfo 管 solist，hide-maps 管 maps/smaps/map_files。

## 注意事项（官方 + 实测）

- Native 回调 `arguments[0..7]` = x0..x7（BigInt），`this` = 寄存器上下文（`this.x0..x30/sp/pc`、`this.$orig()`）；改参数写 `this.xN` 再 `$orig()`。
- Java 字段访问必须 `.value`；`Java.choose` wrapper 跨回调失效；spawn 模式 Java hook 必须在 `Java.ready` 里；`Java.setStealth` 必须在 `impl` 之前。
- 自修改代码后 `Memory.flushCodeCache(addr, size)` 清 I-cache。
- `--watch-so` 依赖 ldmonitor（eBPF），内核需支持 uprobe；失败时退回 `--spawn` 轮询。
- RECOMP 模式需要单独的 recomp 内核模块（prctl `0x52430001/0x52430002`），设备上没装就别用。

## 与 wxshadow 手挂的分工

- **要改返回值/参数**：优先 `Hook.WXSHADOW`（rustFrida 一把梭，JS 里完成）。
- **要精确观察某条指令命中时的寄存器现场**：`wxshadow_client -a <addr>` + `dmesg`（不写跳板、零侵入）。
- **要写自定义跳板**：`--read`（rw-）→ 通过 `/proc/pid/mem` 写 → `--active`（--x）；跳板不能含读自身所在页的指令（ADRP+LDR 同页会死锁）。
