package server

// 批量扇出端到端：dry-run 只出计划；真实创建按 host 分片、每片独立 scope/资产关联，
// 并归入同一个 campaign。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestCreateTaskBatchShardsByHost(t *testing.T) {
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
	post := func(body any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/api/tasks/batch", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	suffix := time.Now().UnixNano()
	var companyID int64
	if err := m.pg.QueryRow(`INSERT INTO companies(name,nkey) VALUES($1,$2) RETURNING id`,
		"批量接口测试", fmt.Sprintf("batch-api-%d", suffix)).Scan(&companyID); err != nil {
		t.Fatalf("company: %v", err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM companies WHERE id=$1`, companyID) })
	root := fmt.Sprintf("batch-api-%d.example", suffix)
	assetIDs := []int64{}
	for i := 1; i <= 3; i++ {
		var id int64
		domain := fmt.Sprintf("s%d.%s", i, root)
		if err := m.pg.QueryRow(`INSERT INTO assets(type, domain, root_domain, company_id)
VALUES ('subdomain',$1,$2,$3) RETURNING id`, domain, root, companyID).Scan(&id); err != nil {
			t.Fatalf("asset %d: %v", i, err)
		}
		assetIDs = append(assetIDs, id)
	}
	t.Cleanup(func() {
		_, _ = m.pg.Exec(`DELETE FROM assets WHERE id = ANY($1::bigint[])`, assetIDs)
	})
	body := map[string]any{
		"description": "批量扇出接口测试", "goal": "按 host 分片",
		"mode": "src", "mode_doc": map[string]any{"allow_budget_stop": true},
		"company_ids": []int64{companyID},
		"shard_by":    "host", "assets_per_task": 2, "max_shards": 10,
		"token_budget": 5000,
	}

	// dry-run: 3 个 host、每片 2 个资产 → 2 片，但不建任务/campaign。
	code, out := post(map[string]any{
		"description": body["description"], "goal": body["goal"], "mode": body["mode"],
		"mode_doc": body["mode_doc"], "company_ids": body["company_ids"],
		"shard_by": "host", "assets_per_task": 2, "dry_run": true,
	})
	if code != 200 || out["dry_run"] != true {
		t.Fatalf("dry-run: %d %v", code, out)
	}
	planned, _ := out["shards"].([]any)
	if len(planned) != 2 {
		t.Fatalf("dry-run shards=%d want 2 (%v)", len(planned), out)
	}
	if _, hasCampaign := out["campaign"]; hasCampaign {
		t.Fatalf("dry-run must not create a campaign: %v", out)
	}

	code, out = post(body)
	if code != 201 {
		t.Fatalf("batch create: %d %v", code, out)
	}
	totals, _ := out["totals"].(map[string]any)
	if totals["created"].(float64) != 2 || totals["assets"].(float64) != 3 {
		t.Fatalf("totals=%v", totals)
	}
	campaign, _ := out["campaign"].(map[string]any)
	campaignID := int64(campaign["id"].(float64))
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM campaigns WHERE id=$1`, campaignID) })
	shards, _ := out["shards"].([]any)
	if len(shards) != 2 {
		t.Fatalf("shards=%v", shards)
	}
	created := 0
	for _, raw := range shards {
		shard := raw.(map[string]any)
		if shard["status"] != "created" {
			t.Fatalf("shard failed: %v", shard)
		}
		taskID := shard["task_id"].(string)
		id, _ := strconv.ParseInt(taskID, 10, 64)
		t.Cleanup(func() { _ = m.pg.DeleteTask(id) })
		created++
		var scopes, links int
		if err := m.pg.QueryRow(`SELECT count(*) FROM task_scope WHERE task_id=$1 AND kind='subdomain'`, id).Scan(&scopes); err != nil {
			t.Fatalf("scope count: %v", err)
		}
		if err := m.pg.QueryRow(`SELECT count(*) FROM task_asset_links WHERE task_id=$1`, id).Scan(&links); err != nil {
			t.Fatalf("link count: %v", err)
		}
		if scopes == 0 || links == 0 {
			t.Fatalf("task %s scopes=%d links=%d", taskID, scopes, links)
		}
		var gotCampaign int64
		var budget int64
		if err := m.pg.QueryRow(`SELECT COALESCE(campaign_id,0), COALESCE(token_budget,0) FROM tasks WHERE id=$1`, id).Scan(&gotCampaign, &budget); err != nil {
			t.Fatalf("task row: %v", err)
		}
		if gotCampaign != campaignID || budget != 5000 {
			t.Fatalf("task %s campaign=%d budget=%d", taskID, gotCampaign, budget)
		}
	}
	if created != 2 {
		t.Fatalf("created=%d", created)
	}
}
