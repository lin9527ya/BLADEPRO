package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

func TestIdentityDiffComparesAuthorizedReadsWithoutLeakingSecrets(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "tenant-a-secret" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"tenant":"a","id":1}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer target.Close()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	doc := srcmode.DefaultDoc()
	doc.TestIdentities = []srcmode.TestIdentity{
		{Label: "A", Role: "tenant-a", Tenant: "a", CredentialEnv: "BLADEPRO_TEST_IDENTITY_A"},
		{Label: "B", Role: "tenant-b", Tenant: "b", CredentialEnv: "BLADEPRO_TEST_IDENTITY_B"},
	}
	raw, _ := json.Marshal(doc)
	task, err := m.CreateTaskWithOptions("identity", "identity", db.TaskCreateOptions{
		Mode: "src", ModeDoc: raw,
		ScopeRows: []db.TaskScopeInput{{Kind: "ip", Net: "127.0.0.1/32"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.pg.DeleteTask(parseIntDefault(task.ID))
	var asset int64
	if err := m.pg.QueryRow(`INSERT INTO assets(type,url,ip) VALUES('service',$1,'127.0.0.1') RETURNING id`, target.URL).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	defer m.pg.Exec(`DELETE FROM assets WHERE id=$1`, asset)
	t.Setenv("BLADEPRO_TEST_IDENTITY_A", "tenant-a-secret")
	t.Setenv("BLADEPRO_TEST_IDENTITY_B", "tenant-b-secret")
	s := &Server{m: m}
	res, err := s.runSrcIdentityDiff(context.Background(), agent.SrcIdentityRequest{
		TaskID: parseIntDefault(task.ID), AssetID: asset, Path: "/api/users/1", IdentityA: "A", IdentityB: "B",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.A.Status != 200 || res.B.Status != 403 || !res.Distinct {
		t.Fatalf("unexpected comparison: %+v", res)
	}
	encoded, _ := json.Marshal(res)
	if strings.Contains(string(encoded), "tenant-a-secret") || strings.Contains(string(encoded), "tenant-b-secret") {
		t.Fatal("credential leaked into comparison result")
	}
	if res.EvidenceNodeID > 0 {
		node, err := task.Store.GetNode(res.EvidenceNodeID)
		if err != nil || node == nil {
			t.Fatal(err)
		}
		if strings.Contains(string(node.Payload), "secret") {
			t.Fatal("credential leaked into evidence payload")
		}
	}
}

func TestIdentityDiffRejectsAbsoluteAndForeignPaths(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	doc := srcmode.DefaultDoc()
	doc.TestIdentities = []srcmode.TestIdentity{
		{Label: "A", CredentialEnv: "BLADEPRO_TEST_IDENTITY_A"},
		{Label: "B", CredentialEnv: "BLADEPRO_TEST_IDENTITY_B"},
	}
	raw, _ := json.Marshal(doc)
	task, err := m.CreateTaskWithOptions("identity-reject", "identity", db.TaskCreateOptions{
		Mode: "src", ModeDoc: raw, ScopeRows: []db.TaskScopeInput{{Kind: "ip", Net: "127.0.0.1/32"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.pg.DeleteTask(parseIntDefault(task.ID))
	var asset int64
	if err := m.pg.QueryRow(`INSERT INTO assets(type,url,ip) VALUES('service',$1,'127.0.0.1') RETURNING id`, "http://127.0.0.1:9").Scan(&asset); err != nil {
		t.Fatal(err)
	}
	defer m.pg.Exec(`DELETE FROM assets WHERE id=$1`, asset)
	t.Setenv("BLADEPRO_TEST_IDENTITY_A", "a")
	t.Setenv("BLADEPRO_TEST_IDENTITY_B", "b")
	s := &Server{m: m}
	for _, path := range []string{"https://evil.example/x", "//evil.example/x"} {
		if _, err := s.runSrcIdentityDiff(context.Background(), agent.SrcIdentityRequest{
			TaskID: parseIntDefault(task.ID), AssetID: asset, Path: path, IdentityA: "A", IdentityB: "B",
		}); err == nil {
			t.Fatalf("foreign path accepted: %s", path)
		}
	}
}
