# KPM 无痕栈：hide-maps / kload / dbc-rw / stealth.kpm + KernelPatch 基础

来源：`kkkbbb/mkpms`（hide-maps 初版）· `dbcyyds/mkpms-stealth`（融合版 v1.1.0：hide-maps + kload + wxshadow + dbc-rw 一次加载）。
本机克隆：`（外部知识库未随包提供）\src\opencode\tools\gh-src\mkpms-stealth`（源码 + 完整 README）。

> 设备上的 `hide-so.kpm` 是 hide-maps 系旧构建；升级用 mkpms-stealth 重编 `stealth.kpm`（推荐只加载**一个**融合模块，别同时 load 独立 hide-maps/kload/wxshadow/dbc）。

## 一、KernelPatch 基础（APatch 侧）

- KPM = 内核模块包，用 `kpatch` 管理：
  ```bash
  kpatch <SUPERKEY> kpm load /data/local/tmp/x.kpm
  kpatch <SUPERKEY> kpm list
  kpatch <SUPERKEY> kpm unload <module-name>
  kpatch <SUPERKEY> kpm ctl0 <module-name> [args...]
  ```
- 加载事件：`pre-kernel-init`（最早，只适合不扫 mm 的模块）、`post-init-second-stage`（有 mm 的任务出现后，扫 task/mm 的模块必须用这个）、以及运行期 `kpm load`。
- APatch「嵌入」= 写进 boot 镜像开机自动加载（不可动态卸载）；「加载」= 运行期临时（调试用）。
- SuperKey 是 APatch 安装时设的；`kpatch` 二进制一般在 `/data/adb/...` 或随 APatch 模块分发。

## 二、hide-maps（maps/smaps/map_files 隐藏）

过滤目标进程的 `/proc/<pid>/maps`、`smaps`、`map_files`：**关键词**命中整行不出现；**地址区间** `[start,end)` 隐藏 VMA（匿名 stub / so 段）；`map_files` 命中区间目录项不出现、`readlink` 返回 `-ENOENT`。

prctl API（`0x484D`）：

| option | 值 | 参数 |
|---|---|---|
| PING | `0x484D0001` | → 返回 `0x484D` |
| ADD_KW | `0x484D0002` | arg1=字符串，加隐藏词（最多 48 个 × 128B） |
| ADD_RANGE | `0x484D0003` | arg1=start, arg2=end（end 不含，最多 96 条） |
| CLR_RANGE | `0x484D0004` | 清区间表 |
| REGISTER | `0x484D0010` | arg1=包名, arg2=so 路径（进程起来自动套用） |
| QUERY | `0x484D0011` | 查登记任务 |
| CLEAR | `0x484D0012` | 清任务（不清 kw/range） |

**做不到的**：内存扫描、`dl_iterate_phdr`、调用栈、linker solist —— solist 要靠 rustFrida 的 hide_soinfo 或 so 自带摘链。

## 三、kload（无 ptrace 注入 so）

```
用户态解析目标 dlopen 地址 → prctl(KLOAD_INJECT, pid, path, dlopen_va)
→ 目标任意 syscall 返回用户态前：内核改写 pc=dlopen, x0=path, x1=RTLD_NOW
→ 目标自己执行 dlopen → constructor
```

prctl API（`0x4B4C`）：PING `0x4B4C0001`；INJECT `0x4B4C0010`(pid, 目标进程内 path 字符串地址, dlopen VA)；STATUS `0x4B4C0011`（0 无/1 等待/2 完成/<0 错）；CLEAR `0x4B4C0012`。

- `TracerPid=0`（无 ptrace）；so 先拷到目标 `code_cache/<随机8位hex>`，注入后 unlink（maps 里最多见 `(deleted)`）。
- path 必须是**写进目标进程**的 C 字符串地址（stealth_inject 用 `/proc/pid/mem` 写）。
- 目标需要较快产生 syscall；dlopen 地址由用户态从 maps+ELF 解析（需同设备同库）。

## 四、dbc-rw（内核跨进程读写）

