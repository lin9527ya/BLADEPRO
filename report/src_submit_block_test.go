package report

// 提交块（C3）：【提交信息】在 md 与 docx 里必须同源——平台表单是照着它抄的，
// 两个交付件各说各话比没有更糟。

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestSubmitBlockRenderedInBothFormats(t *testing.T) {
	block := "## 【提交信息】\n- 平台：ByteSRC\n- 危害等级：高危\n- 测试源IP：1.2.3.4\n"
	in := SrcInput{
		Title: "越权读取订单", TargetURL: "https://a.example/order", Severity: "high",
		Description: "我拿自己的会话读取了别人的订单。", Impact: "能批量读取他人订单。",
		Fix: "加对象级鉴权。", SubmitBlock: block,
	}

	md := SrcMarkdown(in)
	if !strings.HasPrefix(md, "## 【提交信息】") {
		t.Fatalf("提交块应在 markdown 最前：%q", md[:min(60, len(md))])
	}
	for _, want := range []string{"ByteSRC", "1.2.3.4", "漏洞标题："} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown 缺 %q", want)
		}
	}
	if strings.Index(md, "【提交信息】") > strings.Index(md, "漏洞标题：") {
		t.Fatal("提交块必须排在正文之前")
	}

	blob, err := SrcDOCX(in)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatal(err)
	}
	var doc string
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(rc)
		_ = rc.Close()
		doc = string(raw)
	}
	if doc == "" {
		t.Fatal("docx 缺 document.xml")
	}
	for _, want := range []string{"ByteSRC", "1.2.3.4"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("docx 正文缺提交块字段 %q", want)
		}
	}
	if strings.Index(doc, "1.2.3.4") > strings.Index(doc, "越权读取订单") {
		t.Fatal("docx 里提交块必须排在标题之前")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
