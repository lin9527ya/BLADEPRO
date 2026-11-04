# wxshadow KPM —— 原理 / prctl ABI / 客户端 / 版本差异（源码分析版）

W^X Shadow Memory：基于 KernelPatch 的**隐藏断点/Patch**内核模块（ARM64）。
仓库：`kkkbbb/mkpms`（`kpms/wxshadow/`，v1.1.0 起源）· `dbcyyds/mkpms-stealth`（融合版 + 6.6 适配 v1.2.0-k66）· `X14Nuy/wxshadow`（6.12.23 适配）· 本机克隆 `（外部知识库未随包提供）\src\opencode\tools\gh-src\{mkpms,mkpms-stealth,wxshadow}`。

## 一、原理（读/执行分离）

```
NONE → SHADOW_X(--x) ↔ ORIGINAL(r--) ↔ STEPPING(r-x)
                    ↘ DORMANT（hook 退休，保留 shadow）
```

- **Shadow 页**：复制原始代码页 → 在断点处写 `BRK #0x7`（或自定义 patch）→ PTE 设为 `--x`。
- **读隐藏**：进程读该页时 PTE 切回 `r--` 原始页（`/proc/pid/mem`、`process_vm_readv`、ptrace 都读到原指令）。
- **单步恢复**：BRK 命中 → PTE 切回 `r-x` 原始页执行原指令 → 再切回 shadow 继续。
- 结果：**内存完整性校验（CRC/MD5/逐字节）全过**，hook 只存在于执行路径。

## 二、prctl ABI（用户态接口）

| 命令 | 值 | 说明 |
|---|---|---|
| `PR_WXSHADOW_SET_BP` | `0x57580001` | 设隐藏断点 |
| `PR_WXSHADOW_SET_REG` | `0x57580002` | 断点命中时改寄存器（x0–x30/sp，每断点 ≤4 个） |
| `PR_WXSHADOW_DEL_BP` | `0x57580003` | 删断点 |
| `PR_WXSHADOW_SET_TLB_MODE` | `0x57580004` | TLB flush 模式 |
| `PR_WXSHADOW_GET_TLB_MODE` | `0x57580005` | 读当前模式 |
| `PR_WXSHADOW_READ` | `0x57580006` | 创建 shadow 页并置 `rw-`（v1.1 新增；融合版把 PATCH 也用 0x06，按构建确认） |
| `PR_WXSHADOW_ACTIVE` | `0x57580007` | shadow 从 `rw-` 切 `--x` 激活（v1.1 新增） |
| `PR_WXSHADOW_PATCH` | `0x57580006` | 写自定义机器码进 shadow（copy_from_user，单页内） |
| `PR_WXSHADOW_RELEASE` | `0x57580008` | 释放 shadow，恢复原页 |

## 三、客户端全参数（wxshadow_client）

```
-p, --pid <pid>       目标 PID（0 = 自己）
-a, --addr <addr>     虚拟地址（hex；-d/--release 时可省）
-b, --base <lib>      库名（自动找基址）
-o, --offset <off>    相对库基址偏移（hex）
-r, --reg x0=<val>    寄存器修改，可多次（x0-x30/sp）
-d, --delete          删断点（不带 -a = 删全部）
-m, --maps            列可执行区域
    --patch <hex>     向 shadow 写机器码（如 d503201f = NOP）
    --read            准备 shadow 页（rw-）
    --active          激活 shadow（--x）
    --release         释放（不带 -a = 全部）
```

常用机器码：`d503201f` = NOP；`000080d2c0035fd6` = `mov x0,#0; ret`。

## 四、内核 hook 点（源码 wxshadow.c/_handlers.c）

| Hook 目标 | 方式 | 用途 |
|---|---|---|
| `prctl` | syscall hook | 用户态接口分发 |
| `brk_handler` | direct hook | BRK 断点处理（切页+单步） |
| `single_step_handler` | direct hook | 单步执行原指令 |
| `do_page_fault` / `do_mem_abort` | hook | 读隐藏（权限 fault 切页；6.6 用 do_mem_abort） |
| `follow_page_pte` | hook（可选） | GUP 隐藏（process_vm_readv/ptrace 读原页） |
| `copy_process` / `uprobe_dup_mmap` | hook | fork 保护（子进程不继承 shadow） |
| `exit_mmap` | hook | 进程退出清理（5 阶段卸载） |

## 五、版本差异（踩坑前先看）

