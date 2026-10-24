package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestAssetTierFlowsIntoSrcGate pins the asset-tier contract: the tier lives in
// assets.extra (no wide-schema change), is readable through the SRC scope join,
// and clearing it restores the unclassified state.
func TestAssetTierFlowsIntoSrcGate(t *testing.T) {
	d, _, _ := testSetup(t)
	defer d.Close()
	ctx := context.Background()

	task, err := d.CreateTaskWithOptions("asset-tier", "资产分级", TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	var taskID int64
	if err := d.QueryRow(`SELECT id FROM tasks WHERE exploration_id=$1`, task.ExplorationID).Scan(&taskID); err != nil {
		t.Fatalf("task id: %v", err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(taskID) })

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	domain := "tier-" + suffix + ".example"
	assetID, err := d.Assets().UpsertSubdomain(UpsertSubdomainReq{Domain: domain, TaskID: taskID})
	if err != nil {
		t.Fatalf("upsert subdomain: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, assetID) })
	if _, err := d.Exec(`INSERT INTO task_scope(task_id, kind, domain, source, reason)
VALUES ($1,'subdomain',$2,'manual','asset tier test')`, taskID, domain); err != nil {
		t.Fatalf("insert task scope: %v", err)
	}

	if found, err := d.Assets().SetAssetTier(assetID, "primary"); err != nil || !found {
		t.Fatalf("set tier: found=%v err=%v", found, err)
	}
	asset, err := d.Src().SrcAssetInScope(ctx, taskID, assetID)
	if err != nil || asset == nil {
		t.Fatalf("src asset: %+v %v", asset, err)
	}
	if asset.Tier != "primary" {
		t.Fatalf("tier=%q want primary", asset.Tier)
	}
	assets, err := d.Src().InScopeSrcAssets(ctx, taskID)
	if err != nil {
		t.Fatalf("list assets: %v", err)
	}
	var listed bool
	for _, a := range assets {
		if a.ID == assetID {
			listed = a.Tier == "primary"
		}
	}
	if !listed {
		t.Fatalf("asset tier missing from InScopeSrcAssets: %+v", assets)
	}
	if score, err := d.Src().AutoTaskPriority(ctx, taskID); err != nil || score != 12 {
		t.Fatalf("auto priority=%d err=%v want 12", score, err)
	}

	if found, err := d.Assets().SetAssetTier(assetID, ""); err != nil || !found {
		t.Fatalf("clear tier: found=%v err=%v", found, err)
	}
	asset, err = d.Src().SrcAssetInScope(ctx, taskID, assetID)
	if err != nil || asset == nil || asset.Tier != "" {
		t.Fatalf("cleared tier: %+v %v", asset, err)
	}
}
