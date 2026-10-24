package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

func TestSrcMarkCellBatch(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	root := fmt.Sprintf("batch-%d.example", time.Now().UnixNano())
	task, err := d.CreateTaskWithOptions("src-batch", "批量点格", db.TaskCreateOptions{
		Mode: "src", ScopeRows: []db.TaskScopeInput{{Kind: "root_domain", Domain: root}},
	})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID) })
	as := d.Assets()
	assetID, err := as.UpsertSubdomain(db.UpsertSubdomainReq{Domain: "a." + root, TaskID: task.ID})
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, assetID) })

	ts := NewToolSet(d.Exploration(task.ExplorationID), "worker")
	ts.SetTaskID(task.ID)
	ts.SetAssetStore(as, d.Companies())
	ts.SetSrcMode(srcmode.DefaultDoc(), d.Src())
	payload := map[string]any{"cells": []any{
		map[string]any{"asset_id": assetID, "area": "authz", "state": "na", "reason": "范围外", "na_code": "out_of_scope"},
		map[string]any{"asset_id": assetID, "area": "xss", "state": "na", "reason": "无回显", "na_code": "no_reflect"},
	}}
	raw, _ := json.Marshal(payload)
	res, err := ts.srcMarkCell().Call(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Flatten()), &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, res.Flatten())
	}
	if out["ok"].(float64) != 2 || out["failed"].(float64) != 0 {
		t.Fatalf("result=%v", out)
	}
	cells, _ := d.Src().ListSrcCells(context.Background(), task.ID)
	if len(cells) != 2 {
		t.Fatalf("cells=%d want 2", len(cells))
	}
}

// TestSrcNegativeTool pins the add/list/clear contract and the scope guard.
func TestSrcNegativeTool(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	root := fmt.Sprintf("negative-%d.example", time.Now().UnixNano())
	task, err := d.CreateTaskWithOptions("src-negative", "负知识", db.TaskCreateOptions{
		Mode: "src", ScopeRows: []db.TaskScopeInput{{Kind: "root_domain", Domain: root}},
	})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID) })
	as := d.Assets()
	assetID, err := as.UpsertSubdomain(db.UpsertSubdomainReq{Domain: "n." + root, TaskID: task.ID})
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, assetID) })

	ts := NewToolSet(d.Exploration(task.ExplorationID), "worker")
	ts.SetTaskID(task.ID)
	ts.SetAssetStore(as, d.Companies())
	ts.SetSrcMode(srcmode.DefaultDoc(), d.Src())
	tool := ts.srcNegativeTool()

	call := func(payload map[string]any) map[string]any {
		raw, _ := json.Marshal(payload)
		res, err := tool.Call(context.Background(), raw, nil)
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(res.Flatten()), &out)
		return out
	}
	out := call(map[string]any{
		"asset_id": assetID, "area": "injection", "endpoint": "GET /search",
		"technique": "error_based", "reason": "无报错差异",
	})
	neg, _ := out["negative"].(map[string]any)
	if neg == nil || neg["id"] == nil || neg["asset"] == "" {
		t.Fatalf("add result=%v", out)
	}
	out = call(map[string]any{"action": "list"})
	if out["total"].(float64) != 1 {
		t.Fatalf("list result=%v", out)
	}
	// 越界资产不得写入（错误结果不是 JSON，直接看文本）。
	raw, _ := json.Marshal(map[string]any{"asset_id": 999999, "area": "injection", "reason": "x"})
	errRes, err := tool.Call(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(errRes.Flatten(), "不在本任务测试范围") {
		t.Fatalf("out-of-scope add should fail: %q", errRes.Flatten())
	}
	id := int64(neg["id"].(float64))
	out = call(map[string]any{"action": "clear", "id": id})
	if out["deleted"] == nil {
		t.Fatalf("clear result=%v", out)
	}
}

// TestReconGateBlocksPayloadIntents pins the pre-gate: with recon_gate on, an
// app/service asset with incomplete inventory rejects payload intents until both
// closures are done, then allows them.
func TestReconGateBlocksPayloadIntents(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	root := fmt.Sprintf("recon-%d.example", time.Now().UnixNano())
	task, err := d.CreateTaskWithOptions("src-recon-gate", "侦察闸", db.TaskCreateOptions{
		Mode: "src", ScopeRows: []db.TaskScopeInput{{Kind: "root_domain", Domain: root}},
	})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID) })
	as := d.Assets()
	assetID, err := as.UpsertHTTPService(db.UpsertHTTPServiceReq{URL: "https://shop." + root, TaskID: task.ID})
	if err != nil {
		t.Fatalf("service asset: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, assetID) })
	if _, err := d.Exec(`INSERT INTO task_scope(task_id, kind, domain, source, reason)
VALUES ($1,'subdomain',$2,'manual','recon gate test')`, task.ID, "shop."+root); err != nil {
		t.Fatalf("scope: %v", err)
	}

	on := true
	doc := srcmode.DefaultDoc()
	doc.ReconGate = &on
	ts := NewToolSet(d.Exploration(task.ExplorationID), "planner")
	ts.SetTaskID(task.ID)
	ts.SetAssetStore(as, d.Companies())
	ts.SetSrcMode(doc, d.Src())

	if err := ts.checkReconGate(context.Background(), []int64{assetID}); err == nil {
		t.Fatal("incomplete inventory must block payload intents")
	}
	// 清点两项都落数字后放行。
	ctx := context.Background()
	for _, c := range []db.SrcCheck{
		{TaskID: task.ID, AssetID: assetID, CheckKey: "js_enum", State: db.SrcCheckDone, Metrics: map[string]int{"js_inline": 1}},
		{TaskID: task.ID, AssetID: assetID, CheckKey: "paths_enum", State: db.SrcCheckDone, Metrics: map[string]int{"paths": 200}},
	} {
		if _, err := d.Src().UpsertSrcCheck(ctx, &c); err != nil {
			t.Fatalf("upsert %s: %v", c.CheckKey, err)
		}
	}
	if err := ts.checkReconGate(context.Background(), []int64{assetID}); err != nil {
		t.Fatalf("completed inventory should pass: %v", err)
	}
}
