package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestSrcNegativeKnowledgeReuse pins the cross-task contract: a negative fact on
// an asset is visible from another task that has the same asset in scope, but a
// task cannot delete another task's rows.
func TestSrcNegativeKnowledgeReuse(t *testing.T) {
	d, _, _ := testSetup(t)
	defer d.Close()
	ctx := context.Background()

	mkTask := func(name string) (*Task, int64) {
		task, err := d.CreateTaskWithOptions(name, "negative", TaskCreateOptions{Mode: "src"})
		if err != nil {
			t.Fatalf("create task: %v", err)
		}
		var id int64
		if err := d.QueryRow(`SELECT id FROM tasks WHERE exploration_id=$1`, task.ExplorationID).Scan(&id); err != nil {
			t.Fatalf("task id: %v", err)
		}
		t.Cleanup(func() { _ = d.DeleteTask(id) })
		return task, id
	}
	t1, id1 := mkTask("negative-1")
	_, id2 := mkTask("negative-2")

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	domain := "neg-" + suffix + ".example"
	assetID, err := d.Assets().UpsertSubdomain(UpsertSubdomainReq{Domain: domain, TaskID: id1})
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, assetID) })
	for _, tid := range []int64{id1, id2} {
		if _, err := d.Exec(`INSERT INTO task_scope(task_id, kind, domain, source, reason)
VALUES ($1,'subdomain',$2,'manual','negative test')`, tid, domain); err != nil {
			t.Fatalf("scope: %v", err)
		}
	}
	_ = t1

	row, err := d.Src().UpsertSrcNegative(ctx, SrcNegative{
		TaskID: id1, AssetID: assetID, Area: "injection", Endpoint: "GET /search",
		Technique: "sql_error_based", Reason: "只回显通用错误页，无 SQL 报错差异",
	})
	if err != nil || row == nil || row.ID == 0 {
		t.Fatalf("upsert: %+v %v", row, err)
	}
	// 同键重复记录应更新而不是新增。
	row2, err := d.Src().UpsertSrcNegative(ctx, SrcNegative{
		TaskID: id1, AssetID: assetID, Area: "injection", Endpoint: "GET /search",
		Technique: "sql_error_based", Reason: "更新后的结论",
	})
	if err != nil || row2.ID != row.ID || row2.Reason != "更新后的结论" {
		t.Fatalf("upsert update: %+v %v", row2, err)
	}

	got, err := d.Src().ListSrcNegativesForTask(ctx, id2, 20)
	if err != nil || len(got) != 1 || got[0].ID != row.ID || got[0].Asset == "" {
		t.Fatalf("cross-task list: %+v %v", got, err)
	}
	ok, err := d.Src().DeleteSrcNegative(ctx, id2, row.ID)
	if err != nil || ok {
		t.Fatalf("other task must not delete: ok=%v err=%v", ok, err)
	}
	ok, err = d.Src().DeleteSrcNegative(ctx, id1, row.ID)
	if err != nil || !ok {
		t.Fatalf("owner delete: ok=%v err=%v", ok, err)
	}
	if n, _ := d.Src().SrcNegativeCount(ctx, id1); n != 0 {
		t.Fatalf("rows left=%d", n)
	}
}
