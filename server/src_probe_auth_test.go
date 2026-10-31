package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

func TestSrcProbeUsesAuthorizedIdentityWithoutLeakingIt(t *testing.T) {
	var mu sync.Mutex
	var cookies []string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		cookies = append(cookies, r.Header.Get("Cookie"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	doc := srcmode.DefaultDoc()
	doc.TestIdentities = []srcmode.TestIdentity{{Label: "A", CredentialEnv: "BLADEPRO_TEST_PROBE_COOKIE"}}
	raw, _ := json.Marshal(doc)
	task, err := m.CreateTaskWithOptions("probe-auth", "probe-auth", db.TaskCreateOptions{
		Mode: "src", ModeDoc: raw,
		ScopeRows: []db.TaskScopeInput{{Kind: "ip", Net: "127.0.0.1/32"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.pg.DeleteTask(parseIntDefault(task.ID)) })
	var asset int64
	if err := m.pg.QueryRow(`INSERT INTO assets(type,url,ip) VALUES('service',$1,'127.0.0.1') RETURNING id`, target.URL).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM assets WHERE id=$1`, asset) })
	t.Setenv("BLADEPRO_TEST_PROBE_COOKIE", "session=authorized")
	s := &Server{m: m}
	res, err := s.runSrcProbe(context.Background(), agent.SrcProbeRequest{
		TaskID: parseIntDefault(task.ID), AssetID: asset, Kind: "methods", Identity: "A", Force: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) == 0 {
		t.Fatal("probe did not send the identity cookie")
	}
	for _, c := range cookies {
		if c != "session=authorized" {
			t.Fatalf("unexpected cookie %q", c)
		}
	}
	encoded, _ := json.Marshal(res)
	if strings.Contains(string(encoded), "session=authorized") {
		t.Fatal("credential leaked into probe result")
	}
	if !strings.Contains(string(encoded), "认证身份：A") {
		t.Fatalf("identity note missing: %s", encoded)
	}
}
