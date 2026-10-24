package db

// 跨任务复用的存储层：资产级探针缓存与「其他任务的最近终态」查询。

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestProbeCacheAndPriorCoverage(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	ctx := context.Background()

	var assetID int64
	if err := d.QueryRow(`INSERT INTO assets(type, domain, root_domain)
VALUES ('subdomain',$1,$2) RETURNING id`, "reuse-"+time.Now().Format("150405.000000")+".example", "reuse.example").Scan(&assetID); err != nil {
		t.Fatalf("asset: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, assetID) })

	// 探针缓存：按 (asset, key, target) 存取，过期后读不到。
	payload := []byte(`{"kind":"js","metrics":{"js_external":3}}`)
	if err := d.PutProbeCache(ctx, assetID, "js", "https://reuse.example", payload, time.Hour); err != nil {
		t.Fatalf("put cache: %v", err)
	}
	entry, err := d.GetProbeCache(ctx, assetID, "js", "https://reuse.example")
	if err != nil || entry == nil {
		t.Fatalf("get cache=%+v err=%v", entry, err)
	}
	var gotPayload, wantPayload map[string]any
	_ = json.Unmarshal(entry.Payload, &gotPayload)
	_ = json.Unmarshal(payload, &wantPayload)
	if !reflect.DeepEqual(gotPayload, wantPayload) {
		t.Fatalf("cache payload=%s want %s", entry.Payload, payload)
	}
	// target 不同 → 不命中（资产 URL 变更即失效）。
	if other, err := d.GetProbeCache(ctx, assetID, "js", "https://other.example"); err != nil || other != nil {
		t.Fatalf("target mismatch should miss: %+v %v", other, err)
	}
	if _, err := d.Exec(`UPDATE asset_probe_cache SET expires_at=now()-interval '1 minute' WHERE asset_id=$1`, assetID); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if expired, err := d.GetProbeCache(ctx, assetID, "js", "https://reuse.example"); err != nil || expired != nil {
		t.Fatalf("expired cache should miss: %+v %v", expired, err)
	}

	// 两个任务，同一资产；任务 2 的 prior_coverage 应看到任务 1，而不是自己。
	t1, err := d.CreateTaskWithOptions("reuse-1", "来源", TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatalf("task1: %v", err)
	}
	t2, err := d.CreateTaskWithOptions("reuse-2", "目标", TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatalf("task2: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM tasks WHERE id = ANY($1::bigint[])`, []int64{t1.ID, t2.ID})
	})
	if _, err := d.Src().UpsertSrcCell(ctx, &SrcCell{
		TaskID: t1.ID, AssetID: assetID, Area: "authz", Item: "*",
		State: SrcStateClear, Reason: "历史已测", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("t1 cell: %v", err)
	}
	if _, err := d.Src().UpsertSrcCell(ctx, &SrcCell{
		TaskID: t2.ID, AssetID: assetID, Area: "authz", Item: "*",
		State: SrcStateClear, Reason: "本轮已测", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("t2 cell: %v", err)
	}
	prior, err := d.Src().PriorSrcCoverage(ctx, t2.ID, []int64{assetID}, time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("prior: %v", err)
	}
	if len(prior) != 1 || prior[0].TaskID != t1.ID || prior[0].Area != "authz" || prior[0].State != SrcStateClear {
		t.Fatalf("prior=%+v want task %d", prior, t1.ID)
	}
	// 当前任务自己的格不会出现在 prior_coverage 里。
	all, err := d.Src().PriorSrcCoverage(ctx, t2.ID, []int64{assetID}, time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		if c.TaskID == t2.ID {
			t.Fatalf("prior coverage leaked current task: %+v", c)
		}
	}
}
