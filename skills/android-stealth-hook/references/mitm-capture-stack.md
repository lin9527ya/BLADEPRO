# mitm 抓包链路（PC + 设备）

> 触发：需要 HTTP(S) 抓包（WebView/H5/后台/App 发行层）时。**工具位置与端口都已约定——不要搜磁盘、不要每次现踩。**
> 先例（实录）：wok 战役曾为端口冲突 / 证书 / MIUI 代理折腾大量时间——本页把坑固化。

## 工具位置（唯一真源：`paths.json` 的 `toolchain` 段）

| 工具 | 位置 |
|---|---|
| mitmdump / mitmproxy / mitmweb | `（当前用户目录）`（pip 安装） |
| frida CLI 套件（frida-*.exe） | 同上目录 |
| frida-server（推设备用） | `（外部知识库未随包提供）\src\opencode\tools\frida\`（frida-server / frida-server32 / frida-server32_16） |
| 设备端核心工具 | `/data/local/tmp/`（清单见 `paths.json` → `deviceTools`） |

## 端口约定

- **mitm 默认 8899**；`8080` 是 Burp 常用位——启动前先查：`netstat -ano | Select-String ":8899|:8080"`
- 手机侧代理指向 `8899`

## 链路选择（优先序）

1. **adb reverse（USB，最稳）**：`adb -s <serial> reverse tcp:8899 tcp:8899` → 手机内 `127.0.0.1:8899`
2. **WiFi 直连**：`<PC-IP>:8899`（手机与 PC 同网段）——MIUI 需 `svc wifi disable/enable` 才让代理生效
3. **iptables DNAT**（C#/native 层不走系统代理时）：需 root；注意 v4/v6 双栈与 eBPF 绕过（Android 12+ netfilter 可能拦不住，见 kpm-stealth-stack 记录）

## 证书

- mitm CA 已装系统证书区；证书文件 `/data/local/tmp/myc8750f0d.0`；重装/重放用设备端 `cacerts.sh` / `reapply_ca.sh`
- 换设备或清数据后必须重装，否则 HTTPS 全红

## 标准启动（照抄，按需改 addon）

```powershell
# 1) 端口检查
netstat -ano | Select-String ":8899"
# 2) 后台起 mitm（addon 每次按目标现写；心智参考 hunts\tools\tencent\wok\mitm_log.py：请求行 + body 落盘）
& "（当前用户目录）" -p 8899 -s <addon.py> --set block_global=false
# 3) 链路（USB）
adb -s 1B301FDEE0A8WQ reverse tcp:8899 tcp:8899
# 4) 验证：PC 侧与手机侧各一次
curl.exe -x http://127.0.0.1:8899 http://example.com/ -s -o NUL -w "PC via mitm: %{http_code}`n"
# 5) 手机代理（WiFi 模式才需要）
adb -s 1B301FDEE0A8WQ shell "settings put global http_proxy <PC-IP>:8899"
```

## 坑（wok 实录，先看再踩）

- **8080 被 Burp 占**：mitm 起不来或手机流量进了 Burp → 换 8899，先 netstat
- **批量 pkill 误杀**：杀 mitm 前先 netstat 确认 pid，别横扫 `python*`
- **MIUI 系统代理不生效**：`svc wifi disable/enable` 重启 WiFi 才生效
- **游戏 C#/native 层不走系统代理**：mitm 看不到 → iptables DNAT 或 rustFrida hook 网络层收发出入口
- **addon 用"每请求写文件"**：防进程崩了丢全部记录
- **目标专用脚本落 `hunts\tools\<厂商>\<目标>\`**：进 `_tmp` 会被清理闸删
