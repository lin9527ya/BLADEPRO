package report

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// P19 同源发现归并：给"同一个漏洞"一个稳定指纹。
//
// 指纹只由两样**可核实**的东西算出来：漏洞类型 + 该发现绑定流量的归一化端点集合。
// 刻意不掺标题/摘要（同一漏洞换措辞重报很常见，掺进去就认不出来了），也刻意不给
// 没有绑定流量的发现算指纹——那样的"指纹"只能靠资产+类型硬凑，会把同一资产上两个
// 不同的洞认成一个。宁可少报"疑似重复"，不可误导人合并。

// EndpointRef 一条绑定流量的端点（方法 + URL）。
type EndpointRef struct {
	Method string
	URL    string
}

// NormalizeEndpoint 归一化一条端点：方法大写、host 小写、去掉默认端口与尾斜杠、
// 查询串只保留**键名**（排序）。
//
// 只留键名是刻意的：`?id=1` 与 `?id=2` 是同一个注入点，而同一条路径上换了参数名
// （`?id=` vs `?q=`）是不同的注入面，必须算成不同的端点。URL 解析失败时退回去空白的
// 原文（不丢样本，只降级归一化强度）。
func NormalizeEndpoint(method, rawURL string) string {
	m := strings.ToUpper(strings.TrimSpace(method))
	raw := strings.TrimSpace(rawURL)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return m + " " + strings.TrimRight(strings.ToLower(raw), "/")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port != "" && !(u.Scheme == "https" && port == "443") && !(u.Scheme == "http" && port == "80") {
		host += ":" + port
	}
	// 对象 ID 折叠：/user/123 与 /user/456 是同一个 IDOR 端点，不该算成两个洞。
	path := normalizePathIDs(strings.TrimRight(u.Path, "/"))
	if path == "" {
		path = "/"
	}
	keys := make([]string, 0, 8)
	for k := range u.Query() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	q := ""
	if len(keys) > 0 {
		q = "?" + strings.Join(keys, ",")
	}
	return m + " " + host + path + q
}

// Fingerprint 是发现的稳定指纹：漏洞类型 + 排序后的归一化端点集合，取 sha256 前 16 位。
// 没有端点（没有绑定流量）时返回空串——调用方据此判断"这条不参与归并"。
func Fingerprint(vulnClass string, endpoints []EndpointRef) string {
	if len(endpoints) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var norm []string
	for _, e := range endpoints {
		n := NormalizeEndpoint(e.Method, e.URL)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		norm = append(norm, n)
	}
	if len(norm) == 0 {
		return ""
	}
	sort.Strings(norm)
	class := CanonicalVulnClass(vulnClass)
	// v2：口径变更（对象 ID 折叠 + 类型受控词表）。前缀让新旧指纹不会半匹配。
	sum := sha256.Sum256([]byte("v2|" + class + "|" + strings.Join(norm, "|")))
	return hex.EncodeToString(sum[:8])
}

var (
	uuidSegRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	hexSegRe  = regexp.MustCompile(`(?i)^[0-9a-f]{16,}$`)
)

