// 银行 App 抓取脚本：加密参数（密钥/IV/加解密内容）+ 网络交互（URL/OkHttp）
// rustFrida QuickJS, Frida 兼容语法

function bytesToHex(b) {
    if (!b) return "(null)";
    var s = "";
    for (var i = 0; i < b.length && i < 128; i++) {
        var h = (b[i] & 0xff).toString(16);
        s += (h.length < 2 ? "0" + h : h);
    }
    return s + (b.length > 128 ? "...(" + b.length + "B)" : "");
}

function printable(b) {
    if (!b) return "(null)";
    var s = "";
    for (var i = 0; i < b.length && i < 96; i++) {
        var c = b[i] & 0xff;
        s += (c >= 0x20 && c < 0x7f) ? String.fromCharCode(c) : ".";
    }
    // 同时给 hex 前缀方便复制
    return '"' + s + '"|hex:' + bytesToHex(b);
}

Java.ready(function () {
    console.log("[*] capture script loaded, installing hooks...");

    // ===== 1. 加密参数 =====
    try {
        var SecretKeySpec = Java.use('javax.crypto.spec.SecretKeySpec');
        SecretKeySpec.$init.overload('[B', 'java.lang.String').implementation = function (key, algo) {
            console.log("[KEY] " + algo + " key=" + bytesToHex(key));
            return this.$init(key, algo);
        };
        console.log("[+] SecretKeySpec hooked");
    } catch (e) { console.log("[!] SecretKeySpec: " + e); }

    try {
        var IvSpec = Java.use('javax.crypto.spec.IvParameterSpec');
        IvSpec.$init.overload('[B').implementation = function (iv) {
            console.log("[IV] " + bytesToHex(iv));
            return this.$init(iv);
        };
        console.log("[+] IvParameterSpec hooked");
    } catch (e) { console.log("[!] IvSpec: " + e); }

    try {
        var Cipher = Java.use('javax.crypto.Cipher');
        Cipher.getInstance.overload('java.lang.String').implementation = function (trans) {
            console.log("[CIPHER] getInstance(" + trans + ")");
            return this.getInstance(trans);
        };
        Cipher.doFinal.overload('[B').implementation = function (input) {
            var out = this.doFinal(input);
            try {
                console.log("[CIPHER doFinal] algo=" + this.getAlgorithm() +
                    "\n    input = " + printable(input) +
                    "\n    output= " + printable(out));
            } catch (e) {
                console.log("[CIPHER doFinal] (algo err) in=" + printable(input) + " out=" + printable(out));
            }
            return out;
        };
        console.log("[+] Cipher hooked");
    } catch (e) { console.log("[!] Cipher: " + e); }

    try {
        var MD = Java.use('java.security.MessageDigest');
        MD.digest.overload('[B').implementation = function (input) {
            var out = this.digest(input);
            console.log("[DIGEST " + this.getAlgorithm() + "] in=" + printable(input) + " out=" + bytesToHex(out));
            return out;
        };
        console.log("[+] MessageDigest hooked");
    } catch (e) { console.log("[!] MD: " + e); }

    try {
        var Mac = Java.use('javax.crypto.Mac');
        Mac.doFinal.overload('[B').implementation = function (input) {
            var out = this.doFinal(input);
            console.log("[MAC " + this.getAlgorithm() + "] in=" + printable(input) + " out=" + bytesToHex(out));
            return out;
        };
        console.log("[+] Mac hooked");
    } catch (e) { console.log("[!] Mac: " + e); }

    // ===== 2. 网络交互 =====
    try {
        var URL = Java.use('java.net.URL');
        URL.$init.overload('java.lang.String').implementation = function (u) {
            if (u && u.indexOf('http') === 0) console.log("[URL] " + u);
            return this.$init(u);
        };
        console.log("[+] URL hooked");
    } catch (e) { console.log("[!] URL: " + e); }

    try {
        var ReqBuilder = Java.use('okhttp3.Request$Builder');
        ReqBuilder.build.implementation = function () {
            var req = this.build();
            try {
                var body = "";
                try {
                    var b = req.body();
                    if (b) {
                        var Buf = Java.use('okio.Buffer');
                        var buf = Buf.$new();
                        b.writeTo(buf);
                        var bs = buf.readByteArray();
                        body = " body=" + printable(bs);
                    }
                } catch (e2) { }
                console.log("[OKHTTP] " + req.method() + " " + req.url().toString() + body);
            } catch (e) { }
            return req;
        };
        console.log("[+] OkHttp Request hooked");
    } catch (e) { console.log("[!] okhttp: " + e); }

    // ===== 2.5 Apache HttpClient（本 App 实际网络栈）=====
    try {
        var HttpPost = Java.use('org.apache.http.client.methods.HttpPost');
        HttpPost.$init.overload('java.lang.String').implementation = function (u) {
            console.log("[HTTP-POST] " + u);
            return this.$init(u);
        };
        var HttpGet = Java.use('org.apache.http.client.methods.HttpGet');
        HttpGet.$init.overload('java.lang.String').implementation = function (u) {
            console.log("[HTTP-GET] " + u);
            return this.$init(u);
        };
        console.log("[+] Apache HttpGet/HttpPost hooked");
    } catch (e) { console.log("[!] apache methods: " + e); }

    try {
        var StringEntity = Java.use('org.apache.http.entity.StringEntity');
        StringEntity.$init.overload('java.lang.String').implementation = function (s) {
            console.log("[POST-BODY] " + s);
            return this.$init(s);
        };
        console.log("[+] StringEntity hooked");
    } catch (e) { console.log("[!] StringEntity: " + e); }

    try {
        var UrlEncEntity = Java.use('org.apache.http.entity.UrlEncodedFormEntity');
        UrlEncEntity.$init.overload('java.util.List').implementation = function (pairs) {
            try {
                var s = "";
                for (var i = 0; i < pairs.size(); i++) {
                    var p = pairs.get(i);
                    s += p.getName() + "=" + p.getValue() + (i < pairs.size() - 1 ? "&" : "");
                }
                console.log("[FORM-BODY] " + s);
            } catch (e2) { }
            return this.$init(pairs);
        };
        console.log("[+] UrlEncodedFormEntity hooked");
    } catch (e) { console.log("[!] UrlEncodedFormEntity: " + e); }

    try {
        var Exec = Java.use('org.apache.http.impl.client.AbstractHttpClient');
        Exec.execute.overload('org.apache.http.client.methods.HttpUriRequest').implementation = function (req) {
            try {
                console.log("[APACHE-EXEC] " + req.getMethod() + " " + req.getURI().toString());
            } catch (e) { }
            return this.execute(req);
        };
        console.log("[+] AbstractHttpClient.execute hooked");
    } catch (e) { console.log("[!] AbstractHttpClient: " + e); }

    // ===== 3. native 层网络兜底：SSL_write 计数（内容靠 Java 层实体抓）=====
    try {
        var sslWrite = Module.findExportByName(null, "SSL_write");
        if (sslWrite) {
            hook(sslWrite, function (a0, a1, a2) {
                console.log("[SSL_write] len=" + (typeof a2 === 'bigint' ? a2 : (a2 && a2.toString ? a2.toString() : a2)));
                return this.$orig();
            });
            console.log("[+] SSL_write hooked @ " + sslWrite);
        } else {
            console.log("[!] SSL_write not found");
        }
    } catch (e) { console.log("[!] SSL_write: " + e); }

    console.log("[*] all hooks installed, waiting for login action...");
});