| 版本 | 关键点 |
|---|---|
| **v1.0.0**（初版） | BRK 隐藏 + 寄存器修改 + 读隐藏 + 单步；无 READ/ACTIVE |
| **v1.1.0** | 新增 READ（rw-）/ACTIVE（--x）/RELEASE，支持写自定义跳板 |
| **v1.2.0-k66**（mkpms-stealth） | 内核 6.6 适配：VMA 扫描弃 `mm->mmap`（maple tree）；`vm_mm` 偏移 0x40→**0x10**；page fault 优先 `do_mem_abort`；FAR 去 TBI/MTE；FSC 0x09–0x0B 按权限 fault 处理 |
| **X14Nuy 6.12.23 适配** | 运行时路径**完全不用 `find_vma`**（6.12 有 `WARN_ON_ONCE(rwsem_is_locked)`；mmap_lock 偏移拿不到）→ 改 `get_user_pte()` + PFN 校验；TLB 用广播 `tlbi vaale1is`；`page->mm` 为权威 owner mm；**THP/2MB 块目标拒绝（-14）**；exit 5 阶段卸载（`g_wxshadow_initialized` 必须 init 末尾置位，否则 hook 悬空 → 下次 syscall 重启） |

**本机设备内核 6.1.124** 用 mkpms 系构建实测可用（见 SKILL.md 限制节）。

## 六、关键限制（硬边界）

- 仅 ARM64；需要 KernelPatch（APatch）框架。
- **自读代码不能下断**：同页「读+执行」会死锁（CRC 自校验场景）→ 换页。
- **PATCH 不能跨页**（`offset + len <= PAGE_SIZE`）。
- 每页最多 128 断点 + 128 patch；每断点最多 4 个寄存器修改。
- 断点只捕获寄存器，不 dump 内存内容（要数据用 rustFrida hook 或 READ/ACTIVE 跳板）。
- fork 子进程不继承 shadow；App force-stop 后 mm 销毁，按新 pid 重挂。
- 6.12 适配版：THP 目标不支持（-14）。

## 七、源码地图 + 测试脚本

| 文件 | 说明 |
|---|---|
| `wxshadow.c` | 模块 init/exit、页生命周期、refcount、prctl 分发 |
| `wxshadow_pgtable.c` | 无锁 PTE walk（get_user_pte）、BBM PTE 写、TLB flush、shadow/original PTE 构造 |
| `wxshadow_handlers.c` | BRK/单步/读写 fault/exit_mmap/GUP/fork 处理 |
| `wxshadow_bp.c` | set_bp/set_reg/patch/release 路径 |
| `wxshadow_scan.c` | 运行时符号与结构偏移扫描 |
| `wxshadow_internal.h` / `wxshadow.h` | 内部/公开头 |
| `wxshadow_client.c` | 用户态客户端 |

`X14Nuy/wxshadow` 自带闭环测试：`run_basic.sh`（寄存器捕获）、`run_regmod.sh`（改寄存器）、`run_multibp.sh`、`run_patch2.sh`、`run_readhide.sh`（/proc/mem 读原页）、`run_guphide.sh`（GUP 读原页）、`run_fork.sh`、`run_repeat.sh`（100/100 稳定性）。可作回归基线。

## 八、部署怪癖

- 某些设备（KernelSU 重定向）`adb push` 直接覆盖 `/data/local/tmp/<file>` 会**静默失败** → 先 push 到 `/sdcard/`，再 `su -c "cp /sdcard/x /data/local/tmp/x"`，`md5sum` 核对。
- 加载：`kpatch <SUPERKEY> kpm load /data/local/tmp/wxshadow.kpm`；卸载 `kpatch <SUPERKEY> kpm unload wxshadow`。
- 日志：开机自载状态查 `/data/adb/ap/log/dmesg.log`（持久化，不受 dmesg 轮转影响）；运行时事件（`[set_bp]` / Breakpoint Hit）查 `dmesg | grep wxshadow`。

## 九、与 rustFrida 的集成

rustFrida 的 `Hook.WXSHADOW`（值 1）直接走这套 prctl 接口写 shadow 页（`Memory.writeBytes(addr, bytes, 1)` 亦然），**无需手写跳板**。要求：wxshadow KPM 已加载（prctl 0x5758 可用）。JS 里用：

```js
hook(addr, function (a0) { return 1; }, Hook.WXSHADOW);
Java.setStealth(1);   // Java hook 也走 shadow
```
