package db

// 批量扇出的存储层测试：资产解析（企业/来源任务/显式 id 并集）与创建时
// 精确 scope + asset link 的原子写入。

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestResolveBatchAssetsAndShardScope(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	ctx := context.Background()

	var companyID int64
	nkey := fmt.Sprintf("batch-fanout-test-%d", time.Now().UnixNano())
	if err := d.QueryRow(`INSERT INTO companies(name,nkey) VALUES($1,$2) RETURNING id`,
		"批量扇出测试公司", nkey).Scan(&companyID); err != nil {
		t.Fatalf("create company: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM companies WHERE id=$1`, companyID) })

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	root := "batch-" + suffix + ".example"
	insertAsset := func(domain string, company int64) int64 {
		t.Helper()
		var id int64
		var companyArg any
		if company > 0 {
			companyArg = company
		}
		if err := d.QueryRow(`INSERT INTO assets(type, domain, root_domain, company_id)
VALUES ('subdomain',$1,$2,$3) RETURNING id`, domain, root, companyArg).Scan(&id); err != nil {
			t.Fatalf("insert asset %s: %v", domain, err)
		}
		t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, id) })
		return id
	}
	a1 := insertAsset("a1."+root, companyID)
	a2 := insertAsset("a2."+root, companyID)
	a3 := insertAsset("other."+root, 0)

	byCompany, truncated, err := d.ResolveBatchAssets(ctx, []int64{companyID}, nil, nil, nil, 100)
	if err != nil || truncated {
		t.Fatalf("by company: %v truncated=%v", err, truncated)
	}
	if len(byCompany) != 2 || byCompany[0].ID != a1 || byCompany[1].ID != a2 {
		t.Fatalf("company assets=%+v want [%d %d]", byCompany, a1, a2)
	}
	// 显式 id 与类型过滤。
	byIDs, _, err := d.ResolveBatchAssets(ctx, nil, nil, []int64{a1, a3}, []string{"subdomain"}, 100)
	if err != nil || len(byIDs) != 2 {
		t.Fatalf("by ids=%+v err=%v", byIDs, err)
	}
	// 上限截断。
	limited, truncated, err := d.ResolveBatchAssets(ctx, []int64{companyID}, nil, nil, nil, 1)
	if err != nil || !truncated || len(limited) != 1 {
		t.Fatalf("limit: %+v truncated=%v err=%v", limited, truncated, err)
	}

	// 来源任务：task_asset_links 命中的资产也算进范围。
	source, err := d.CreateTaskWithOptions("batch-source", "来源任务", TaskCreateOptions{})
	if err != nil {
		t.Fatalf("create source task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, source.ID)
	})
	if _, err := d.Exec(`INSERT INTO task_asset_links(task_id, asset_id, source)
VALUES ($1,$2,'manual')`, source.ID, a3); err != nil {
		t.Fatalf("link source asset: %v", err)
	}
	bySource, _, err := d.ResolveBatchAssets(ctx, nil, []int64{source.ID}, nil, nil, 100)
	if err != nil || len(bySource) != 1 || bySource[0].ID != a3 {
		t.Fatalf("by source=%+v err=%v", bySource, err)
	}

	// 建任务时原子写入精确 scope 与资产关联。
	shard, err := d.CreateTaskWithOptions("batch-shard", "分片任务", TaskCreateOptions{
		ScopeRows:   []TaskScopeInput{{Kind: "subdomain", Domain: "a1." + root, Reason: "test"}},
		AssetIDs:    []int64{a1},
		TokenBudget: 1234,
	})
	if err != nil {
		t.Fatalf("create shard: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, shard.ID)
	})
	if shard.TokenBudget != 1234 {
		t.Fatalf("token budget=%d want 1234", shard.TokenBudget)
	}
	var scopeDomain string
	if err := d.QueryRow(`SELECT COALESCE(domain,'') FROM task_scope WHERE task_id=$1 AND kind='subdomain'`, shard.ID).Scan(&scopeDomain); err != nil {
		t.Fatalf("scope: %v", err)
	}
	if scopeDomain != "a1."+root {
		t.Fatalf("scope domain=%q", scopeDomain)
	}
	var linked bool
	if err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM task_asset_links WHERE task_id=$1 AND asset_id=$2)`, shard.ID, a1).Scan(&linked); err != nil || !linked {
		t.Fatalf("asset link: %v linked=%v", err, linked)
	}
	// 非法 scope 行必须在事务内回滚，不留下半成品任务。
	if _, err := d.CreateTaskWithOptions("bad-shard", "坏分片", TaskCreateOptions{
		ScopeRows: []TaskScopeInput{{Kind: "subdomain"}},
	}); err == nil {
		t.Fatal("missing domain should fail")
	}
}
