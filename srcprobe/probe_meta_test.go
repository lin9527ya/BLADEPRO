package srcprobe

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mmh3 向量取自 Python mmh3.hash(..., signed=True)，保证 favicon 哈希与 FOFA 口径一致。
func TestMurmurVectors(t *testing.T) {
	cases := []struct {
		in   string
		want int32
	}{
		{"", 0},
		{"abc", -1277324294},
		{"hello", 613153351},
	}
	for _, c := range cases {
		if got := int32(murmur3_32([]byte(c.in), 0)); got != c.want {
			t.Fatalf("murmur3(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestFaviconHashMatchesFofaConvention(t *testing.T) {
	// base64("testicon") = "dGVzdGljb24="，mmh3.hash 得到 -352040107。
	if got := faviconHash([]byte("testicon")); got != -352040107 {
		t.Fatalf("faviconHash = %d, want -352040107", got)
	}
}

func metaFixture(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Server", "nginx")
		w.Header().Set("X-Powered-By", "PHP/8.2")
		_, _ = io.WriteString(w, `<html><head><title>Demo Console</title></head><body>ok</body></html>`)
	})
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "admin")
	})
	mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/missing-xyz", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("testicon"))
	})
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "User-agent: *\nDisallow: /admin\n")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestProbeAliveCounts(t *testing.T) {
	srv := metaFixture(t)
	res := run(t, Options{
		Target: srv.URL, Kind: KindAlive,
		URLs: []string{"/admin", "/missing-xyz", "/boom"},
	})
	if got := res.Metrics["alive"]; got != 4 {
		t.Fatalf("alive = %d, want 4（首页 + 3 条路径）", got)
	}
	if res.Metrics["status_2xx"] != 2 || res.Metrics["status_5xx"] != 1 {
		t.Fatalf("状态分布异常: %v", res.Metrics)
	}
	if res.Metrics["titled"] != 1 {
		t.Fatalf("titled = %d, want 1", res.Metrics["titled"])
	}
	if res.Metrics["failed"] != 0 {
		t.Fatalf("failed = %d, want 0", res.Metrics["failed"])
	}
}

func TestProbeFingerprintCollectsMeta(t *testing.T) {
	srv := metaFixture(t)
	res := run(t, Options{Target: srv.URL, Kind: KindFingerprint})
	for _, key := range []string{"alive", "titled", "favicon", "robots"} {
		if res.Metrics[key] != 1 {
			t.Fatalf("%s = %d, want 1（metrics=%v）", key, res.Metrics[key], res.Metrics)
		}
	}
	if res.Metrics["sitemap"] != 0 {
		t.Fatalf("sitemap = %d, want 0", res.Metrics["sitemap"])
	}
	if res.Metrics["tech"] < 2 {
		t.Fatalf("tech = %d, want >=2（Server + X-Powered-By）", res.Metrics["tech"])
	}
	if res.FaviconMMH3 != "-352040107" {
		t.Fatalf("favicon_mmh3 = %q, want -352040107", res.FaviconMMH3)
	}
	// 首页 + favicon + robots + sitemap，固定路径只对首个 URL 抓一次。
	if res.Requests != 4 {
		t.Fatalf("requests = %d, want 4", res.Requests)
	}
	if !bytes.Contains([]byte(res.Target), []byte("http")) {
		t.Fatalf("target 记录异常: %q", res.Target)
	}
}
