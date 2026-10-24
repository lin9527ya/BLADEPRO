package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestCustomSubmitTemplateHTTP pins the user-template CRUD and validation:
// custom: prefix required, unknown field sources rejected, and built-ins stay
// visible in the merged list.
func TestCustomSubmitTemplateHTTP(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	s := New(context.Background(), m, t.TempDir(), t.TempDir(), t.TempDir())
	h := s.Handler()
	token, err := signJWT(s.auth.signKey.get())
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path string, body any) (int, map[string]any) {
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	key := fmt.Sprintf("custom:acme-%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM src_platform_templates WHERE key=$1`, key) })
	code, created := do(http.MethodPost, "/api/src/platform-templates", map[string]any{
		"key": key, "name": "Acme 表单", "rule_url": "https://acme.example/rules", "rule_date": "2026-10-24",
		"fields": []map[string]any{
			{"label": "标题", "source": "title", "required": true},
			{"label": "等级", "source": "severity"},
		},
	})
	if code != http.StatusOK || created["key"] != key {
		t.Fatalf("create status=%d body=%v", code, created)
	}
	code, _ = do(http.MethodPost, "/api/src/platform-templates", map[string]any{
		"key": "no-prefix", "name": "x", "fields": []map[string]any{{"label": "a", "source": "title"}},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("missing custom prefix status=%d", code)
	}
	code, _ = do(http.MethodPost, "/api/src/platform-templates", map[string]any{
		"key": key, "name": "x", "fields": []map[string]any{{"label": "a", "source": "nope"}},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("unknown source status=%d", code)
	}
	code, listed := do(http.MethodGet, "/api/src/platform-templates", nil)
	if code != http.StatusOK {
		t.Fatalf("list status=%d", code)
	}
	temps, _ := listed["templates"].([]any)
	var foundCustom, foundBuiltin bool
	for _, raw := range temps {
		tpl, _ := raw.(map[string]any)
		if tpl["key"] == key && tpl["custom"] == true {
			foundCustom = true
		}
		if tpl["key"] == "butian" && tpl["custom"] == false {
			foundBuiltin = true
		}
	}
	if !foundCustom || !foundBuiltin {
		t.Fatalf("merged list missing entries: %v", listed)
	}
	code, deleted := do(http.MethodDelete, "/api/src/platform-templates/"+key, nil)
	if code != http.StatusOK || deleted["deleted"] != key {
		t.Fatalf("delete status=%d body=%v", code, deleted)
	}
}
