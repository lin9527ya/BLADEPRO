package agent

import (
	"context"
	"encoding/json"
	"fmt"
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
