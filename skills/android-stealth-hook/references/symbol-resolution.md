# 符号偏移解析与读隐藏验证

设备上没有 readelf/nm，在 PC 上解析。两个已实测可用的方法。

## 1. 拿目标库的加载基址

```bash
adb shell "su -c '/data/local/tmp/wxshadow_client -p <pid> -m'" | grep libc.so
# 输出形如：0x0000007d2b70d000 0x0000007d2b7a6000 r-xp  /apex/com.android.runtime/lib64/bionic/libc.so
# 注意：wxshadow_client -b libc.so 自己会解析基址（取 ELF 头那页），手算时 target = maps 里第一行 libc 基址 + offset
```

## 2. 从设备拉库文件，PC 解析 ELF 动态符号表

```bash
adb pull /apex/com.android.runtime/lib64/bionic/libc.so （外部知识库未随包提供）\work\libc.so
```

PowerShell ELF64 dynsym 解析（列出指定符号的 st_value 偏移）：

```powershell
$b = [System.IO.File]::ReadAllBytes('（外部知识库未随包提供）\work\libc.so')
$shoff = [BitConverter]::ToUInt64($b, 0x28)      # e_shoff
$shentsize = [BitConverter]::ToUInt16($b, 0x3A)  # e_shentsize
$shnum = [BitConverter]::ToUInt16($b, 0x3C)      # e_shnum
$target = @('mmap','writev','getuid')            # 想查的符号
for ($i = 0; $i -lt $shnum; $i++) {
    $so = $shoff + $i * $shentsize
    $stype = [BitConverter]::ToUInt32($b, $so + 4)
    if ($stype -ne 11) { continue }              # SHT_DYNSYM
    $symoff = [BitConverter]::ToUInt64($b, $so + 24)   # sh_offset
    $symsize = [BitConverter]::ToUInt64($b, $so + 32)  # sh_size
    $strlink = [BitConverter]::ToUInt32($b, $so + 40)  # sh_link -> .dynstr
    $stroff = $shoff + $strlink * $shentsize
    $strsoff = [BitConverter]::ToUInt64($b, $stroff + 24)
    $n = [int]($symsize / 24)                    # Elf64_Sym = 24 字节
    for ($j = 0; $j -lt $n; $j++) {
        $e = $symoff + $j * 24
        $nameoff = [BitConverter]::ToUInt32($b, $e)
        $val = [BitConverter]::ToUInt64($b, $e + 8)    # st_value
        if ($val -eq 0) { continue }
        $end = $strsoff + $nameoff
        while ($b[$end] -ne 0) { $end++ }
        $name = [System.Text.Encoding]::ASCII.GetString($b, $strsoff + $nameoff, $end - ($strsoff + $nameoff))
        if ($target -contains $name) { Write-Host ($name + ' offset: 0x' + $val.ToString('x')) }
    }
}
```

Pixel 6 Pro / Android 16 bionic libc 实测结果：`mmap=0xdb580`、`writev=0xdacc0`、`getuid=0xd9c40`。

选热函数技巧：`mmap`（分配时触发，中等频率）、`writev`（每次写日志触发，极热）、冷门函数断上不动就换。注意断点设在热函数上日志会刷屏，验证完尽快 `-d` 删掉。

## 3. 读隐藏效果验证（wxshadow 的核心卖点）

断点激活期间，从 `/proc/<pid>/mem` 读断点地址，应该返回**原始指令字节**（而不是 BRK 0xD42000E0）。同时 dmesg 里 BRK 持续命中 —— 读写分离的直接证据。

```bash
# PC 上先算：dec = addr / 4（断点地址必须 4 字节对齐）
# dd 的 skip 单位是 bs=4 的块数
adb shell "su -c 'dd if=/proc/<pid>/mem bs=4 skip=<dec> count=1 2>/dev/null | xxd'"
```

对比基准：PC 上读库文件对应偏移的字节（PowerShell）：

```powershell
$b = [System.IO.File]::ReadAllBytes('（外部知识库未随包提供）\work\libc.so')
'{0:X2} {1:X2} {2:X2} {3:X2}' -f $b[0xdb580],$b[0xdb581],$b[0xdb582],$b[0xdb583]
```

两边一致 = 读隐藏生效；如果读到 `E0 00 20 D4` = BRK#7 指令，说明隐藏失效。

注意 adb shell 里 `$((...))` 算术展开会经过三层引号转义报 `syntax error`，一律在 PC 算好十进制再传。