// normalizePathIDs 把路径里的对象 ID 折叠成 {id}：纯数字、UUID、长 hex。静态段
// （v2、api2、user-name）保持原样。
func normalizePathIDs(path string) string {
	if path == "" {
		return ""
	}
	segs := strings.Split(path, "/")
	for i, seg := range segs {
		if isDigits(seg) || uuidSegRe.MatchString(seg) || hexSegRe.MatchString(seg) {
			segs[i] = "{id}"
		}
	}
	return strings.Join(segs, "/")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// vulnClassAliases 把常见中英文写法归一到同一个受控类名。键是去掉空格/连字符/
// 下划线后的小写形式。
var vulnClassAliases = map[string]string{
	"sqli": "sql_injection", "sqlinjection": "sql_injection", "sql注入": "sql_injection",
	"nosqli": "nosql_injection", "nosqlinjection": "nosql_injection", "nosql注入": "nosql_injection",
	"idor": "authorization", "越权": "authorization", "越权访问": "authorization",
	"brokenaccesscontrol": "authorization", "unauthorizedaccess": "authorization", "authz": "authorization",
	"xss": "xss", "跨站脚本": "xss", "crosssitescripting": "xss",
	"ssrf": "ssrf", "服务端请求伪造": "ssrf",
	"rce": "rce", "命令执行": "rce", "远程代码执行": "rce", "remotecodeexecution": "rce",
	"codeexecution": "rce",
	"命令注入":          "command_injection", "cmdi": "command_injection", "commandinjection": "command_injection",
	"csrf": "csrf", "跨站请求伪造": "csrf",
	"xxe": "xxe", "xml外部实体": "xxe",
	"lfi": "path_traversal", "pathtraversal": "path_traversal", "文件包含": "path_traversal",
	"任意文件读取": "path_traversal", "目录穿越": "path_traversal", "路径穿越": "path_traversal",
	"upload": "upload", "文件上传": "upload", "arbitraryfileupload": "upload",
	"openredirect": "open_redirect", "开放重定向": "open_redirect",
	"informationdisclosure": "information_disclosure", "infoleak": "information_disclosure",
	"信息泄露": "information_disclosure", "信息泄漏": "information_disclosure",
	"敏感信息泄露": "information_disclosure", "sensitivedataexposure": "information_disclosure",
	"racecondition": "race_condition", "竞态": "race_condition", "竞态条件": "race_condition",
	"businesslogic": "business_logic", "逻辑漏洞": "business_logic", "业务逻辑": "business_logic",
	"defaultcredentials": "default_credentials", "弱口令": "default_credentials",
	"默认口令": "default_credentials", "defaultpassword": "default_credentials",
	"jwt": "jwt", "oauth": "oauth", "graphql": "graphql", "cors": "cors",
	"subdomaintakeover": "subdomain_takeover", "子域接管": "subdomain_takeover",
	"cachepoisoning": "cache_poisoning", "缓存投毒": "cache_poisoning",
	"requestsmuggling": "request_smuggling", "请求走私": "request_smuggling",
	"clickjacking": "clickjacking", "点击劫持": "clickjacking",
	"privilegeescalation": "privilege_escalation", "提权": "privilege_escalation",
	"权限提升":            "privilege_escalation",
	"deserialization": "deserialization", "反序列化": "deserialization",
}

// vulnClassSubstrings 是按特异性排序的兜底规则（精确别名没命中时用）。
var vulnClassSubstrings = []struct{ needle, class string }{
	{"反序列化", "deserialization"},
	{"deserialization", "deserialization"},
	{"提权", "privilege_escalation"},
	{"权限提升", "privilege_escalation"},
	{"命令注入", "command_injection"},
	{"sql注入", "sql_injection"},
	{"sqli", "sql_injection"},
	{"sql injection", "sql_injection"},
	{"nosql", "nosql_injection"},
	{"越权", "authorization"},
	{"idor", "authorization"},
	{"broken access", "authorization"},
	{"unauthorized", "authorization"},
	{"ssrf", "ssrf"},
	{"服务端请求伪造", "ssrf"},
	{"xss", "xss"},
	{"跨站脚本", "xss"},
	{"csrf", "csrf"},
	{"跨站请求伪造", "csrf"},
	{"xxe", "xxe"},
	{"xml外部实体", "xxe"},
	{"路径穿越", "path_traversal"},
	{"目录穿越", "path_traversal"},
	{"任意文件读取", "path_traversal"},
	{"文件包含", "path_traversal"},
	{"path traversal", "path_traversal"},
	{"文件上传", "upload"},
	{"file upload", "upload"},
	{"开放重定向", "open_redirect"},
	{"open redirect", "open_redirect"},
	{"信息泄露", "information_disclosure"},
	{"信息泄漏", "information_disclosure"},
	{"敏感信息", "information_disclosure"},
	{"information disclosure", "information_disclosure"},
	{"竞态", "race_condition"},
	{"race condition", "race_condition"},
	{"逻辑漏洞", "business_logic"},
	{"业务逻辑", "business_logic"},
	{"business logic", "business_logic"},
	{"弱口令", "default_credentials"},
	{"默认口令", "default_credentials"},
	{"default credential", "default_credentials"},
	{"子域接管", "subdomain_takeover"},
	{"subdomain takeover", "subdomain_takeover"},
	{"缓存投毒", "cache_poisoning"},
	{"cache poisoning", "cache_poisoning"},
	{"请求走私", "request_smuggling"},
	{"request smuggling", "request_smuggling"},
	{"点击劫持", "clickjacking"},
	{"clickjacking", "clickjacking"},
	{"jwt", "jwt"},
	{"oauth", "oauth"},
	{"graphql", "graphql"},
	{"cors", "cors"},
}

// CanonicalVulnClass 把自由文本漏洞类型归一到受控类名；未知类型退化成稳定 slug
// （保留中文与字母数字），至少让标点/空格不同的同一种写法能匹配上。
func CanonicalVulnClass(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	compact := strings.NewReplacer(" ", "", "-", "", "_", "", "/", "", "\\", "").Replace(s)
	if v, ok := vulnClassAliases[compact]; ok {
		return v
	}
	for _, r := range vulnClassSubstrings {
		if strings.Contains(s, r.needle) {
			return r.class
		}
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r > 127:
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

// DegradedFingerprint 是"没有绑定流量"时的低置信指纹：受控类型 + 归一化 host/路径
// （对象 ID 已折叠）+ 参数名集合。它只用于"疑似重复"提示，**绝不自动写 dedupe_of**；
// 前缀 d1: 也让调用方一眼看出这不是可精确定案的指纹。
func DegradedFingerprint(vulnClass, rawURL string, paramNames []string) string {
	class := CanonicalVulnClass(vulnClass)
	raw := strings.TrimSpace(rawURL)
	if class == "" || raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	path := normalizePathIDs(strings.TrimRight(u.Path, "/"))
	if path == "" {
		path = "/"
	}
	keys := map[string]bool{}
	for k := range u.Query() {
		keys[k] = true
	}
	for _, p := range paramNames {
		if p = strings.TrimSpace(p); p != "" {
			keys[p] = true
		}
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	sum := sha256.Sum256([]byte("d1|" + class + "|" + host + path + "|" + strings.Join(names, ",")))
	return "d1:" + hex.EncodeToString(sum[:8])
}
