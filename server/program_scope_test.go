package server

// program_scope 端到端：in 进 task_scope、out 进排除表，SRC 范围分母真的排除 out 资产。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lin9527ya/bladepro/db"
)

func TestProgramScopeCompilesIntoTask(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	td := t.TempDir()
	s := New(context.Background(), m, td, td, td)
	h := s.Handler()
	tok, err := signJWT(s.auth.signKey.get())
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	do := func(method, path string, body any) (int, map[string]any) {
		var r = httptest.NewRequest(method, path, nil)
		if body != nil {
			b, _ := json.Marshal(body)
			r = httptest.NewRequest(method, path, bytes.NewReader(b))
		}
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	suffix := time.Now().UnixNano()
	root := fmt.Sprintf("scope-%d.example", suffix)
	program, err := m.pg.Src().CreateSrcProgram(context.Background(), db.SrcProgramInput{
		Name: fmt.Sprintf("scope-program-%d", suffix), Platform: "test",
	})
	if err != nil {
		t.Fatalf("program: %v", err)
	}
	t.Cleanup(func() { _ = m.pg.Src().DeleteSrcProgram(context.Background(), program.ID) })

	code, out := do("PUT", fmt.Sprintf("/api/src/programs/%d/scope", program.ID), map[string]any{
		"rows": []map[string]any{
			{"direction": "in", "kind": "root_domain", "value": root},
			{"direction": "out", "kind": "subdomain", "value": "secret." + root},
		},
	})
	if code != 200 || out["total"].(float64) != 2 {
		t.Fatalf("put scope: %d %v", code, out)
	}

	// 资产：一个正常子域 + 一个被 out 排除的子域。
	var publicID, secretID int64
	for _, item := range []struct {
		domain string
		id     *int64
	}{{"public." + root, &publicID}, {"secret." + root, &secretID}} {
		if err := m.pg.QueryRow(`INSERT INTO assets(type, domain, root_domain) VALUES('subdomain',$1,$2) RETURNING id`,
			item.domain, root).Scan(item.id); err != nil {
			t.Fatalf("asset: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = m.pg.Exec(`DELETE FROM assets WHERE id = ANY($1::bigint[])`, []int64{publicID, secretID})
	})

	code, out = do("POST", "/api/tasks", map[string]any{
		"description": "program scope 测试", "goal": "只测范围内资产",
		"mode": "src", "src_program_id": program.ID,
	})
	if code != 201 {
		t.Fatalf("create task: %d %v", code, out)
	}
	taskID := parseIntDefault(out["id"].(string))
	t.Cleanup(func() { _ = m.pg.DeleteTask(taskID) })

	var scopeCount, exclCount int
	if err := m.pg.QueryRow(`SELECT count(*) FROM task_scope WHERE task_id=$1 AND kind='root_domain' AND domain=$2`, taskID, root).Scan(&scopeCount); err != nil {
		t.Fatalf("scope: %v", err)
	}
	if err := m.pg.QueryRow(`SELECT count(*) FROM task_scope_exclusions WHERE task_id=$1 AND kind='subdomain' AND domain=$2`, taskID, "secret."+root).Scan(&exclCount); err != nil {
		t.Fatalf("exclusion: %v", err)
	}
	if scopeCount != 1 || exclCount != 1 {
		t.Fatalf("scope=%d exclusion=%d", scopeCount, exclCount)
	}

	assets, err := m.pg.Src().InScopeSrcAssets(context.Background(), taskID)
	if err != nil {
		t.Fatalf("in-scope: %v", err)
	}
	foundPublic, foundSecret := false, false
	for _, a := range assets {
		if a.ID == publicID {
			foundPublic = true
		}
		if a.ID == secretID {
			foundSecret = true
		}
	}
	if !foundPublic || foundSecret {
		t.Fatalf("in-scope assets=%+v (public=%v secret=%v)", assets, foundPublic, foundSecret)
	}
}
