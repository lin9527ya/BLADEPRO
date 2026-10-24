package server

import (
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/traffic"
)

func TestVerifySrcMetricsWarnsOnOverReport(t *testing.T) {
	obs := srcObservedMetrics{Requests: 10, Paths: 3, Methods: 2, JS: 1, SourceMaps: 0, Non404: 2, Retested: 1}

	cases := []struct {
		key      string
		reported map[string]int
		want     string // 空 = 不应告警
	}{
		{"paths_enum", map[string]int{"paths": 214}, "paths=214"},
		{"paths_enum", map[string]int{"paths": 3}, ""},
		{"js_enum", map[string]int{"js_external": 4, "js_chunks": 2}, "JS=6"},
		{"js_enum", map[string]int{"sourcemap": 2}, "sourcemap=2"},
		{"method_matrix", map[string]int{"methods": 7}, "方法=7"},
		{"non404_retest", map[string]int{"non404": 9}, "non404=9"},
		{"non404_retest", map[string]int{"retested": 4}, "已复测=4"},
		{"non404_retest", map[string]int{"non404": 2, "retested": 1}, ""},
		{"screenshots", map[string]int{"anything": 99}, ""},
	}
	for _, tc := range cases {
		got := verifySrcMetrics(tc.key, tc.reported, obs)
		joined := strings.Join(got, "；")
		if tc.want == "" {
			if len(got) != 0 {
				t.Fatalf("%s reported=%v 不该告警：%s", tc.key, tc.reported, joined)
			}
			continue
		}
		if !strings.Contains(joined, tc.want) {
			t.Fatalf("%s reported=%v 应包含 %q，got %q", tc.key, tc.reported, tc.want, joined)
		}
	}
}

func TestSrcAssetHost(t *testing.T) {
	cases := map[string]string{
		"https://api.example.com:8443/x": "api.example.com",
		"api.example.com:443":            "api.example.com",
		"api.example.com":                "api.example.com",
		"10.0.0.1":                       "10.0.0.1",
		"Admin Portal":                   "",
		"":                               "",
	}
	for in, want := range cases {
		if got := srcAssetHost(in); got != want {
			t.Errorf("srcAssetHost(%q)=%q want %q", in, got, want)
		}
	}
}

func TestObservedFromExchanges(t *testing.T) {
	rows := []traffic.ExchangeMeta{
		{Method: "GET", URLTemplate: "/a", URL: "https://x/a", Status: 200, ContentType: "text/html"},
		{Method: "GET", URLTemplate: "/a", URL: "https://x/a", Status: 404, ContentType: "text/html"},
		{Method: "GET", URLTemplate: "/a", URL: "https://x/a", Status: 500, ContentType: "text/html"},
		{Method: "GET", URLTemplate: "/app.js", URL: "https://x/app.js", Status: 200, ContentType: "application/javascript"},
		{Method: "GET", URLTemplate: "/app.js.map", URL: "https://x/app.js.map", Status: 200, ContentType: "application/json"},
	}
	obs := observedFromExchanges(rows)
	if obs.Requests != 5 || obs.Paths != 3 || obs.Methods != 1 {
		t.Fatalf("基础计数不对：%+v", obs)
	}
	if obs.JS != 1 || obs.SourceMaps != 1 {
		t.Fatalf("JS/sourcemap 计数不对：%+v", obs)
	}
	// 非 404：/a 的 200 + 500、app.js、app.js.map 共 4 条；GET /a 出现 2 次 -> retested=1
	if obs.Non404 != 4 || obs.Retested != 1 {
		t.Fatalf("non404/retested 计数不对：%+v", obs)
	}
}
