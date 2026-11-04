package server

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestHTMLToText(t *testing.T) {
	in := `<html><head><style>.x{color:red}</style><script>alert(1)</script></head>
<body><h1>标题</h1><p>第一段&nbsp;内容</p><div>第二段</div><noscript>忽略</noscript></body></html>`
	got := htmlToText(in)
	for _, want := range []string{"标题", "第一段", "内容", "第二段"} {
		if !strings.Contains(got, want) {
			t.Errorf("htmlToText 缺少 %q：%q", want, got)
		}
	}
	for _, bad := range []string{"alert(1)", ".x{", "忽略", "<p>"} {
		if strings.Contains(got, bad) {
			t.Errorf("htmlToText 未清理 %q：%q", bad, got)
		}
	}
	if !strings.Contains(got, "\n") {
		t.Errorf("块级标签应保留换行：%q", got)
	}
}

func TestFetchURLTextRejectsInternal(t *testing.T) {
	t.Setenv(IngestAllowPrivateEnv, "")
	for _, raw := range []string{
		"http://127.0.0.1:1/",
		"http://[::1]/",
		"http://169.254.169.254/latest/meta-data/",
		"http://192.168.1.1/",
		"file:///etc/passwd",
	} {
		if _, err := fetchURLText(context.Background(), raw); err == nil {
			t.Errorf("%s 应被拒绝", raw)
		}
	}
}

func TestBlockedIngestIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.0.1", "169.254.169.254", "0.0.0.0"}
	for _, s := range blocked {
		if !blockedIngestIP(net.ParseIP(s)) {
			t.Errorf("%s 应视为内网/本机", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if blockedIngestIP(net.ParseIP(s)) {
			t.Errorf("%s 不应被拒绝", s)
		}
	}
}
