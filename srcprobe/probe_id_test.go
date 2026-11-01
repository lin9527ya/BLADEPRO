package srcprobe

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func idEnumFixture(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/users/")
		cookie := r.Header.Get("Cookie")
		switch id {
		case "1": // 只有 owner 能读：正常
			if cookie == "owner" {
				_, _ = io.WriteString(w, "obj1")
				return
			}
			w.WriteHeader(http.StatusForbidden)
		case "2": // 两个身份读到同一对象：IDOR 候选
			_, _ = io.WriteString(w, "obj2")
		case "3": // A 能读、B 反而不能：权限倒挂候选
			if cookie == "other" {
				_, _ = io.WriteString(w, "obj3")
				return
			}
			w.WriteHeader(http.StatusForbidden)
		default: // 都读不到
			w.WriteHeader(http.StatusForbidden)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestProbeIDEnumClassifiesCandidates(t *testing.T) {
	srv := idEnumFixture(t)
	res := run(t, Options{
		Target: srv.URL, Kind: KindIDEnum,
		Template: "/api/users/{id}",
		IDs:      []string{"1", "2", "3", "4", "a/b"}, // a/b 字符集非法，应跳过
		HeadersA: map[string]string{"Cookie": "other"},
		HeadersB: map[string]string{"Cookie": "owner"},
	})
	want := map[string]int{
		"tested": 4, "b_only": 1, "same_body": 1, "a_only": 1, "neither": 1,
		"diff_body": 0, "skipped": 1, "failed": 0, "candidates": 2,
	}
	for k, v := range want {
		if res.Metrics[k] != v {
			t.Fatalf("metrics[%s] = %d, want %d（全部：%v）", k, res.Metrics[k], v, res.Metrics)
		}
	}
	if len(res.IDCandidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(res.IDCandidates))
	}
	classes := map[string]bool{}
	for _, c := range res.IDCandidates {
		classes[c.Class] = true
		if c.URL == "" || c.ID == "" {
			t.Fatalf("候选缺字段: %+v", c)
		}
	}
	if !classes["same_body"] || !classes["a_only"] {
		t.Fatalf("候选归类不全: %v", classes)
	}
	if res.Requests != 8 {
		t.Fatalf("requests = %d, want 8（4 个合法 ID × 2 身份）", res.Requests)
	}
}

func TestProbeIDEnumRejectsBadTemplate(t *testing.T) {
	srv := idEnumFixture(t)
	_, err := Run(t.Context(), Options{Target: srv.URL, Kind: KindIDEnum, Template: "/api/users/1", IDs: []string{"1"}, Rate: 1000})
	if err == nil || !strings.Contains(err.Error(), "{id}") {
		t.Fatalf("模板缺少 {id} 应报错，实际: %v", err)
	}
}

func TestProbeIDEnumCapsIDs(t *testing.T) {
	srv := idEnumFixture(t)
	ids := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		ids = append(ids, fmt.Sprintf("%d", 100+i))
	}
	res := run(t, Options{
		Target: srv.URL, Kind: KindIDEnum, Template: "/api/users/{id}", IDs: ids,
		HeadersA: map[string]string{"Cookie": "other"},
		HeadersB: map[string]string{"Cookie": "owner"},
	})
	if !res.Truncated {
		t.Fatal("超过 50 个 ID 时应标记 Truncated")
	}
	if res.Requests != 100 {
		t.Fatalf("requests = %d, want 100（50 个 ID × 2 身份）", res.Requests)
	}
}
