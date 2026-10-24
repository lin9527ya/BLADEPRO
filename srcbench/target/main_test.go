package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newTarget(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	h, err := NewHandlerWithRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, root
}

func get(t *testing.T, base, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestTargetContainsGoldenBugs 是金标靶场的自检：每个"金标"漏洞必须真的存在，
// 否则 benchmark 的 recall 分母是假的。
func TestTargetContainsGoldenBugs(t *testing.T) {
	srv, _ := newTarget(t)

	if code, body := get(t, srv.URL, "/health"); code != 200 || !strings.Contains(body, "ok") {
		t.Fatalf("health: %d %q", code, body)
	}
	// IDOR：不登录也能读 bob。
	if code, body := get(t, srv.URL, "/api/users/2"); code != 200 || !strings.Contains(body, "bob@example.test") {
		t.Fatalf("idor: %d %q", code, body)
	}
	// 未授权管理面。
	if code, body := get(t, srv.URL, "/admin"); code != 200 || !strings.Contains(body, "Admin Panel") {
		t.Fatalf("unauth admin: %d %q", code, body)
	}
	// 反射 XSS：payload 原样出现（未转义）。
	if code, body := get(t, srv.URL, "/search?q=<script>alert(1)</script>"); code != 200 || !strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("reflected xss: %d %q", code, body)
	}
	// 路径穿越：../secret.txt 能跳出 files/。
	if code, body := get(t, srv.URL, "/download?file=../secret.txt"); code != 200 || !strings.Contains(body, "BENCH-SECRET-42") {
		t.Fatalf("path traversal: %d %q", code, body)
	}
	// 开放重定向。
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.Get(srv.URL + "/redirect?to=https://evil.example/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "evil.example") {
		t.Fatalf("open redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// 缓存投毒：X-Forwarded-Host 进入可缓存页面。
	req, _ := http.NewRequest("GET", srv.URL+"/cache?key=a", nil)
	req.Header.Set("X-Forwarded-Host", "evil.example")
	cresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cbody, _ := io.ReadAll(cresp.Body)
	cresp.Body.Close()
	if !strings.Contains(string(cbody), "evil.example") || !strings.Contains(cresp.Header.Get("Cache-Control"), "public") {
		t.Fatalf("cache poisoning: %q %q", cresp.Header.Get("Cache-Control"), cbody)
	}
	// SSRF：能代取 loopback 上的服务。
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "INTERNAL-ECHO")
	}))
	defer echo.Close()
	if code, body := get(t, srv.URL, "/proxy?url="+url.QueryEscape(echo.URL)); code != 200 || !strings.Contains(body, "INTERNAL-ECHO") {
		t.Fatalf("ssrf: %d %q", code, body)
	}
}
