---
name: reverse
description: Android/iOS App 与小程序逆向。脱壳、反编译、协议还原、加密逻辑分析、隐藏接口与调试面发现。Use when analyzing APK/IPA/小程序包、脱壳、frida hook、协议逆向。
origin: quiver
upstream-license: AGPL-3.0-only
---

> 来源：quiver（AGPL-3.0-only），原文件 `skills/reverse/SKILL.md`；移植时改写了工具名与外部路径。

# App / 小程序逆向

## 工作流

### Android (APK)

```bash
# 脱壳（根据壳类型选择）
frida-dexdump -U -f <package>          # frida_dump
frida -U -f <package> -l dump.js        # frida 脱壳脚本

# 反编译
jadx -d out/ app.apk                    # Java 源码
apktool d app.apk -o apktool_out/      # 资源/smali

# 常看点
# AndroidManifest.xml — exported 组件/provider/deeplink
# res/xml/network_security_config.xml — 是否允许明文
# assets/ — 内置配置/证书/接口地址
# .env / config.json — 泄露的 key
```

### iOS (IPA)

```bash
# 砸壳（需越狱/AltStore）
frida-ios-dump                          # frida 砸壳
class-dump -H decrypted.ipa/Payload/*.app/ > headers/  # 头文件
# 后续用 Hopper/IDA/Ghidra 静态分析
```

### 小程序

```bash
# 微信 PC 端缓存路径
%USERPROFILE%\Documents\WeChat Files\Applet\<appid>\
# 解包 wxapkg → 反编译 wxml/wxss/js
```

### Frida Hook

```javascript
// 常用 hook 点
Java.perform(function() {
    // SSL pinning bypass
    var TrustManager = Java.use('javax.net.ssl.X509TrustManager');
    // 加密函数 hook
    var Cipher = Java.use('javax.crypto.Cipher');
    Cipher.doFinal.overload('[B').implementation = function(input) {
        console.log("input: " + bytesToHex(input));
        var result = this.doFinal(input);
        console.log("output: " + bytesToHex(result));
        return result;
    };
});
```

## 产出落库

- 发现的接口 → `record_knowledge(kind=url_pattern)`
- 密钥/token → `record_knowledge(kind=token|cookie)`
- 加密逻辑（算法/key/IV）→ `propose_knowledge(kind=param_rule)`
- 工具链经验 → `propose_knowledge`

## 红线

- 仅逆向授权目标
- 不为分发/破解目的产出成品
- 真实用户数据按最小获取原则
- 中间产物写工作目录，不写 /tmp
