package srcmode

import (
	"strings"
	"testing"
)

// 交付层（F3）：唯一标识头、标题规则、成品报文校验。
// 这些判定的"真相源"是 src-dig 的 report_validate.py / packet_rules.py。

func TestComplianceChainHeadersNotReported(t *testing.T) {
	// traceparent / b3 / amzn 是链级复用，同一 trace 里多个请求本来就同值：收了会稳定误报。
	steps := []EvidenceStep{
		{BindingID: 1, Role: "proof", Method: "GET", URL: "https://a/x", Status: 200, RespHead: "HTTP/1.1 200 OK\n",
			ReqHead: "GET /x HTTP/1.1\nHost: a\ntraceparent: 00-abc-01\nx-b3-traceid: abc\n"},
		{BindingID: 2, Role: "verification", Method: "GET", URL: "https://a/y", Status: 200, RespHead: "HTTP/1.1 200 OK\n",
			ReqHead: "GET /y HTTP/1.1\nHost: a\ntraceparent: 00-abc-01\nx-b3-traceid: abc\n"},
	}
	for _, is := range CheckCompliance("high", steps) {
		if is.Code == ComplianceReplayedID {
			t.Fatalf("链级头不该被判成重放：%s", is.Detail)
		}
	}
}

func TestCompliancePerRequestHeaderDuplicated(t *testing.T) {
	steps := []EvidenceStep{
		{BindingID: 1, Role: "proof", Method: "GET", URL: "https://a/x", Status: 200, RespHead: "HTTP/1.1 200 OK\n",
			ReqHead: "GET /x HTTP/1.1\nHost: a\nx-tt-logid: 20261023AAAA\n"},
		{BindingID: 2, Role: "verification", Method: "GET", URL: "https://a/y", Status: 200, RespHead: "HTTP/1.1 200 OK\n",
			ReqHead: "GET /y HTTP/1.1\nHost: a\nx-tt-logid: 20261023AAAA\n"},
	}
	found := false
	for _, is := range CheckCompliance("high", steps) {
		if is.Code == ComplianceReplayedID {
			found = true
		}
	}
	if !found {
		t.Fatal("同一个 x-tt-logid 出现在两步里应被拒（这两步是同一个请求）")
	}
}

func TestComplianceTruncatedSignatureHeader(t *testing.T) {
	steps := []EvidenceStep{
		{BindingID: 1, Role: "proof", Method: "GET", URL: "https://a/x", Status: 200, RespHead: "HTTP/1.1 200 OK\n",
			ReqHead: "GET /x HTTP/1.1\nHost: a\nx-medusa: 7f3a9c...\n"},
	}
	found := false
	for _, is := range CheckCompliance("high", steps) {
		if is.Code == ComplianceTruncatedHead {
			found = true
		}
	}
	if !found {
		t.Fatal("签名头以省略号结尾应报截断")
	}

	// 完整长值不报。
	full := strings.Repeat("a1b2c3d4", 12)
	steps[0].ReqHead = "GET /x HTTP/1.1\nHost: a\nx-medusa: " + full + "\n"
	for _, is := range CheckCompliance("high", steps) {
		if is.Code == ComplianceTruncatedHead {
			t.Fatalf("完整签名头不该报：%s", is.Detail)
		}
	}
}

func TestValidateTitle(t *testing.T) {
	cases := []struct {
		title string
		want  string // 期望命中的关键词；空 = 应通过
	}{
		{"某电商订单接口越权读取他人订单", ""},
		{"https://a.com/api/order 未授权读取", "链接"},
		{"/api/order 未授权", "路径开头"},
		{"漏洞 3：订单越权", "编号"},
		{"GET /api/order 越权", "方法"},
		{strings.Repeat("很", 30), "太长"},
	}
	for _, c := range cases {
		issues := ValidateTitle(c.title)
		if c.want == "" {
			if len(issues) != 0 {
				t.Fatalf("%q 应通过，got %v", c.title, issues)
			}
			continue
		}
		joined := strings.Join(issues, "；")
		if !strings.Contains(joined, c.want) {
			t.Fatalf("%q 应命中 %q，got %v", c.title, c.want, issues)
		}
	}
}

func TestCheckReportMarkdown(t *testing.T) {
	ok := "# 标题\n\n```http\nGET /api/x HTTP/1.1\nHost: a.com\nx-request-id: 1\n```\n"
	if issues := CheckReportMarkdown(ok); len(issues) != 0 {
		t.Fatalf("合法报文不该报：%v", issues)
	}

	// 缺 Host。
	noHost := "```http\nGET /api/x HTTP/1.1\nx-request-id: 1\n```\n"
	if issues := CheckReportMarkdown(noHost); len(issues) == 0 || !strings.Contains(issues[0], "Host") {
		t.Fatalf("缺 Host 应报：%v", issues)
	}

	// 整行省略号。
	ellipsis := "```http\nGET /api/x HTTP/1.1\nHost: a\n\n...\n```\n"
	if issues := CheckReportMarkdown(ellipsis); len(issues) == 0 || !strings.Contains(issues[0], "省略号") {
		t.Fatalf("整行省略号应报：%v", issues)
	}

	// 跨块重复的唯一标识头。
	dup := "```http\nGET /a HTTP/1.1\nHost: a\nx-tt-logid: X1\n```\n" +
		"```http\nGET /b HTTP/1.1\nHost: a\nx-tt-logid: X1\n```\n"
	issues := CheckReportMarkdown(dup)
	found := false
	for _, is := range issues {
		if strings.Contains(is, "x-tt-logid") {
			found = true
		}
	}
	if !found {
		t.Fatalf("跨块同一 x-tt-logid 应报：%v", issues)
	}

	// Content-Length 大于正文。
	long := "```http\nPOST /a HTTP/1.1\nHost: a\nContent-Length: 100\n\nabc\n```\n"
	if issues := CheckReportMarkdown(long); len(issues) == 0 || !strings.Contains(issues[0], "Content-Length") {
		t.Fatalf("包长不符应报：%v", issues)
	}

	// 有省略标记时不再比字节数。
	withMarker := "```http\nPOST /a HTTP/1.1\nHost: a\nContent-Length: 100000\n\n" + omissionMarkers[0] + "\n```\n"
	for _, is := range CheckReportMarkdown(withMarker) {
		if strings.Contains(is, "Content-Length") {
			t.Fatalf("带省略标记时不该比字节数：%v", is)
		}
	}
}