syscall 41（socket）+ magic `0x1b1841fd2c1e0000`：

| cmd | 含义 | 参数 |
|---|---|---|
| 0 | VERSION | → `0x20260725` |
| 1 | READ | pid, addr, buf, size |
| 2 | WRITE | pid, addr, buf, size |

用户态：`tools/dbc_rw/dbc_rw.h`（`dbc_rw_version/bind/r64/w32/read_pid/module_base`），示例 `example_rw`。走页表读写（mm/pgd 缓存 + 同页 VA→PA 缓存），不依赖 `process_vm_*`。hide-maps 滤掉路径后 `module_base()` 可能找不到，改用绝对地址。

## 五、stealth.kpm（融合）

```bash
kpatch <KEY> kpm load /data/local/tmp/stealth.kpm
# dmesg 期望（刚 load 的运行时事件可查 dmesg；开机自载状态查 /data/adb/ap/log/dmesg.log，勿用运行时 dmesg 判加载）：
#   stealth: [1/4] hide-maps ready
#   stealth: [2/4] kload ready
#   stealth: [3/4] wxshadow ready
#   stealth: [4/4] dbc-rw ready (syscall 41)
kpatch <KEY> kpm ctl0 stealth          # → stealth hm=1 kl=1 wx=1 rw=1
```

控制口转发：`kpatch <KEY> kpm ctl0 stealth hide|kload|wx|rw ...`。

## 六、stealth_inject（一键注入）

```bash
stealth_inject -p <包名> -s <so 绝对路径> [--start] [--wait-lib libUE4.so] [--delay 500] [--key <SUPERKEY>]
# --start 强停并拉起；--wait-lib 等库出现在 maps 再注；--key 自动 load stealth.kpm
# --tinject 强制用 tinjector；--no-tinject kload 失败不回退；--timeout 120
```

流程：登记 hide-maps → 等进程 → stage so 到 `code_cache/<随机hex>` → kload（无 ptrace）→ unlink → 失败回退 tinjector。

## 七、验证与排查

```bash
su -c 'grep -i "KP load kpm" /data/adb/ap/log/dmesg.log'    # KPM 开机自载状态（首选；运行时 dmesg 会轮转，勿用其判加载）
pid=$(su -c 'pidof com.example.app' | awk '{print $1}')
su -c "cat /proc/$pid/maps | grep -iE 'dbc|libx|tinject'"   # 期望无输出
su -c "grep TracerPid /proc/$pid/status"                    # kload 成功 = 0
su -c 'dmesg | grep -E "stealth|kload|hidemaps" | tail -20' # 运行时事件（刚发生）
```

| 现象 | 处理 |
|---|---|
| `kpm load` 失败 | superkey 不对 / 与已加载模块冲突（先 `kpm list`/`unload`） |
| 注入后 maps 仍可见 | hide-maps 未就绪 / 没登记关键词或区间 |
| 游戏启动即崩 | 加 `--wait-lib` + `--delay` |
| kload 一直 pending | 目标少 syscall / dlopen VA 解析错 → `--tinject` 对比 |
| 读写与注入互抢 | 别同时 load 独立 `dbc` 与 `stealth`（会双 hook syscall 41） |
| 单独编模块 | `cmake --build build --target hide-maps.kpm / kload.kpm / wxshadow.kpm / dbc-rw.kpm -j` |

## 八、编译（Linux/WSL）

```bash
git clone --recurse-submodules https://github.com/dbcyyds/mkpms-stealth.git && cd mkpms-stealth
./setup-kp.sh && ln -sfn .kp/kernel kernel     # kernel 必须指向 .kp/kernel
mkdir build && cd build && cmake -DCMAKE_C_COMPILER=aarch64-linux-gnu-gcc ..
cmake --build . --target stealth.kpm -j        # 产物 build/kpms/stealth/stealth.kpm
```

交叉编译器：`aarch64-linux-gnu-gcc`（或 NDK clang）。许可证 GPL v3（衍生作品须同许可开源）。
