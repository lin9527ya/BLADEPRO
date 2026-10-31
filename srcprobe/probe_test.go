package srcprobe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fixture(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><body><script>var a=1</script><script src="/app.js"></script></body></html>`)
	})
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.WriteString(w, "//# sourceMappingURL=app.js.map\nconsole.log(1)\n")
	})
	mux.HandleFunc("/app.js.map", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"version":3}`)
	})
	mux.HandleFunc("/api/users/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1}`)
	})
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "admin")
	})
	mux.HandleFunc("/allow", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, POST, DELETE")
		_, _ = io.WriteString(w, "ok")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func run(t *testing.T, opts Options) *Result {
	t.Helper()
	if opts.Rate == 0 {
		opts.Rate = 1000
	}
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestProbePaths(t *testing.T) {
	srv := fixture(t)
	res := run(t, Options{Target: srv.URL, Kind: KindPaths, Wordlist: []string{"api/users/1", "admin", "nope"}, MaxPaths: 10})
	if res.Metrics["paths"] != 3 || res.Metrics["non404"] != 2 {
		t.Fatalf("metrics=%v", res.Metrics)
	}
	if res.CountsByStatus["404"] != 1 || len(res.Hits) != 2 {
		t.Fatalf("counts=%v hits=%v", res.CountsByStatus, res.Hits)
	}
	if res.Requests != 3 {
		t.Fatalf("requests=%d", res.Requests)
	}
}

func TestProbeJS(t *testing.T) {
	srv := fixture(t)
	res := run(t, Options{Target: srv.URL + "/", Kind: KindJS})
	if res.Metrics["js_inline"] != 1 || res.Metrics["js_external"] != 1 || res.Metrics["sourcemap"] != 1 {
		t.Fatalf("metrics=%v js=%v", res.Metrics, res.JSURLs)
	}
	if len(res.JSURLs) != 1 || !strings.HasSuffix(res.JSURLs[0], "/app.js") {
		t.Fatalf("js urls=%v", res.JSURLs)
	}
}

func TestProbeMethods(t *testing.T) {
	srv := fixture(t)
	res := run(t, Options{Target: srv.URL, Kind: KindMethods, Endpoints: []string{srv.URL + "/allow"}})
	if res.Metrics["methods"] != 3 {
		t.Fatalf("methods=%d", res.Metrics["methods"])
	}
	notes := strings.Join(res.Notes, "；")
	if !strings.Contains(notes, "Allow 声明：DELETE,GET,POST") {
		t.Fatalf("notes=%q", notes)
	}
	if !strings.Contains(notes, "只读探针") {
		t.Fatalf("缺少只读提示：%q", notes)
	}
}

func TestProbeNon404(t *testing.T) {
	srv := fixture(t)
	res := run(t, Options{Target: srv.URL, Kind: KindNon404, Retest: []string{srv.URL + "/admin", srv.URL + "/nope"}})
	if res.Metrics["non404"] != 1 || res.Metrics["retested"] != 1 {
		t.Fatalf("metrics=%v", res.Metrics)
	}
	if res.Requests != 4 {
		t.Fatalf("requests=%d（每个端点复测 2 次）", res.Requests)
	}
}

func TestProbeRejectsBadTarget(t *testing.T) {
	if _, err := Run(context.Background(), Options{Target: "not-a-url", Kind: KindPaths}); err == nil {
		t.Fatal("非 http/https 目标应拒绝")
	}
}
