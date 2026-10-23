package db

// src_checks 的存储层集成测试（无 PG 自动跳过；CI 的 postgres 服务会真跑）。
// 钉住三件事：幂等 upsert、evidence_node_id 的 COALESCE 保留、按资产分组。

import (
	"context"
	"testing"
)

func TestSrcChecksStoreRoundTrip(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil {
		t.Skipf("no postgres configured (%v) — skipping", err)
	}
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	task, err := d.CreateTaskWithOptions("src-checks-store", "收尾项存储", TaskCreateOptions{})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	var expID int64
	if err := d.QueryRow(`SELECT exploration_id FROM tasks WHERE id=$1`, task.ID).Scan(&expID); err != nil {
		t.Fatalf("exploration for task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM src_checks WHERE task_id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	})

	as := d.Assets()
	assetID, err := as.UpsertSubdomain(UpsertSubdomainReq{Domain: "checks-store.example", TaskID: task.ID})
	if err != nil {
		t.Fatalf("upsert subdomain: %v", err)
	}
	var nodeID int64
	if err := d.QueryRow(`INSERT INTO exploration_nodes(exploration_id, kind, state, payload)
VALUES ($1,'fact','confirmed','{}'::jsonb) RETURNING id`, expID).Scan(&nodeID); err != nil {
		t.Fatalf("insert fact node: %v", err)
	}

	ctx := context.Background()
	store := d.Src()

	row, err := store.UpsertSrcCheck(ctx, &SrcCheck{
		TaskID: task.ID, AssetID: assetID, CheckKey: "paths_enum",
		State: SrcCheckDone, Metrics: map[string]int{"paths": 210}, UpdatedBy: "test",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if row == nil || row.State != SrcCheckDone || row.Metrics["paths"] != 210 {
		t.Fatalf("回读不对：%+v", row)
	}
	if row.Asset == "" || row.AssetType != "subdomain" {
		t.Fatalf("资产标签/类型没 join 上：%+v", row)
	}

	// 幂等：同一 (task, asset, key) 只应有一行。
	if _, err := store.UpsertSrcCheck(ctx, &SrcCheck{
		TaskID: task.ID, AssetID: assetID, CheckKey: "paths_enum",
		State: SrcCheckNA, NACode: "reachable", Reason: "改为不适用", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	all, err := store.ListSrcChecks(ctx, task.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("唯一键失效：%d 行", len(all))
	}
	if all[0].State != SrcCheckNA || all[0].NACode != "reachable" {
		t.Fatalf("覆盖写没生效：%+v", all[0])
	}

	// evidence_node_id 的 COALESCE：先补上，再用 0 覆盖，应当保留。
	if _, err := store.UpsertSrcCheck(ctx, &SrcCheck{
		TaskID: task.ID, AssetID: assetID, CheckKey: "paths_enum",
		State: SrcCheckDone, EvidenceNodeID: nodeID,
		Metrics: map[string]int{"paths": 201}, UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("upsert with evidence: %v", err)
	}
	if _, err := store.UpsertSrcCheck(ctx, &SrcCheck{
		TaskID: task.ID, AssetID: assetID, CheckKey: "paths_enum",
		State: SrcCheckDone, EvidenceNodeID: 0,
		Metrics: map[string]int{"paths": 202}, UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("upsert without evidence: %v", err)
	}
	got, err := store.GetSrcCheck(ctx, task.ID, assetID, "paths_enum")
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.EvidenceNodeID != nodeID {
		t.Fatalf("evidence_node_id 被清掉了：%d, want %d", got.EvidenceNodeID, nodeID)
	}
	if got.Metrics["paths"] != 202 {
		t.Fatalf("metrics 没覆盖：%+v", got.Metrics)
	}

	// 分组：第二个资产再写一项，分组后每个资产各一组。
	otherID, err := as.UpsertSubdomain(UpsertSubdomainReq{Domain: "other.checks-store.example", TaskID: task.ID})
	if err != nil {
		t.Fatalf("upsert other: %v", err)
	}
	if _, err := store.UpsertSrcCheck(ctx, &SrcCheck{
		TaskID: task.ID, AssetID: otherID, CheckKey: "unreachable_proof",
		State: SrcCheckNA, NACode: "reachable", Reason: "可达", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("upsert other: %v", err)
	}
	grouped, err := store.SrcChecksByAsset(ctx, task.ID)
	if err != nil {
		t.Fatalf("grouped: %v", err)
	}
	if len(grouped) != 2 || len(grouped[assetID]) != 1 || len(grouped[otherID]) != 1 {
		t.Fatalf("分组不对：%+v", grouped)
	}
}
