# eBPFDexDumper 源码分析（rs 版为主，Go 版对照）

本机克隆：`（外部知识库未随包提供）\src\opencode\tools\gh-src\eBPFDexDumper-rs`（Rust，chinleez）· `...\eBPFDexDumper`（Go，LLeavesG）。

## 一、两个版本怎么选

| 维度 | **rs 版（chinleez，主力）** | Go 版（LLeavesG） |
|---|---|---|
| 语言/产物 | Rust，release 预编译 arm64 | Go，需自带 BTF/vmlinux 编译 |
| 探针模式 | `full` / `lifecycle` / `maps-only` 三档可切 | dump 自动（解释器 + 生命周期），可选 watch |
| 自动修复 | 退出即修复（头/映射/格式/缺失 code_item/回填字节码） | 内置修复（dump/fixso 自动） |
| native so | `dumpso` + `fixso`（段偏移 + JNI 符号注入） | dumpso/fixso（section header 重建、APS2/RELR、watch 解密、ARM32 也支持） |
| 内核要求 | 内置最小 BTF 资产（a12-5.10 / rock5b-5.10），GKI 6.1 实测 | 需要匹配内核的 vmlinux.h/BTF，编译门槛高 |
| 适合 | 快速出货、模式可控 | 复杂 so 场景、跨架构、需要 watch |

## 二、源码地图（rs 版）

| 路径 | 作用 |
|---|---|
| `bpf/bpf.c`（34KB） | eBPF 程序：全部 uprobe/uretprobe 探针 |
| `bpf/header.h` | BPF 侧结构（事件、缓冲） |
| `src/dump.rs`（99KB） | 运行期抓取：探针挂载、事件消费、DEX 落盘、JNI 捕获 |
| `src/fix.rs`（97KB） | 修复/回填管线（头、map、格式、字节码、缺失 code_item、校验） |
| `src/art.rs`（40KB） | ART 偏移解析（hook 目标、RegisterNatives、DexFile 布局） |
| `src/dex.rs` | DEX 解析/校验（魔数、file_size、SHA-1、Adler-32） |
| `src/bytecode.rs` | 方法字节码记录（`_code.json`） |
| `src/platform.rs` | 平台辅助（maps、进程、路径） |
| `build.rs` / `build_android.sh` | 交叉编译（NDK；macOS 需 LLVM clang） |
| `assets/*.btf` | 最小 BTF 资产（内核无 BTF 时兜底） |
| `skills/android-dex-dump/` | 官方自带 PC 端一键脚本（run_dump.py/sh/ps1，full→lifecycle→maps-only 依次降级） |
| `docs/DEFENSIVE_VALIDATION.md` | 防御性验证报告（结果 + 已知问题） |

## 三、eBPF 探针清单（bpf.c，源码实测）

| 探针 | 类型 | 作用 |
|---|---|---|
| `libart_execute` | uprobe | 解释器执行（方法字节码回填主来源） |
| `libart_executeNterpImpl` | uprobe | Nterp 快速解释器 |
| `libart_nterpOpInvoke` | uprobe | Nterp 调用操作 |
| `libart_dexFileCtor` | uprobe | **DexFile 构造（生命周期核心：壳抹头前的完整镜像）** |
| `libart_registerDexFile` | uprobe | DexFile 注册（stripped ROM 内联场景兜底） |
| `libart_verifyClass` | uprobe | 类校验（同兜底路径） |
| `libart_registerNatives` | uprobe | **JNI 动态注册名捕获** |
| `libc_memcpy` / `memmove`（含 uretprobe） | uprobe | 跟踪 DEX/so 拷贝入内存 |
| `libc_mmap` / `mprotect`（含 uretprobe） | uprobe | 跟踪匿名可执行段（自映射 ELF / 解密产物） |

## 四、探针模式（src/dump.rs `ProbeMode`）

