# Ghidra 二进制分析（so / elf / dll）——纯命令行，三通道

> 触发：native `.so` 深分析（加密算法、签名/协议实现、反调试逻辑、JNI 注册、CodeRegistration 定位）——strings 级别不够时。
> 工具位置唯一真源：`paths.json` → `toolchain`（ghidraAnalyze / ghidraQuery / pyghidraRunner / pyghidraScripts / ghidraProjects[Py]）。
> **纯 headless，无需 GUI**——不占 8080（那是 Burp 的位）；GhidraMCP/GUI 方案已弃用。
> 三通道分工：**① 批量一次导出**（总览）→ **② 快速单点查询** → **③ pyghidra 自由脚本**（深挖首选）。

## ① 首次：分析 + 导出（大文件后台跑）

```powershell
pwsh （外部知识库未随包提供）\src\opencode\tools\ghidra\ghidra-analyze.ps1 -Binary <so路径> -OutDir hunts\dig\<厂商>\<目标>\ghidra\<名字>
```

- 首次 = 导入 + 全量分析 + 导出；**项目持久保存**（`tools\ghidra\projects\w_<名字>`），之后重跑 = 秒级重导出（不重析）
- `-Fresh` 强制删项目重析；耗时：100KB ≈ 10 秒 · 10MB ≈ 1–3 分钟 · **>100MB ≈ 10–60 分钟（后台跑）**
- 产物（OutDir）：`functions.txt`（函数清单）· `decompiled.c`（全量伪代码）· `strings.txt`（字符串表）

## ② 增量查询（同一 so 分析过即可用；每次约 12 秒）

```powershell
pwsh （外部知识库未随包提供）\src\opencode\tools\ghidra\ghidra-query.ps1 -Name <so文件名> -Action decompile -Arg "entry"
#   -Action decompile  -Arg <函数名|地址>   单函数伪代码（找函数名用 search）
#   -Action xrefs      -Arg <函数名>        谁调用了它
#   -Action search     -Arg <关键字>        按函数名搜（列表 + 签名）
#   -Action strings    -Arg <子串>          字符串表搜索
#   -Action imports                         外部符号（库依赖）
```

## ③ pyghidra 脚本通道（深挖首选：Python 驱动，随便写）

环境（已就位）：Ghidra 11.3.2（`D:\ghidra`）+ pyghidra 2.2.1（用户级 pip，入口 `%APPDATA%\Python\Python311\Scripts\pyghidra.exe`）+ JDK21；用户级环境变量 `GHIDRA_INSTALL_DIR=D:\ghidra` 已设（包装脚本再兜底）。

```powershell
# 自带助手：按 函数名(正则)/入口地址 反编译；首次自动分析，之后复用缓存（--fresh 重析）
pwsh （外部知识库未随包提供）\src\opencode\tools\ghidra\pyghidra-run.ps1 （外部知识库未随包提供）\src\opencode\tools\ghidra\py\decompile.py <so路径> <函数名|地址>
pwsh （外部知识库未随包提供）\src\opencode\tools\ghidra\pyghidra-run.ps1 （外部知识库未随包提供）\src\opencode\tools\ghidra\py\decompile.py <so路径> --list --limit 80
```

**写自己的脚本**：放 `tools\ghidra\py\` 下（模板即 `decompile.py`），跑法同上（第一个参数换成你的脚本）。骨架：

```python
import os; os.environ.setdefault("GHIDRA_INSTALL_DIR", r"D:\ghidra")
import pyghidra
with pyghidra.open_program(r"<so路径>",
                           project_location=r"（外部知识库未随包提供）\src\opencode\tools\ghidra\projects-py",
                           project_name="p_<名字>", analyze=False) as flat:  # 已分析过 → False 秒开；首次 True
    program = flat.getCurrentProgram()
    # 想干啥干啥：函数遍历 / 反编译 / 字符串 / 引用 / 数据类型 …
```

常用模式（贴进脚本即用）：

| 目的 | API |
|---|---|
| 反编译函数 | `DecompInterface().openProgram(p)` → `decompileFunction(f, 120, None).getDecompiledFunction().getC()` |
| 按名找函数 | 遍历 `p.getFunctionManager().getFunctions(True)`，取 `f.getName()` / `f.getEntryPoint()` |
| 找 JNI 注册 | 检索 `RegisterNatives` / `JNI_OnLoad` 字符串 + `ReferenceManager` 追引用 |
| 符号 / 导出表 | `p.getSymbolTable().getAllSymbols(True)`；外部依赖 `p.getExternalManager()` |
| 字符串 | `DefinedDataIterator(p.getListing().getDefinedData(True))` 筛 String 值 |
| 交叉引用 | `p.getReferenceManager().getReferencesTo(addr)` |

**典型工作流**：① 全量导出 → 读 `decompiled.c` 定位疑点 → ③ 写针对性 pyghidra 脚本深挖（算法还原 / JNI 注册链 / CodeRegistration 校验）→ 单点快查用 ②。

## ④ IL2CPP 目标专用工作流（Android 手游重灾区）★

IL2CPP 的字符串经 **metadata usage 表**间接引用（usage 槽的值运行时才填入 .bss）——**capstone 静态拼 adrp+ldr 拿不到"槽→字面量"的映射**，别在这上面反复试。正确顺序：

1. **Il2CppDumper** 出 `dump.cs` / `script.json` / `stringliteral.json`（含方法 RVA）
2. **pyghidra 一次性导入 `libil2cpp.so`**（89MB 约 5–15 分钟，**后台跑**；项目缓存进 `ghidraProjectsPy`，之后同一 so 任意函数都是秒级）：
   `pwsh （外部知识库未随包提供）\src\opencode\tools\ghidra\pyghidra-run.ps1 （外部知识库未随包提供）\src\opencode\tools\ghidra\py\decompile.py <libil2cpp.so> <函数名|RVA>`
   （Il2CppDumper 还自带 `il2cpp_header_to_ghidra.py`，可在 Ghidra 里批量套符号与字符串注释——headless 跑法见 ①）
3. 函数引用 ↔ 字面量对照后，回 `dump.cs` 追类结构与调用链

**纪律**：别因为"导入慢"退回 capstone 手拼地址——单函数最小验证可以用 capstone，但"这个函数引用了哪些字符串 / 调用了谁"这类问题，pyghidra 一次解决（tx6 场教训：为省 5–15 分钟导入，capstone 试错 2 轮未果，最后绕道**运行时内存 dump** 才拿到 ConfigDataMgr.LoadCfg 的 section 名与全部键名）。

## 说明

- JDK 已写死 `D:\jdk21`（`launch.properties` 的 `JAVA_HOME_OVERRIDE` + 包装脚本兜底）；系统 PATH 的 java8 不可用于 Ghidra
- 输出先落 `dig\`；`_tmp` 会被清理闸删；大 so 建议后台 + 产物归档
- 脚本源码：headless 双脚本 = `tools\ghidra\scripts\{export_decomp.py, query.py}`（Jython）；pyghidra 通道 = `tools\ghidra\py\*.py` + 包装 `tools\ghidra\pyghidra-run.ps1`
- 迁移到新机：`pip install pyghidra` + 用户级 `GHIDRA_INSTALL_DIR=<ghidra 根>`（要求 Ghidra ≥11.3 + JDK21）
