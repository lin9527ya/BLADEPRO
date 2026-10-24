package main

import (
	"strings"
	"testing"
)

func testTarget() Target {
	return Target{
		Name:    "local-vuln",
		BaseURL: "http://127.0.0.1:18080",
		Golden: []Golden{
			{VulnClass: "authorization", Endpoint: "GET /api/users/{id}", Severity: "high"},
			{VulnClass: "xss", Endpoint: "GET /search?q", Severity: "medium"},
			{VulnClass: "open_redirect", Endpoint: "GET /redirect?to", Severity: "low"},
		},
	}
}

func TestScoreMatching(t *testing.T) {
	findings := []Finding{
		{VulnClass: "IDOR", Endpoints: []string{"GET http://127.0.0.1:18080/api/users/2"}, Severity: "high", Status: "confirmed", Title: "越权读用户"},
		{VulnClass: "跨站脚本", Assets: []AssetRef{{Label: "https://127.0.0.1:18080/search?q=1"}}, Severity: "medium", Status: "confirmed"},
		{VulnClass: "open_redirect", Endpoints: []string{"/redirect?to=https://evil.example/"}, Severity: "low"},
		{VulnClass: "information_disclosure", Endpoints: []string{"/backup"}, Severity: "low"},
		{VulnClass: "authorization", Endpoints: []string{"/api/users/1"}, Severity: "high", Status: "false_positive"},
		{VulnClass: "xss", Endpoints: []string{"/search?q=2"}, Severity: "medium"}, // 同洞重复 -> FP
	}
	res := score(testTarget(), findings)
	if res.TP != 3 || res.FP != 2 || res.FN != 0 {
		t.Fatalf("tp/fp/fn=%d/%d/%d, matches=%+v", res.TP, res.FP, res.FN, res.Matches)
	}
	if res.Precision != 0.6 || res.Recall != 1 || res.Severity != 3 || res.SeverityN != 3 {
		t.Fatalf("metrics=%+v", res)
	}
	if len(res.Matches) != 3 || *res.Matches[0].SeverityOK != true {
		t.Fatalf("matches=%+v", res.Matches)
	}
}

func TestScoreMissedGolden(t *testing.T) {
	findings := []Finding{
		{VulnClass: "IDOR", Endpoints: []string{"/api/users/2"}, Severity: "high"},
		{VulnClass: "xss", Endpoints: []string{"/search?q=1"}, Severity: "high"},
	}
	res := score(testTarget(), findings)
	if res.TP != 2 || res.FN != 1 || res.Recall < 0.66 || res.Recall > 0.67 {
		t.Fatalf("tp/fn/recall=%d/%d/%.2f", res.TP, res.FN, res.Recall)
	}
	if len(res.Missed) != 1 || res.Missed[0].VulnClass != "open_redirect" {
		t.Fatalf("missed=%+v", res.Missed)
	}
	// xss 报成 high，金标 medium -> 等级不一致。
	if res.Severity != 1 || res.SeverityN != 2 {
		t.Fatalf("severity=%d/%d", res.Severity, res.SeverityN)
	}
}

func TestApplySummaryRates(t *testing.T) {
	res := Result{TP: 4}
	applySummary(&res, SrcSummary{CellsTotal: 40, CellsTerminal: 10, ChecksTotal: 8, ChecksDone: 3, GapsTotal: 30}, 4000)
	if res.CoverageRate != 0.25 || res.CheckRate != 0.375 || res.TokensPerTP != 1000 {
		t.Fatalf("summary metrics=%+v", res)
	}
}

func TestRenderMarkdown(t *testing.T) {
	res := score(testTarget(), []Finding{{VulnClass: "IDOR", Endpoints: []string{"/api/users/2"}, Severity: "high"}})
	applySummary(&res, SrcSummary{CellsTotal: 10, CellsTerminal: 5, ChecksTotal: 8, ChecksDone: 4}, 1000)
	md := renderMarkdown(res)
	for _, want := range []string{"# SRC 金标评测", "Precision / Recall / F1", "覆盖格完成率", "收尾闸完成率", "Token / 每条命中", "漏报"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown 缺 %q：\n%s", want, md)
		}
	}
}

func TestNormalizeGoldenEndpoint(t *testing.T) {
	got := normalizeGoldenEndpoint("http://127.0.0.1:18080", "GET /api/users/{id}")
	if got != "GET 127.0.0.1:18080/api/users/{id}" {
		t.Fatalf("got %q", got)
	}
}
