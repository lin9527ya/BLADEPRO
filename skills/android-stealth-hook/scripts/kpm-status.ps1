# kpm-status.ps1 —— KPM 健康检查三判据（PC 端，pwsh）
# 用法：pwsh .opencode\skills\android-stealth-hook\scripts\kpm-status.ps1 [-Serial 1B301FDEE0A8WQ]
# 判据来源：2026-09-29 实锤（dmesg 环形缓冲轮转造成假阴性；开机自载状态一律查 APatch 持久化日志）
param(
  [string]$Serial = "1B301FDEE0A8WQ",
  [string]$Adb = "E:\old\adb\platform-tools\adb.exe"
)

Write-Host "=== ① KPM 开机自载状态（持久化日志 /data/adb/ap/log/dmesg.log，首选） ==="
& $Adb -s $Serial shell 'su -c "grep -i \"KP load kpm\" /data/adb/ap/log/dmesg.log"'

Write-Host ""
Write-Host "=== ② wxshadow_client 区域列表（能列 = KPM 活） ==="
& $Adb -s $Serial shell 'su -c "/data/local/tmp/wxshadow_client -p $(pidof system_server) -m"' | Select-Object -First 6

Write-Host ""
Write-Host "=== ③ dmesg 近期 wxshadow 运行时事件（set_bp / Breakpoint Hit） ==="
& $Adb -s $Serial shell 'su -c "dmesg | grep -iE \"wxshadow|set_bp|Breakpoint\" | tail -6"'

Write-Host ""
Write-Host "提示：判据②③失败时先确认设备端文件在位（/data/local/tmp/wxshadow_client 等）；"
Write-Host "      开机自载状态一律以①为准——运行时 dmesg 会轮转，0 行不是'没加载'。"
