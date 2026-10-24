package server

// 计划任务/模板接 SRC 的接口测试：mode、平台项目、覆盖继承与每任务预算都要
// 原样存进规格，并在触发时真正落到新任务上。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/lin9527ya/bladepro/db"
)

func TestScheduleAndTemplateCarrySRCPresets(t *testing.T) {
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

	program, err := m.pg.Src().CreateSrcProgram(context.Background(), db.SrcProgramInput{
		Name: fmt.Sprintf("preset-%d", time.Now().UnixNano()), Platform: "test",
	})
	if err != nil {
		t.Fatalf("program: %v", err)
	}
	t.Cleanup(func() { _ = m.pg.Src().DeleteSrcProgram(context.Background(), program.ID) })

	// 模板：SRC 预设原样可读。
	code, out := do("POST", "/api/task-templates", map[string]any{
		"name":        fmt.Sprintf("SRC 模板 %d", time.Now().UnixNano()),
		"description": "模板描述", "goal": "模板目标",
		"mode": "src", "src_program_id": program.ID, "inherit_coverage": true,
	})
	if code != 201 {
		t.Fatalf("create template: %d %v", code, out)
	}
	tmplID := int64(out["id"].(float64))
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM task_templates WHERE id=$1`, tmplID) })
	if out["mode"] != "src" || out["inherit_coverage"] != true {
		t.Fatalf("template presets lost: %v", out)
	}
	if pid, _ := out["src_program_id"].(float64); int64(pid) != program.ID {
		t.Fatalf("template program=%v want %d", out["src_program_id"], program.ID)
	}

	// 计划任务：规格带 SRC 模式/项目/继承/预算；立即运行建出的任务要一致。
	code, out = do("POST", "/api/task-schedules", map[string]any{
		"name": "SRC 定期复测", "enabled": false,
		"task_spec": map[string]any{
			"description": "定期复测 SRC", "goal": "只读复测",
			"mode": "src", "src_program_id": program.ID,
			"inherit_coverage": true, "token_budget": 1234,
		},
		"frequency": map[string]any{"kind": "every_hours", "every_hours": 6},
	})
	if code != 201 {
		t.Fatalf("create schedule: %d %v", code, out)
	}
	schedID := int64(out["id"].(float64))
	t.Cleanup(func() { _, _ = m.pg.DeleteTaskSchedule(schedID) })
	spec, _ := out["task_spec"].(map[string]any)
	if spec["mode"] != "src" || spec["inherit_coverage"] != true {
		t.Fatalf("schedule spec presets lost: %v", spec)
	}

	code, out = do("POST", "/api/task-schedules/"+strconv.FormatInt(schedID, 10)+"/run", nil)
	if code != 200 {
		t.Fatalf("run schedule: %d %v", code, out)
	}
	taskRef, _ := out["task_id"].(string)
	taskID, _ := strconv.ParseInt(taskRef, 10, 64)
	if taskID <= 0 {
		t.Fatalf("bad task id %q", taskRef)
	}
	t.Cleanup(func() { _ = m.pg.DeleteTask(taskID) })
	task, err := m.pg.GetTask(taskID)
	if err != nil || task == nil {
		t.Fatalf("get task: %v %v", task, err)
	}
	if task.Mode != "src" {
		t.Fatalf("task mode=%q want src", task.Mode)
	}
	if task.TokenBudget != 1234 {
		t.Fatalf("task token budget=%d want 1234", task.TokenBudget)
	}
	if task.SrcProgramID == nil || *task.SrcProgramID != program.ID {
		t.Fatalf("task program=%v want %d", task.SrcProgramID, program.ID)
	}
}

func TestScheduleBatchFansOut(t *testing.T) {
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
	root := fmt.Sprintf("sched-batch-%d.example", suffix)
	ids := []int64{}
	for i := 0; i < 3; i++ {
		var id int64
		domain := fmt.Sprintf("s%d.%s", i, root)
		if err := m.pg.QueryRow(`INSERT INTO assets(type, domain, root_domain) VALUES('subdomain',$1,$2) RETURNING id`, domain, root).Scan(&id); err != nil {
			t.Fatalf("asset: %v", err)
		}
		ids = append(ids, id)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM assets WHERE id = ANY($1::bigint[])`, ids) })

	code, out := do("POST", "/api/task-schedules", map[string]any{
		"name": "批量计划", "enabled": false,
		"task_spec": map[string]any{
			"description": "批量计划扇出", "goal": "按 host 分片",
			"mode": "src",
			"batch": map[string]any{
				"asset_ids": ids, "shard_by": "host", "assets_per_task": 2,
			},
		},
		"frequency": map[string]any{"kind": "every_hours", "every_hours": 6},
	})
	if code != 201 {
		t.Fatalf("create schedule: %d %v", code, out)
	}
	schedID := int64(out["id"].(float64))
	t.Cleanup(func() { _, _ = m.pg.DeleteTaskSchedule(schedID) })

	code, out = do("POST", "/api/task-schedules/"+strconv.FormatInt(schedID, 10)+"/run", nil)
	if code != 200 {
		t.Fatalf("run: %d %v", code, out)
	}
	taskRef, _ := out["task_id"].(string)
	taskID, _ := strconv.ParseInt(taskRef, 10, 64)
	if taskID <= 0 {
		t.Fatalf("bad task id %q", taskRef)
	}
	var campaignID int64
	if err := m.pg.QueryRow(`SELECT campaign_id FROM tasks WHERE id=$1`, taskID).Scan(&campaignID); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	var count int
	if err := m.pg.QueryRow(`SELECT count(*) FROM tasks WHERE campaign_id=$1 AND deleted_at IS NULL`, campaignID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("campaign tasks=%d want 2", count)
	}
	t.Cleanup(func() {
		_, _ = m.pg.Exec(`UPDATE tasks SET campaign_id=NULL WHERE campaign_id=$1`, campaignID)
		_, _ = m.pg.Exec(`DELETE FROM campaigns WHERE id=$1`, campaignID)
		_, _ = m.pg.Exec(`DELETE FROM tasks WHERE campaign_id IS NULL AND description LIKE '批量计划扇出%'`)
	})
}
