package server

import (
	"context"
	"github.com/lin9527ya/bladepro/db"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestBrowserRelayChecksEveryRequestAgainstFrozenTask(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy identity leaked to target")
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	task, err := m.CreateTaskWithOptions("relay", "relay", db.TaskCreateOptions{Mode: "src", ScopeRows: []db.TaskScopeInput{{Kind: "ip", Net: "127.0.0.1/32"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.pg.DeleteTask(parseIntDefault(task.ID))
	s := &Server{m: m}
	addr, closeRelay, err := s.srcBrowserProxy(context.Background(), parseIntDefault(task.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer closeRelay()
	proxy, _ := url.Parse("http://" + addr)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxy)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("authorized target status=%d", resp.StatusCode)
	}
	resp, err = client.Get("http://not-authorized.example/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("out-of-scope target status=%d", resp.StatusCode)
	}
}