- `Full`：解释器 + 生命周期 + libc（`attaches_interpreter()` 为真）→ 能回填字节码，但有卡顿。
- `Lifecycle`：只挂生命周期（`attaches_lifecycle()`）→ 零影响，先拿 DEX。
- `MapsOnly`：不挂 uprobe（`uses_bpf()==false`，要求开启 maps scan）→ 只按内存映射抓，防 uprobe 检测。

## 五、修复管线（src/fix.rs，v0.2.6+）

1. **头边界**：修 `file_size` / `data_off` / `data_size`。
2. **map 列表**：乱序或不在文件尾时重建。
3. **格式字段**：越界的 `debug_info_off` / `tries_size` / `interfaces_off` / `annotations_off` / `static_values_off` 清零。
4. **字节码回填**：把抓到的 method body 写回 code_item。
5. **缺失 code_item**：壳抽掉的方法合成新 code_item（ART 实测可执行：构造/正常/异常路径）。
6. **修后校验**：`DexParser::new` 复检，报告 `validation OK/FAILED`。
7. **并行修复**：多 DEX 并发。
8. 注意：迁移后的旧区段必须清零（非零填充会导致 ART 加载失败，已修 + 回归测试守着）。

## 六、native so（dumpso / fixso）

- `dumpso`：从内存抓 .so，含**自映射匿名 ELF 镜像**；可从 `.dynamic` 段重建完整 section header 表（IDA/Ghidra 直接认符号/导入导出/重定位）。
- 支持 ARM64/ELF64 与 ARM32/ELF32、Android packed 重定位（APS2 / RELR）、watch 运行时解密、按需过滤系统库。
- `fixso --symbols`：把 `dump` 期捕获的 JNI 动态注册名注入 dump 的 .so。

## 七、输出结构

```
<output>/<package>/
├── dex_<pid>_<begin>_<size>.dex      # 原始 DEX（十六进制命名，进程隔离）
├── dex_*_code.json                   # 方法字节码记录
├── repair/                           # 自动修复后的 DEX（推荐）
├── final/                            # 单独跑 fix 的汇总输出
├── native_elf/                       # 匿名 ELF（可选）
└── jni_symbols_*.txt                 # JNI 名（可选）
```

## 八、已知问题（docs/DEFENSIVE_VALIDATION.md + README）

- 部分 API 35 系统 JNI 自动定位不准：用 `--register-natives-offset` 给当前 libart 实测偏移（**不可跨构建套用**；验证报告里用的是 0x885724）。
- 地址/DEX 头/CodeItem 标识都不变的原地改写仍可能漏采。
- 结构检查不是完整字节码验证；重建方法缺少原始异常表/调试信息，寄存器窗口靠推断。
- 双进程同地址：分别保留（去重后合并）；无效 DEX 输入不生成输出。
- `repair` 保留原文件并检查结构与校验和，失败返回非零。
- Go 版另有要求：dump 前删应用 `oat/`（否则 cdex/空结果）；rs 版默认自动处理，`--no-clean-oat` 可关。

## 九、构建（rs 版）

```bash
# Linux/macOS
sh build_android.sh            # → target/aarch64-linux-android/release/eBPFDexDumper
# Windows（有 cargo-ndk）
cargo ndk -t arm64-v8a build --release
# macOS 需 LLVM clang：export CLANG=/opt/homebrew/opt/llvm/bin/clang
# NDK：ANDROID_NDK_HOME / NDK_HOME，或 cargo ndk
```

部署：`adb push target/aarch64-linux-android/release/eBPFDexDumper /data/local/tmp/`；root 下运行。

## 十、PC 端一键脚本（官方自带）

`skills/android-dex-dump/scripts/run_dump.{py,sh,ps1}`：输入 APK 路径，自动 `full → lifecycle → maps-only` 依次降级、pull 结果、写 `report.txt`（需要 adb + apkanalyzer/aapt）。本技能目录里的 `dump_regions.sh` 是设备端辅助（找可疑 rwxp/大匿名段）。
