package server

import (
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestParseSrcPocHeaders(t *testing.T) {
	raw := "POST /x HTTP/1.1\r\nHost: api.example.com\r\nContent-Length: 5\r\nX-Test: a'b\r\n\r\n"
	hs := parseSrcPocHeaders(raw)
	if len(hs) != 3 || hs[0] != "Host: api.example.com" {
		t.Fatalf("headers=%v", hs)
	}
	curl := srcPocCurlHeaders(hs)
	for _, h := range curl {
		if strings.HasPrefix(strings.ToLower(h), "content-length:") {
			t.Fatalf("curl 不该携带 Content-Length：%v", curl)
		}
	}
}

func TestRenderSrcPoc(t *testing.T) {
	f := &db.DBFinding{ID: 42, Name: "越权读取"}
	steps := []srcPocStep{
		{
			Role: "proof", Method: "GET", URL: "https://api.example.com/user/123?id=1",
			Headers: []string{"Host: api.example.com", "Cookie: sid=abc", "Content-Length: 0"},
		},
		{
			Role: "verification", Method: "POST", URL: "https://api.example.com/order",
			Headers: []string{"Content-Type: application/json"},
			Body:    []byte(`{"id":1}`), BodyName: "step-2.body",
		},
	}
	script := renderSrcPocScript(f, "越权读取", steps, []string{"正文过大"})
	for _, want := range []string{
		"finding #42", "curl -sS -D - --max-time 30 -X 'GET' 'https://api.example.com/user/123?id=1'",
		"-H 'Cookie: sid=abc'", "--data-binary", "step-2.body", "# 注意：正文过大",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script 缺 %q：\n%s", want, script)
		}
	}
	if strings.Contains(script, "Content-Length: 0") {
		t.Fatal("script 不该带 Content-Length")
	}

	httpFile := renderSrcPocHTTP("越权读取", steps)
	for _, want := range []string{"GET https://api.example.com/user/123?id=1 HTTP/1.1", "Host: api.example.com", `{"id":1}`} {
		if !strings.Contains(httpFile, want) {
			t.Fatalf("request.http 缺 %q：\n%s", want, httpFile)
		}
	}

	readme := renderSrcPocReadme(f, "越权读取", steps, []string{"正文过大"})
	for _, want := range []string{"poc.sh", "request.http", "授权范围", "正文过大"} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README 缺 %q：\n%s", want, readme)
		}
	}
}

func TestRenderSrcPocShellQuote(t *testing.T) {
	steps := []srcPocStep{{Role: "proof", Method: "GET", URL: "https://x/a'b", Headers: []string{"X: a'b"}}}
	script := renderSrcPocScript(&db.DBFinding{ID: 1, Name: "x"}, "x", steps, nil)
	if !strings.Contains(script, `https://x/a'\''b`) || !strings.Contains(script, `X: a'\''b`) {
		t.Fatalf("单引号没有正确转义：\n%s", script)
	}
}

func TestSrcPocFlagsMutatingSteps(t *testing.T) {
	steps := []srcPocStep{{Method: "GET", URL: "https://x/"}, {Method: "POST", URL: "https://x/api"}}
	if !srcPocMutating(steps) {
		t.Fatal("POST must mark PoC as mutating")
	}
	if srcPocMutating([]srcPocStep{{Method: "GET"}, {Method: "HEAD"}, {Method: "OPTIONS"}}) {
		t.Fatal("read-only methods must not be marked mutating")
	}
	readme := renderSrcPocReadme(&db.DBFinding{ID: 1}, "x", steps, nil)
	if !strings.Contains(readme, "状态变更请求") || !strings.Contains(readme, "默认不要自动执行") {
		t.Fatalf("mutating warning missing:\n%s", readme)
	}
}
