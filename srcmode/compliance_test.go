package srcmode

import (
	"strings"
	"testing"
)

func step(role, method, url string) EvidenceStep {
	return EvidenceStep{
		BindingID: 1, Role: role, Method: method, URL: url,
		ReqHead:  method + " /x HTTP/1.1\r\nHost: api.example.com\r\n\r\n",
		RespHead: "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n",
		Status:   200, RespLen: 42, InScope: true, ScopeDecidable: true,
	}
}

func codes(issues []ComplianceIssue) map[string]int {
	out := map[string]int{}
	for _, is := range issues {
		out[is.Code]++
	}
	return out
}

func TestCheckComplianceRequiresEvidence(t *testing.T) {
	got := CheckCompliance("high", nil)
	if c := codes(got); c[ComplianceNoEvidence] != 1 {
		t.Fatalf("没有绑定流量必须报 no_evidence：%+v", got)
	}
	// 低危/信息级不强制 proof，但"完全没证据"仍然不许。
	low := CheckCompliance("low", nil)
	if c := codes(low); c[ComplianceNoEvidence] != 1 {
		t.Fatalf("低危也要有流量才能交：%+v", low)
	}
}

func TestCheckComplianceMediumNeedsProof(t *testing.T) {
	got := CheckCompliance("medium", []EvidenceStep{step("baseline", "GET", "https://api.example.com/x")})
	if c := codes(got); c[ComplianceNoProof] != 1 {
		t.Fatalf("中危只有 baseline 必须报 no_proof：%+v", got)
	}
	ok := CheckCompliance("medium", []EvidenceStep{step("proof", "GET", "https://api.example.com/x")})
	if len(ok) != 0 {
		t.Fatalf("合规证据不该有问题：%+v", ok)
	}
	// 低危只有 baseline：不报 no_proof（但仍是"有证据"）。
	if got := CheckCompliance("low", []EvidenceStep{step("baseline", "GET", "https://a/x")}); len(got) != 0 {
		t.Fatalf("低危 baseline 不该被判不合规：%+v", got)
	}
}

func TestCheckComplianceContentLength(t *testing.T) {
	s := step("proof", "POST", "https://api.example.com/x")
	s.ReqHead = "POST /x HTTP/1.1\r\nHost: api.example.com\r\nContent-Length: 100\r\n\r\n"
	s.ReqLen = 10
	got := CheckCompliance("high", []EvidenceStep{s})
	if c := codes(got); c[ComplianceLengthMismatch] != 1 {
		t.Fatalf("声明 100 实际 10 必须报长度不符：%+v", got)
	}

	// 分块传输不判长度。
	s.ReqHead = "POST /x HTTP/1.1\r\nTransfer-Encoding: chunked\r\nContent-Length: 100\r\n\r\n"
	if got := CheckCompliance("high", []EvidenceStep{s}); len(got) != 0 {
		t.Fatalf("chunked 跳过长度判定：%+v", got)
	}
	// gzip 同理。
	s.ReqHead = "POST /x HTTP/1.1\r\nContent-Encoding: gzip\r\nContent-Length: 100\r\n\r\n"
	if got := CheckCompliance("high", []EvidenceStep{s}); len(got) != 0 {
		t.Fatalf("gzip 跳过长度判定：%+v", got)
	}
	// 声明小于实际：不算"抄包"（可能是 padding），不报。
	s.ReqHead = "POST /x HTTP/1.1\r\nContent-Length: 5\r\n\r\n"
	s.ReqLen = 10
	if got := CheckCompliance("high", []EvidenceStep{s}); len(got) != 0 {
		t.Fatalf("声明小于实际不报：%+v", got)
	}
}

