package server

// 建任务接口的覆盖继承端到端测试：源任务已有 tested_clear + done 收尾项，
// 目标任务带 inherit_coverage=true 创建时应复制这些结论（仅限共同资产）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestCreateTaskInheritsSrcCoverage(t *testing.T) {
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

	var companyID int64
	if err := m.pg.QueryRow(`INSERT INTO companies(name,nkey) VALUES($1,$2) RETURNING id`,
		"继承测试公司", "src-inherit-api-test").Scan(&companyID); err != nil {
		t.Fatalf("create company: %v", err)
	}
	const domain = "inherit-api.example"
	assetID, err := m.pg.Assets().UpsertSubdomain(db.UpsertSubdomainReq{Domain: domain})
	if err != nil {
		t.Fatalf("upsert asset: %v", err)
	}
	t.Cleanup(func() {
		_, _ = m.pg.Exec(`DELETE FROM assets WHERE id=$1`, assetID)
		_, _ = m.pg.Exec(`DELETE FROM companies WHERE id=$1`, companyID)
	})

	create := func(body map[string]any) (int64, string) {
		t.Helper()
		code, out := do("POST", "/api/tasks", body)
		if code != 201 {
			t.Fatalf("create task: %d (%v)", code, out)
		}
		id, _ := out["id"].(string)
		var taskID int64
		if _, err := fmt.Sscan(id, &taskID); err != nil || taskID <= 0 {
			t.Fatalf("bad task id %q", id)
		}
		return taskID, id
	}

	srcID, srcRef := create(map[string]any{
		"description": "inherit source", "goal": "源任务",
		"mode": "src", "company_ids": []int64{companyID},
	})
	// 绑定资产到公司，目标任务按公司范围才会认领同一个资产。
	if _, err := m.pg.Exec(`UPDATE assets SET company_id=$1 WHERE id=$2`, companyID, assetID); err != nil {
		t.Fatalf("bind asset company: %v", err)
	}
	ctx := context.Background()
	if _, err := m.pg.Src().UpsertSrcCell(ctx, &db.SrcCell{
		TaskID: srcID, AssetID: assetID, Area: "authz", Item: "*",
		State: db.SrcStateClear, Reason: "源任务已测，无越权", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("src cell: %v", err)
	}
	if _, err := m.pg.Src().UpsertSrcCheck(ctx, &db.SrcCheck{
		TaskID: srcID, AssetID: assetID, CheckKey: "paths_enum",
		State: db.SrcCheckDone, Metrics: map[string]int{"paths": 210}, UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("src check: %v", err)
	}

	dstID, dstRef := create(map[string]any{
		"description": "inherit dst", "goal": "目标任务",
		"mode": "src", "company_ids": []int64{companyID},
		"inherit_coverage": true,
	})
	t.Cleanup(func() {
		for _, id := range []string{srcRef, dstRef} {
			var expID int64
			_ = m.pg.QueryRow(`SELECT exploration_id FROM tasks WHERE id=$1`, id).Scan(&expID)
			_, _ = m.pg.Exec(`DELETE FROM tasks WHERE id=$1`, id)
			if expID > 0 {
				_, _ = m.pg.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
			}
		}
	})

	cell, err := m.pg.Src().GetSrcCell(ctx, dstID, assetID, "authz", "*")
	if err != nil || cell == nil {
		t.Fatalf("dst cell: %v %v", cell, err)
	}
	if cell.State != db.SrcStateClear || cell.InheritedFromTaskID != srcID {
		t.Fatalf("dst cell=%+v want inherited clear from %d", cell, srcID)
	}
	check, err := m.pg.Src().GetSrcCheck(ctx, dstID, assetID, "paths_enum")
	if err != nil || check == nil {
		t.Fatalf("dst check: %v %v", check, err)
	}
	if check.State != db.SrcCheckDone || check.Metrics["paths"] != 210 || check.InheritedFromTaskID != srcID {
		t.Fatalf("dst check=%+v", check)
	}
}
