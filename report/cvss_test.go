package report

import "testing"

func TestCVSS31BaseScore(t *testing.T) {
	cases := []struct {
		vector string
		score  float64
		sev    string
	}{
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8, "critical"},
		{"CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:H", 8.8, "high"},
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:C/C:L/I:L/A:N", 6.1, "medium"},
		{"CVSS:3.1/AV:N/AC:H/PR:N/UI:R/S:U/C:L/I:N/A:N", 3.1, "low"},
		{"CVSS:3.0/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N", 0, "low"},
	}
	for _, tc := range cases {
		score, normalized, err := CVSS31BaseScore(tc.vector)
		if err != nil {
			t.Fatalf("%s: %v", tc.vector, err)
		}
		if score != tc.score {
			t.Fatalf("%s score=%.1f want %.1f", tc.vector, score, tc.score)
		}
		if got := CVSSSeverity(score); got != tc.sev {
			t.Fatalf("%s severity=%s want %s", tc.vector, got, tc.sev)
		}
		if normalized == "" {
			t.Fatalf("%s: 归一化向量为空", tc.vector)
		}
	}
	if _, _, err := CVSS31BaseScore("CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N"); err == nil {
		t.Fatal("CVSS 4.0 应明确拒绝（当前只算 3.x）")
	}
	if _, _, err := CVSS31BaseScore("CVSS:3.1/AV:N/AC:L"); err == nil {
		t.Fatal("缺基础指标应拒绝")
	}
}

func TestCVSSSeverityAgrees(t *testing.T) {
	if !CVSSSeverityAgrees("high", 8.8) || !CVSSSeverityAgrees("critical", 9.8) {
		t.Fatal("同档应一致")
	}
	if CVSSSeverityAgrees("medium", 9.8) {
		t.Fatal("中危与 9.8 不应一致")
	}
	if CVSSScoreLabel(9.8) != "9.8（严重）" {
		t.Fatalf("label=%q", CVSSScoreLabel(9.8))
	}
}

func TestNormalizeCWEList(t *testing.T) {
	got, err := NormalizeCWEList(" cwe-639, CWE-284,cwe-639 ,")
	if err != nil || got != "CWE-639,CWE-284" {
		t.Fatalf("got %q err=%v", got, err)
	}
	if got, err := NormalizeCWEList(""); err != nil || got != "" {
		t.Fatalf("空 CWE 应放行：%q %v", got, err)
	}
	if _, err := NormalizeCWEList("639"); err == nil {
		t.Fatal("非 CWE-xxx 应拒绝")
	}
}
