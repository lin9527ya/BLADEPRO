package server

import (
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestSrcAreaForVulnClass(t *testing.T) {
	cases := map[string]string{
		"sql_injection": "injection", "nosql_injection": "injection",
		"command_injection": "rce", "deserialization": "rce", "xxe": "rce",
		"authorization": "authz", "unauth": "authz", "xss": "xss", "ssrf": "ssrf",
		"upload": "upload", "path_traversal": "upload",
		"logic": "logic", "business_logic": "logic", "race_condition": "logic",
		"session": "session", "jwt": "session", "oauth": "session", "default_credentials": "session",
		"information_disclosure": "cred",
		"open_redirect":          "", "cache_poisoning": "",
	}
	for in, want := range cases {
		if got := srcAreaForVulnClass(in); got != want {
			t.Errorf("srcAreaForVulnClass(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSrcAreaROIFromCounts(t *testing.T) {
	counts := []db.SrcClassStatus{
		{VulnClass: "SQL注入", Status: "accepted", Count: 3},
		{VulnClass: "sqli", Status: "ready", Count: 1},
		{VulnClass: "sqli", Status: "rejected", Count: 1},
		{VulnClass: "sqli", Status: "draft", Count: 9}, // 未结算，不计入
		{VulnClass: "path_traversal", Status: "rejected", Count: 2},
		{VulnClass: "open_redirect", Status: "accepted", Count: 2}, // 映射不到面，忽略
	}
	roi := srcAreaROIFromCounts(counts)
	if got := roi["injection"]; got != 1.15 {
		t.Fatalf("injection roi=%v want 1.15", got)
	}
	if got := roi["upload"]; got != 0.5 {
		t.Fatalf("upload roi=%v want 0.5", got)
	}
	if _, ok := roi["other"]; ok {
		t.Fatalf("映射不到的类型不该产生 ROI：%v", roi)
	}
	if got := srcSettledTotal(counts); got != 9 {
		t.Fatalf("settled total=%d want 9", got)
	}
}
