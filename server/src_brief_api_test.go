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

// TestPatchTaskSrcBriefOnlyTouchesSoftBoundary pins the runtime contract:
// focus/exclude/notes are editable with CAS, while required areas and other hard
// fields are rejected instead of being silently ignored by encoding/json.
func TestPatchTaskSrcBriefOnlyTouchesSoftBoundary(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	td := t.TempDir()
	s := New(context.Background(), m, td, td, td)
	h := s.Handler()
	tok, err := signJWT(s.auth.signKey.get())
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	do := func(method, path string, body any) (int, map[string]any) {
		var r *http.Request
		if body != nil {
			b, _ := json.Marshal(body)
			r = httptest.NewRequest(method, path, bytes.NewReader(b))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	code, out := do("POST", "/api/src/programs", map[string]any{
		"name": fmt.Sprintf("brief-api-%d", time.Now().UnixNano()), "platform": "test",
		"scope_note": "", "rate_limit": 0, "time_window": "",
		"rules": map[string]any{
			"required_areas": map[string][]string{"subdomain": {"paths"}},
			"focus_areas":    []string{"authz"},
		},
		"severity_map": map[string]string{},
	})
	if code != 201 {
		t.Fatalf("create program: %d (%v)", code, out)
	}
	programIDf, _ := out["id"].(float64)
	programID := int64(programIDf)
	if programID <= 0 {
		t.Fatalf("bad program id %v", out["id"])
	}
	t.Cleanup(func() { _, _ = m.PG().Exec(`DELETE FROM src_programs WHERE id=$1`, programID) })

	code, out = do("POST", "/api/tasks", map[string]any{
		"description": "soft brief api", "goal": "验证探索偏好边界",
		"mode": "src", "src_program_id": programID,
		"mode_doc": map[string]any{
			// 客户端不能自封冻结预设；ProgramBrief 只能由绑定的 Program 编译。
			"program_brief": map[string]any{"focus_areas": []string{"xss"}},
			"soft_brief":    map[string]any{"focus_areas": []string{"logic"}, "revision": 0},
		},
	})
	if code != 201 {
		t.Fatalf("create task: %d (%v)", code, out)
	}
	id, _ := out["id"].(string)
	var taskID int64
	if _, err := fmt.Sscan(id, &taskID); err != nil || taskID <= 0 {
		t.Fatalf("bad task id %q", id)
	}
	t.Cleanup(func() { _ = m.PG().DeleteTask(taskID) })
	base := "/api/tasks/" + id + "/src"

	// Task creation also rejects a SoftBrief that contradicts the Program preset.
	if code, out = do("POST", "/api/tasks", map[string]any{
		"description": "conflicting brief", "goal": "创建期冲突",
		"mode": "src", "src_program_id": programID,
		"mode_doc": map[string]any{
			"soft_brief": map[string]any{"exclude_areas": []string{"authz"}, "revision": 0},
		},
	}); code != http.StatusBadRequest {
		t.Fatalf("conflicting create accepted: %d (%v)", code, out)
	}

	code, out = do("PATCH", base+"/brief", map[string]any{
		"focus_areas": []string{"xss"}, "exclude_areas": []string{"paths"},
		"notes": []string{"先打登录口"}, "revision": 0,
	})
	if code != http.StatusOK {
		t.Fatalf("patch brief: %d (%v)", code, out)
	}
	brief, _ := out["brief"].(map[string]any)
	if brief["revision"] != float64(1) {
		t.Fatalf("revision=%v, want 1", brief["revision"])
	}
	warns, _ := out["warnings"].([]any)
	if len(warns) == 0 {
		t.Fatalf("excluding required paths should warn: %v", out)
	}

	code, snap := do("GET", base, nil)
	if code != http.StatusOK || snap["enabled"] != true {
		t.Fatalf("get src: %d (%v)", code, snap)
	}
	got, _ := snap["soft_brief"].(map[string]any)
	if got == nil || got["revision"] != float64(1) {
		t.Fatalf("snapshot soft_brief=%v", snap["soft_brief"])
	}
	if prog, _ := snap["program_brief"].(map[string]any); prog == nil {
		t.Fatalf("snapshot program_brief missing: %v", snap["program_brief"])
	} else if areas, _ := prog["focus_areas"].([]any); len(areas) != 1 || areas[0] != "authz" {
		t.Fatalf("task-supplied program_brief was not replaced by Program: %v", prog)
	}
	required, _ := snap["required_areas"].(map[string]any)
	if areas, _ := required["subdomain"].([]any); len(areas) != 1 || areas[0] != "paths" {
		t.Fatalf("hard required areas changed: %v", snap["required_areas"])
	}
	if _, ok := snap["soft_debt"]; !ok {
		t.Fatalf("snapshot missing soft_debt: %v", snap)
	}

	// Stale revision → 409, not last-writer-wins.
	if code, out = do("PATCH", base+"/brief", map[string]any{
		"focus_areas": []string{"ssrf"}, "revision": 0,
	}); code != http.StatusConflict {
		t.Fatalf("stale revision: %d (%v)", code, out)
	}
	// Same area on both sides → 400.
	if code, out = do("PATCH", base+"/brief", map[string]any{
		"focus_areas": []string{"xss"}, "exclude_areas": []string{"xss"}, "revision": 1,
	}); code != http.StatusBadRequest {
		t.Fatalf("focus/exclude conflict: %d (%v)", code, out)
	}
	// Unknown area → 400.
	if code, out = do("PATCH", base+"/brief", map[string]any{
		"focus_areas": []string{"not-a-real-area"}, "revision": 1,
	}); code != http.StatusBadRequest {
		t.Fatalf("unknown area: %d (%v)", code, out)
	}
	// A soft edit cannot undo the frozen Program preset.
	if code, out = do("PATCH", base+"/brief", map[string]any{
		"exclude_areas": []string{"authz"}, "revision": 1,
	}); code != http.StatusBadRequest {
		t.Fatalf("program preset overridden: %d (%v)", code, out)
	}
	// Hard boundaries cannot be edited through the soft-brief endpoint.
	for _, body := range []map[string]any{
		{"required_areas": map[string][]string{"subdomain": {"xss"}}, "revision": 1},
		{"program_brief": map[string]any{"focus_areas": []string{"ssrf"}}, "revision": 1},
		{"scope": []string{"evil.example"}, "revision": 1},
		{"budget": 999999999, "revision": 1},
	} {
		if code, out = do("PATCH", base+"/brief", body); code != http.StatusBadRequest {
			t.Fatalf("hard field accepted: %d (%v)", code, out)
		}
	}

	// A valid follow-up advances the revision and keeps hard fields intact.
	code, out = do("PATCH", base+"/brief", map[string]any{
		"focus_areas": []string{"ssrf"}, "exclude_areas": []string{}, "notes": []string{}, "revision": 1,
	})
	if code != http.StatusOK {
		t.Fatalf("second patch: %d (%v)", code, out)
	}
	brief, _ = out["brief"].(map[string]any)
	if brief["revision"] != float64(2) {
		t.Fatalf("second revision=%v, want 2", brief["revision"])
	}
	code, snap = do("GET", base, nil)
	if code != http.StatusOK {
		t.Fatalf("get src after second patch: %d (%v)", code, snap)
	}
	required, _ = snap["required_areas"].(map[string]any)
	if areas, _ := required["subdomain"].([]any); len(areas) != 1 || areas[0] != "paths" {
		t.Fatalf("hard required areas changed after soft update: %v", snap["required_areas"])
	}
}
