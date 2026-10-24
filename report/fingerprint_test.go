package report

import (
	"strings"
	"testing"
)

// 归一化：方法大写、host 小写、默认端口去掉、尾斜杠去掉、查询只留键名（排序）。
func TestNormalizeEndpoint(t *testing.T) {
	cases := []struct{ method, url, want string }{
		{"get", "https://App.Example.com/api/User?id=1", "GET app.example.com/api/User?id"},
		{"GET", "https://app.example.com/api/user/?id=2&name=x", "GET app.example.com/api/user?id,name"},
		{"POST", "http://app.example.com:8080/x", "POST app.example.com:8080/x"},
		{"GET", "https://app.example.com:443/x", "GET app.example.com/x"},
		{"GET", "http://app.example.com:80/", "GET app.example.com/"},
		{"GET", "not a url", "GET not a url"},
		// 对象 ID 折叠：数字 / UUID / 长 hex → {id}；静态段保持原样。
		{"GET", "https://app.example.com/api/user/123?id=1", "GET app.example.com/api/user/{id}?id"},
		{"GET", "https://app.example.com/api/user/550e8400-e29b-41d4-a716-446655440000", "GET app.example.com/api/user/{id}"},
		{"GET", "https://app.example.com/api/order/123/items/456", "GET app.example.com/api/order/{id}/items/{id}"},
		{"GET", "https://app.example.com/static/v2/app.js", "GET app.example.com/static/v2/app.js"},
	}
	for _, c := range cases {
		if got := NormalizeEndpoint(c.method, c.url); got != c.want {
			t.Fatalf("NormalizeEndpoint(%q,%q)=%q want %q", c.method, c.url, got, c.want)
		}
	}
}

// 同一漏洞换个人重报（不同 payload、不同措辞、端点顺序不同）→ 同一指纹；
// 换成别的参数名 / 别的路径 / 别的漏洞类型 → 不同指纹。
func TestFingerprintGroupsSameBug(t *testing.T) {
	a := Fingerprint("sqli", []EndpointRef{{"GET", "https://a.example.com/api/user?id=1"}})
	b := Fingerprint("sqli", []EndpointRef{{"GET", "https://A.example.com/api/user/?id=999"}})
	if a == "" || a != b {
		t.Fatalf("同一注入点应同指纹: %q vs %q", a, b)
	}
	different := []string{
		Fingerprint("sqli", []EndpointRef{{"GET", "https://a.example.com/api/user?q=1"}}),   // 换参数名
		Fingerprint("sqli", []EndpointRef{{"GET", "https://a.example.com/api/order?id=1"}}), // 换路径
		Fingerprint("xss", []EndpointRef{{"GET", "https://a.example.com/api/user?id=1"}}),   // 换类型
		Fingerprint("sqli", []EndpointRef{{"POST", "https://a.example.com/api/user"}}),      // 换方法
	}
	for _, d := range different {
		if d == a {
			t.Fatalf("不同的洞不该同指纹（%q）", d)
		}
	}
	// 多端点：集合相同（顺序不同）→ 同指纹。
	m1 := Fingerprint("sqli", []EndpointRef{{"GET", "https://a/x?id=1"}, {"GET", "https://a/y"}})
	m2 := Fingerprint("sqli", []EndpointRef{{"GET", "https://a/y"}, {"GET", "https://a/x?id=2"}})
	if m1 != m2 {
		t.Fatalf("端点集合相同应同指纹: %q vs %q", m1, m2)
	}
	// 没有端点 → 不算指纹（不参与归并）。
	if got := Fingerprint("sqli", nil); got != "" {
		t.Fatalf("没有绑定流量不该给指纹: %q", got)
	}
}

// TestCanonicalVulnClass 钉住类型受控词表：同一类漏洞的中英文/不同写法要归一到
// 同一个类名，未知类型退化成稳定 slug。
func TestCanonicalVulnClass(t *testing.T) {
	same := [][]string{
		{"sqli", "SQL注入", "SQL Injection", "sql_injection"},
		{"越权", "IDOR", "越权访问", "Broken Access Control"},
		{"XSS", "跨站脚本", "cross site scripting"},
		{"RCE", "命令执行", "远程代码执行"},
	}
	for _, group := range same {
		want := CanonicalVulnClass(group[0])
		if want == "" {
			t.Fatalf("%q 不该归一为空", group[0])
		}
		for _, raw := range group[1:] {
			if got := CanonicalVulnClass(raw); got != want {
				t.Fatalf("CanonicalVulnClass(%q)=%q want %q（同组 %v）", raw, got, want, group)
			}
		}
	}
	if a, b := CanonicalVulnClass("SQL注入"), CanonicalVulnClass("sqli"); a != b {
		t.Fatalf("同一类漏洞必须同指纹: %q vs %q", a, b)
	}
	if got := CanonicalVulnClass("某未知类型!!"); got != "某未知类型" {
		t.Fatalf("未知类型 slug=%q", got)
	}
	if got := CanonicalVulnClass("  "); got != "" {
		t.Fatalf("空类型应返回空串，got %q", got)
	}
}

// TestDegradedFingerprint 钉住无流量时的低置信指纹：对象 ID 不影响，host/路径/
// 参数名变化会影响；前缀 d1: 让调用方一眼看出不能精确定案。
func TestDegradedFingerprint(t *testing.T) {
	a := DegradedFingerprint("SQL注入", "https://api.example.com/user/123?id=1", []string{"name"})
	b := DegradedFingerprint("sqli", "https://api.example.com/user/999?id=2", []string{"name"})
	if a == "" || a != b {
		t.Fatalf("同类型同路径应同降级指纹: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "d1:") {
		t.Fatalf("降级指纹应带 d1: 前缀：%q", a)
	}
	for _, other := range []string{
		DegradedFingerprint("xss", "https://api.example.com/user/1", nil),
		DegradedFingerprint("sqli", "https://other.example.com/user/1", nil),
		DegradedFingerprint("sqli", "https://api.example.com/order/1", nil),
		DegradedFingerprint("sqli", "https://api.example.com/user/1", []string{"other"}),
	} {
		if other == a {
			t.Fatalf("不同 host/路径/类型/参数不该同降级指纹：%q", other)
		}
	}
	if got := DegradedFingerprint("sqli", "", nil); got != "" {
		t.Fatalf("没有 URL 不该给降级指纹：%q", got)
	}
	if got := DegradedFingerprint("", "https://api.example.com/user/1", nil); got != "" {
		t.Fatalf("没有类型不该给降级指纹：%q", got)
	}
}