func TestCheckComplianceReplayedRequestID(t *testing.T) {
	a := step("proof", "GET", "https://api.example.com/a")
	b := step("verification", "GET", "https://api.example.com/b")
	b.BindingID = 2
	a.ReqHead += "X-Request-Id: abc-123\r\n"
	b.ReqHead += "X-Request-Id: abc-123\r\n"
	got := CheckCompliance("high", []EvidenceStep{a, b})
	if c := codes(got); c[ComplianceReplayedID] != 1 {
		t.Fatalf("同一 x-request-id 出现在两步必须报重放：%+v", got)
	}
	// 值不同就不报。
	b.ReqHead = strings.ReplaceAll(b.ReqHead, "abc-123", "def-456")
	if got := CheckCompliance("high", []EvidenceStep{a, b}); len(got) != 0 {
		t.Fatalf("请求 id 不同不该报：%+v", got)
	}
}

func TestCheckComplianceDuplicateRequestHash(t *testing.T) {
	a := step("proof", "GET", "https://api.example.com/a")
	b := step("verification", "GET", "https://api.example.com/b")
	b.BindingID = 2
	a.ReqHash, b.ReqHash = "deadbeef", "deadbeef"
	got := CheckCompliance("high", []EvidenceStep{a, b})
	if c := codes(got); c[ComplianceDuplicateReq] != 1 {
		t.Fatalf("两次绑定是同一个请求必须报重复：%+v", got)
	}
}

func TestCheckComplianceScopeAndEmptyResponse(t *testing.T) {
	s := step("proof", "GET", "https://outside.example.net/x")
	s.InScope = false
	s.ScopeDecidable = true
	s.RespHead, s.RespLen, s.Status = "", 0, 0
	got := CheckCompliance("high", []EvidenceStep{s})
	c := codes(got)
	if c[ComplianceOutOfScope] != 1 || c[ComplianceEmptyResponse] != 1 {
		t.Fatalf("范围外 + 空响应都要报：%+v", got)
	}
	// 判定不了范围时不要诬告越界。
	s.ScopeDecidable = false
	if got := CheckCompliance("high", []EvidenceStep{s}); codes(got)[ComplianceOutOfScope] != 0 {
		t.Fatalf("范围不可判定时不该报越界：%+v", got)
	}
}

func TestValidateReportText(t *testing.T) {
	long := strings.Repeat("我用自己的会话读到了别人的订单数据。", 3)
	ok := ValidateReportText("越权读取订单", long, "可读取全部租户的订单数据", "按 tenant_id 校验归属")
	if len(ok) != 0 {
		t.Fatalf("齐备的正文不该有问题：%v", ok)
	}
	cases := []struct {
		name, title, desc, impact, fix, want string
	}{
		{"no title", "", long, "危害", "修复", "标题"},
		{"no description", "t", "", "危害", "修复", "漏洞描述"},
		{"short description", "t", "太短", "危害", "修复", "太短"},
		{"placeholder in desc", "t", long + "（待补）", "危害", "修复", "占位符"},
		{"no impact", "t", long, "", "修复", "漏洞危害"},
		{"no fix", "t", long, "危害", "", "修复"},
		{"todo in fix", "t", long, "危害", "TODO 后面补", "占位符"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateReportText(tc.title, tc.desc, tc.impact, tc.fix)
			joined := strings.Join(got, " | ")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("问题清单 %q 里没有 %q", joined, tc.want)
			}
		})
	}
}

func TestHeadHeaderIsCaseInsensitive(t *testing.T) {
	head := "GET / HTTP/1.1\r\nhost: a.example\r\nContent-Length: 12\r\n\r\n"
	if got := headHeader(head, "HOST"); got != "a.example" {
		t.Fatalf("headHeader HOST = %q", got)
	}
	if n, ok := declaredLength(head); !ok || n != 12 {
		t.Fatalf("declaredLength = %d,%v", n, ok)
	}
	if _, ok := declaredLength("GET / HTTP/1.1\r\n\r\n"); ok {
		t.Fatal("没有 Content-Length 时应 ok=false")
	}
}
