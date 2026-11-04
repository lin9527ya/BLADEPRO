---
name: android-dex-dump
description: Android 加固 App 脱壳（eBPF）：用 eBPFDexDumper-rs 在 ART 生命周期/解释器探针上抓运行期 DEX 与 native .so，自动修复头/映射/缺失 code_item 并回填字节码，恢复 JNI 动态注册名。当 APK 被梆梆/爱加密/乐固等加固、jadx 只能看到壳、需要 dump dex/so 或做内存取证时使用。Hook/对抗/抓包走 android-stealth-hook 技能。**加固/加密嵌套 jar 是触发条件不是跳过理由**：接口打不穿的逻辑就靠它。
origin: quiver
upstream-license: AGPL-3.0-only
---

> 来源：quiver（AGPL-3.0-only），原文件 `skills/android-dex-dump/SKILL.md`；移植时改写了工具名与外部路径。

# Android 脱壳（eBPF：eBPFDexDumper）

加固壳会把 DEX 头抹掉（file_size/map_off/string_ids 清零）后再加载，内存里搜魔数只能搜到壳的索引表。正确路线：**eBPF uprobe 挂在 ART 的 DexFile 生命周期与解释器上，在壳抹头之前/解释执行时抓到完整镜像**。

- 主力工具：[chinleez/eBPFDexDumper-rs](https://github.com/chinleez/eBPFDexDumper-rs)（Rust，★90，release 有预编译 arm64）
- 对照工具：[LLeavesG/eBPFDexDumper](https://github.com/LLeavesG/eBPFDexDumper)（Go，★467，功能全、对 BTF 依赖重）
- 本机克隆：`（外部知识库未随包提供）\src\opencode\tools\gh-src\{eBPFDexDumper-rs,eBPFDexDumper}`（GitHub 走代理 `http://127.0.0.1:7897`）
- 设备端：`/data/local/tmp/eBPFDexDumper`（已部署）；**拉起 App 的稳定注入用 `android-stealth-hook` 技能**（rustFrida + 属性伪装）

## 〇、顺序与转化（不是"值不值得"，是不做完不算完）

**顺序**：接口优先（未授权/越权/输入面先打一轮）→ 壳里打不穿的逻辑再脱壳。**加固、方法抽取、加密嵌套 jar 都是触发条件，不是放弃理由** —— 该脱就脱。

**什么时候停**：拿到目标类的方法体、或密钥/算法用法即转下一步（把算法用到业务面：重放、改包、越权、批量导出）；全量 dump 是为了"找得到"，不是为了"看完"。

**产出落 `dig\`**：结构线索、类名/函数名、密钥用法、加密嵌套 jar 的入口。**二进制（dex/so/apk）不进报告**，清理闸会按"从手机拉的二进制"删掉。

**壳 → 模式速查**（记忆里已实战的几家）：

| 壳 | 首选模式 | 备注 |
|---|---|---|
| 梆梆（Bangcle/fort.andjni） | `lifecycle` | 方法抽取进 `libdexjni.so`，方法体缺口靠逆向 so；`full` 会拖慢启动致壳自杀 |
| 通付盾（trusfort） | `lifecycle` → 需要方法体再 `full` | 见 jshbank 企业 App 记忆 |
| 爱加密 / 乐固 | `lifecycle` 先拿 DEX | 同上，`full` 只在需要回填时上 |
| 只看内存区域 | `maps-only` | 别与 `--no-maps-scan` 同用 |

## 一、探针模式（先选模式再开抓）
| 模式 | 探针 | 性能影响 | 何时用 |
|---|---|---|---|
| `full`（默认） | 解释器 + 生命周期 + libc | 有卡顿 | 要回填字节码、覆盖最全 |
| `lifecycle` | 仅生命周期 | 无影响 | 壳启动超时自杀 / 只需要完整 DEX（**梆梆首选**） |
| `maps-only` | 无 uprobe | 无影响 | App 检测 uprobe（配合 `--no-maps-scan` 会报错，别同时用） |

`full` 的解释器探针会拖慢数倍 → 壳的启动超时保护可能自杀；**先 lifecycle 拿到 DEX，需要方法字节码再上 full**。

## 二、子命令

| 命令 | 用途 |
|---|---|
| `dump` | 抓 DEX + 方法字节码（退出时自动修复，产物在 `repair/`） |
| `fix` | 用 `_code.json` 把字节码回填进 DEX |
| `repair` | 全量修复：头/格式/缺失 code_item |
| `dumpso` | 从内存抓 native .so（含自映射匿名 ELF 镜像） |
| `fixso` | 修 .so 段偏移 + 注入 JNI 符号（IDA/Ghidra 可识别） |
| `offsets` | 检查 ART 布局与 hook 目标偏移 |

常用参数：`-n <包名>`、`-o <输出目录>`、`--probe-mode <full\|lifecycle\|maps-only>`、`--no-clean-oat`（别删 OAT，默认会删）、`--no-maps-scan`、`--register-natives-offset <hex>`（API 35 JNI 自动定位不准时手工给 libart 偏移）。

## 三、标准配方（梆梆/Bangcle 系，已实战验证）

```bash
# 1. 起 eBPF 追踪器（lifecycle 零性能影响），强停旧进程
adb shell "su -c 'pkill -x eBPFDexDumper; rm -rf /data/local/tmp/dex_out; am force-stop <包名>; pkill -x rustfrida; \
  nohup /data/local/tmp/eBPFDexDumper dump -n <包名> -o /data/local/tmp/dex_out --probe-mode lifecycle > /data/local/tmp/ebpf_dump.log 2>&1 &'"

# 2. rustFrida 带属性伪装拉起（App 不自杀是成功前提；裸启动会触发 root 检测退出）
#    现场写一条带保活的命令（没有通用 runner，包名/profile 每次现填）：
adb shell "su -c 'tail -f /dev/null | /data/local/tmp/rustfrida --spawn <包名> --profile <profile> -l /data/local/tmp/capture.js > /data/local/tmp/rf.log 2>&1 &'"

# 3. 让用户翻 App 页面（壳懒加载：每个新页面解密新分片；登录/网络功能页触发加密 jar）

# 4. 收网
adb pull /data/local/tmp/dex_out ./dex_out
```

- 正常停止会排空 JNI 事件队列；再发一次停止信号会中止排空。
- **每次抓取用新目录**（文件名 `dex_<pid>_<begin>_<size>.dex`，进程隔离）。

## 四、失败路径（别再踩）

- **BlackDex**：被 App 进程防护检测。
- **root 裸扫 /proc/mem 找魔数**：梆梆加载后抹 DEX 头，扫到 80 处魔数也没一个可提取。
- **full 模式**：解释器 + oat-clean 拖慢数倍 → 壳启动超时自杀。
- **日志 `process_vm_readv failed for dex 0x... (size≈10MB)`**：App 中途死了没抓全 → 用 rustFrida 稳定拉起重来。
- **原地改写盲区**：地址/DEX 头/CodeItem 标识都不变的原地改写仍可能漏采（已知问题）。

## 五、dump 后处理

1. 与 APK 内 classes.dex **同尺寸**（约 11.8MB）的是壳 stub，跳过；其余按大小排序即真实业务分片。
2. 优先用 `repair/` 目录（自动修复后的产物）；`final/` 只在单独跑 `fix` 时用。
3. 校验：`dex\n` 魔数、header `file_size`、SHA-1、Adler-32；有原 APK 时对比哈希。
4. 加固 App 的加密嵌套 jar（`assets/ftBank_api.jar` 报 zip END header not found）运行期会解密成 dex 被 eBPF 抓到，不用单独处理。
5. 反编译：jadx（本机 `（外部知识库未随包提供）\src\opencode\src-mode\tools\jadx-cli.bat` 包装器；MCP 走 `jadx_cli_*`）。
6. 结构检查通过 ≠ 完整字节码验证；重建方法缺少原始异常表/调试信息，寄存器窗口靠推断（已知问题）。

## 六、与 hook 技能的配合

- **拉起/防自杀**：`android-stealth-hook`（rustFrida `--spawn --profile` + 属性伪装 + 保活）。
- **抓不到包的加密分析**：hook 技能（SSL_write/Cipher/安全键盘）。
- **脱壳产物里找加密嵌套 jar**：本技能 `dumpso` + `fixso --symbols` 恢复 JNI 名后再进 jadx。

## 七、故障排查

| 现象 | 处理 |
|---|---|
| 没产出 DEX | 查 `pidof`、`logcat`、启动器是否崩溃；换 `lifecycle` 重试 |
| API 35 JNI 定位不准 | `--register-natives-offset <libart 实测偏移>`（偏移不跨构建套用） |
| 结果空/cdex | 默认会删 OAT；确认没加 `--no-clean-oat`；或手动删 `/data/app/.../<pkg>/oat/` |
| native SIGSEGV | 记录信号、fault 地址、API level，换 AVD 复现 |
| ART 布局差异 | 换设备/AVD，报告 API level 与检测到的布局 |
| 内核不支持 | 需要 GKI + BTF（uprobe 权限 root）；老内核换 Go 版并带对应 vmlinux.h 重编 |

## 参考文件

- `references/ebpf-dexdumper.md` — 源码分析：eBPF 探针清单、dump/fix/repair 管线、dumpso/fixso、已知问题、构建
- `scripts/dump_regions.sh` — 设备端内存区域扫描（找可疑 rwxp / 大匿名段，辅助定位）
- 源码（本机克隆）：`（外部知识库未随包提供）\src\opencode\tools\gh-src\eBPFDexDumper-rs`（含 `bpf/bpf.c`、`src/dump.rs`、`src/fix.rs`、`src/art.rs`）
