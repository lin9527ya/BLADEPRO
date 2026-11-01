package server

import (
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/report"
)

func TestRedactSrcRawRequestMasksSecrets(t *testing.T) {
	head := "GET /api/orders?token=query-secret HTTP/1.1\r\nHost: api.example.test\r\nCookie: sid=cookie-secret\r\nAuthorization: Bearer auth-secret\r\nContent-Type: application/json\r\n"
	body := `{"user":"alice","password":"body-secret","token":"body-token"}`
	raw, changed := redactSrcRawRequest(head, body)
	if !changed {
		t.Fatal("credential-bearing request must be marked as changed")
	}
	for _, secret := range []string{"query-secret", "cookie-secret", "auth-secret", "body-secret", "body-token"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("secret %q leaked into external packet:\n%s", secret, raw)
		}
	}
	for _, want := range []string{"Host: api.example.test", redactedValue, srcRedactionMarker} {
		if !strings.Contains(raw, want) {
			t.Fatalf("redacted packet missing %q:\n%s", want, raw)
		}
	}
}

func TestSrcDeliveryTextUsesOverviewNotFullReport(t *testing.T) {
	md := strings.Join([]string{
		"## 概述", "我没登录就读取了别人的订单。",
		"## 影响与危害", "任意用户可以读取他人订单。",
		"## 受影响范围", "订单详情接口",
		"## 修复建议", "按 tenant_id 校验归属。",
		"## 清理与遗留物", "无",
	}, "\n")
	f := &db.DBFinding{Summary: "摘要", Report: md}
	if got := srcDeliveryDescription(f); got != "我没登录就读取了别人的订单。" {
		t.Fatalf("description=%q, want overview only", got)
	}
	if strings.Contains(srcDeliveryDescription(f), "## 概述") || strings.Contains(srcDeliveryDescription(f), "受影响范围") {
		t.Fatalf("description still contains the full reporter report: %q", srcDeliveryDescription(f))
	}
	if got := srcDeliveryImpact(f); got != "任意用户可以读取他人订单。" {
		t.Fatalf("impact=%q", got)
	}
	if got := srcDeliveryFix(f); got != "按 tenant_id 校验归属。" {
		t.Fatalf("fix=%q", got)
	}
	// No overview → fall back to summary, never the long report.
	if got := srcDeliveryDescription(&db.DBFinding{Summary: "一句话", Report: "## 利用过程\n很长的内部报告"}); got != "一句话" {
		t.Fatalf("fallback description=%q", got)
	}
}

func TestSrcSubmissionAssetNormalizesAndRedacts(t *testing.T) {
	for raw, want := range map[string]string{
		"GET https://api.example.test/orders/1": "https://api.example.test/orders/1",
		"api.example.test/orders/1":             "https://api.example.test/orders/1",
		"https://api.example.test/x?token=s":    "https://api.example.test/x?token=%5BREDACTED%5D",
	} {
		if got := srcSubmissionAsset(raw); got != want {
			t.Fatalf("srcSubmissionAsset(%q)=%q want %q", raw, got, want)
		}
	}
	if got := srcSubmissionAsset("GET /orders/1"); got != "" {
		t.Fatalf("relative target must not be submitted as URL: %q", got)
	}
}

func TestSrcExportIssuesFlagsSubmitPlaceholders(t *testing.T) {
	in := report.SrcInput{
		Title:       "某系统订单越权读取",
		Severity:    "high",
		Description: strings.Repeat("我用自己的账号读取到了其他租户的订单详情，服务端只校验登录不校验归属。", 2),
		Impact:      strings.Repeat("任意登录用户可批量读取他人订单与收货信息，造成数据泄露。", 2),
		Fix:         strings.Repeat("在订单详情接口按 tenant_id 校验归属，并补充越权回归测试。", 2),
		SubmitBlock: "## 【提交信息 · 补天】\n- 标题：（待补）\n- 漏洞类型：CWE-639\n",
	}
	issues := srcExportIssues(in)
	if !strings.Contains(strings.Join(issues, "；"), "提交信息缺标题") {
		t.Fatalf("submit placeholder not reported: %v", issues)
	}
}

func TestSrcMarkdownPlainKeepsPacketContent(t *testing.T) {
	md := "漏洞描述：\n```\n# this is a shell comment\nGET / HTTP/1.1\n```\n＃\n修复建议：\n按 tenant_id 校验。\n"
	plain := srcMarkdownPlain(md)
	if !strings.Contains(plain, "# this is a shell comment") || !strings.Contains(plain, "GET / HTTP/1.1") {
		t.Fatalf("plain adapter mangled packet content:\n%s", plain)
	}
	if strings.Contains(plain, "```") || !strings.Contains(plain, "------------------------") {
		t.Fatalf("plain adapter did not normalize fences/separators:\n%s", plain)
	}
}

func TestFilterSrcFindingScreenshots(t *testing.T) {
	node := int64(20)
	f := &db.DBFinding{AssetIDs: []int64{10, 11}, NodeID: &node}
	arts := []db.EvidenceArtifact{
		{ID: 1, AssetID: 10, MIME: "image/png", Filename: "a.png"},
		{ID: 2, AssetID: 11, MIME: "image/jpeg", Filename: "b.jpg"},
		{ID: 3, AssetID: 99, MIME: "image/png", Filename: "other.png"},
		{ID: 4, AssetID: 0, NodeID: 20, MIME: "image/png", Filename: "node.png"},
		{ID: 5, AssetID: 10, MIME: "text/plain", Filename: "notes.txt"},
		{ID: 6, AssetID: 0, NodeID: 0, MIME: "image/png", Filename: "orphan.png"},
	}
	got := filterSrcFindingScreenshots(f, arts)
	ids := make([]int64, 0, len(got))
	for _, a := range got {
		ids = append(ids, a.ID)
	}
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 4 {
		t.Fatalf("filtered screenshot ids=%v, want [1 2 4]", ids)
	}
}
