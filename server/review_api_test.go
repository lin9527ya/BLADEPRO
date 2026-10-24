package server

// 人工复核队列的动作端到端：接受后从队列消失、打回改发现状态、退回重测删格。

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

func TestReviewQueueActions(t *testing.T) {
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

	task, err := m.pg.CreateTaskWithOptions("review-actions", "复核动作", db.TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID) })
	var assetID int64
	if err := m.pg.QueryRow(`INSERT INTO assets(type, domain, root_domain)
VALUES ('subdomain',$1,'review-actions.example') RETURNING id`,
		fmt.Sprintf("rv-%d.example", time.Now().UnixNano())).Scan(&assetID); err != nil {
		t.Fatalf("asset: %v", err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM assets WHERE id=$1`, assetID) })
	ctx := context.Background()
	if _, err := m.pg.Src().UpsertSrcCell(ctx, &db.SrcCell{
		TaskID: task.ID, AssetID: assetID, Area: "authz", Item: "*",
		State: db.SrcStateBudgetStop, Reason: "预算用尽", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("cell: %v", err)
	}
	var findingID int64
	if err := m.pg.QueryRow(`INSERT INTO findings(task_id, vulnclass, name, severity, status)
VALUES ($1,'IDOR','复核测试发现','high','pending') RETURNING id`, task.ID).Scan(&findingID); err != nil {
		t.Fatalf("finding: %v", err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM findings WHERE id=$1`, findingID) })
	if _, err := m.pg.Exec(`INSERT INTO src_finding_meta(finding_id, task_id, report_ready) VALUES($1,$2,false)`, findingID, task.ID); err != nil {
		t.Fatalf("meta: %v", err)
	}

	code, out := do("GET", "/api/review/queue", nil)
	if code != 200 {
		t.Fatalf("queue: %d %v", code, out)
	}
	items, _ := out["items"].([]any)
	var cellItem, findingItem map[string]any
	for _, raw := range items {
		item := raw.(map[string]any)
		if int64(item["task_id"].(float64)) != task.ID {
			continue
		}
		switch item["kind"] {
		case "budget_stop":
			cellItem = item
		case "report_missing":
			if int64(item["finding_id"].(float64)) == findingID {
				findingItem = item
			}
		}
	}
	if cellItem == nil || findingItem == nil {
		t.Fatalf("queue missing items: %v", out)
	}

	// accept 预算停 → 留痕；reject 发现 → false_positive。
	code, out = do("POST", "/api/review/actions", map[string]any{
		"action": "accept", "note": "接受预算停",
		"items": []map[string]any{cellItem},
	})
	if code != 200 || out["ok"].(float64) != 1 {
		t.Fatalf("accept: %d %v", code, out)
	}
	code, out = do("POST", "/api/review/actions", map[string]any{
		"action": "reject", "note": "误报",
		"items": []map[string]any{findingItem},
	})
	if code != 200 || out["ok"].(float64) != 1 {
		t.Fatalf("reject: %d %v", code, out)
	}
	f, err := m.pg.GetFinding(findingID)
	if err != nil || f == nil || f.Status != db.FindingFalsePositive {
		t.Fatalf("finding status=%+v err=%v", f, err)
	}

	// 已裁决项不再出现（即使发现仍是 report_ready=false / 格仍是 budget_stop）。
	code, out = do("GET", "/api/review/queue", nil)
	if code != 200 {
		t.Fatalf("queue2: %d", code)
	}
	for _, raw := range out["items"].([]any) {
		item := raw.(map[string]any)
		if item["ref_key"] == cellItem["ref_key"] || item["ref_key"] == findingItem["ref_key"] {
			t.Fatalf("decided item reappeared: %v", item)
		}
	}

	// ready 超 SLA 打回 → 台账 withdrawn。
	var staleFinding int64
	if err := m.pg.QueryRow(`INSERT INTO findings(task_id, vulnclass, name, severity, status)
VALUES ($1,'XSS','待提交超时','medium','confirmed') RETURNING id`, task.ID).Scan(&staleFinding); err != nil {
		t.Fatalf("stale finding: %v", err)
	}
	if _, err := m.pg.Exec(`INSERT INTO src_submissions(task_id, finding_id, platform, status, title, severity, created_at, updated_at)
VALUES ($1,$2,'','ready','待提交超时','medium', now()-interval '10 days', now()-interval '10 days')`, task.ID, staleFinding); err != nil {
		t.Fatalf("stale submission: %v", err)
	}
	code, out = do("GET", "/api/review/queue", nil)
	var staleItem map[string]any
	for _, raw := range out["items"].([]any) {
		item := raw.(map[string]any)
		if item["kind"] == "ready_stale" && int64(item["finding_id"].(float64)) == staleFinding {
			staleItem = item
		}
	}
	if staleItem == nil {
		t.Fatalf("ready_stale item missing: %v", out)
	}
	code, out = do("POST", "/api/review/actions", map[string]any{
		"action": "reject", "note": "超时撤回",
		"items": []map[string]any{staleItem},
	})
	if code != 200 || out["ok"].(float64) != 1 {
		t.Fatalf("reject stale: %d %v", code, out)
	}
	var staleStatus string
	if err := m.pg.QueryRow(`SELECT status FROM src_submissions WHERE finding_id=$1 AND platform=''`, staleFinding).Scan(&staleStatus); err != nil {
		t.Fatal(err)
	}
	if staleStatus != db.SrcSubWithdrawn {
		t.Fatalf("stale status=%q want withdrawn", staleStatus)
	}

	// retest 预算停 → 删除该格（变回空白）。
	if _, err := m.pg.Src().UpsertSrcCell(ctx, &db.SrcCell{
		TaskID: task.ID, AssetID: assetID, Area: "xss", Item: "*",
		State: db.SrcStateBudgetStop, Reason: "预算用尽", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("cell2: %v", err)
	}
	code, out = do("GET", "/api/review/queue", nil)
	var retestItem map[string]any
	for _, raw := range out["items"].([]any) {
		item := raw.(map[string]any)
		if item["kind"] == "budget_stop" && int64(item["task_id"].(float64)) == task.ID {
			retestItem = item
		}
	}
	if retestItem == nil {
		t.Fatalf("new budget_stop missing: %v", out)
	}
	code, out = do("POST", "/api/review/actions", map[string]any{
		"action": "retest", "note": "人工要求重测",
		"items": []map[string]any{retestItem},
	})
	if code != 200 || out["ok"].(float64) != 1 {
		t.Fatalf("retest: %d %v", code, out)
	}
	if cell, err := m.pg.Src().GetSrcCell(ctx, task.ID, assetID, "xss", "*"); err != nil || cell != nil {
		t.Fatalf("cell should be deleted: %+v %v", cell, err)
	}
}
