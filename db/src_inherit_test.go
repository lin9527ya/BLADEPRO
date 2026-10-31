package db

// 跨任务覆盖继承的存储层集成测试（无 PG 自动跳过）。

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestInheritSrcCoverage(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	newTask := func(desc string) *Task {
		t.Helper()
		task, err := d.CreateTaskWithOptions(desc, "跨任务覆盖继承", TaskCreateOptions{Mode: "src"})
		if err != nil {
			t.Fatalf("create task: %v", err)
		}
		var expID int64
		if err := d.QueryRow(`SELECT exploration_id FROM tasks WHERE id=$1`, task.ID).Scan(&expID); err != nil {
			t.Fatalf("exploration for task: %v", err)
		}
		t.Cleanup(func() {
			_, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID)
			_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
		})
		return task
	}

	src := newTask("inherit-src")
	dst := newTask("inherit-dst")
	ctx := context.Background()
	store := d.Src()

	const domain = "inherit-coverage.example"
	assetID, err := d.Assets().UpsertSubdomain(UpsertSubdomainReq{Domain: domain, TaskID: src.ID})
	if err != nil {
		t.Fatalf("upsert subdomain: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM src_cells WHERE asset_id=$1`, assetID)
		_, _ = d.Exec(`DELETE FROM src_checks WHERE asset_id=$1`, assetID)
		_, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, assetID)
	})
	// 目标任务的范围内也要有这个资产（同一 asset 行），继承才有意义。
	if _, err := d.Exec(`INSERT INTO task_scope(task_id, kind, domain, source, reason)
VALUES ($1,'subdomain',$2,'manual','inherit test')`, dst.ID, domain); err != nil {
		t.Fatalf("dst scope: %v", err)
	}

	// 源任务：一格已测 + 一项收尾数字 + 一条不应被继承的发现/NA。
	if _, err := store.UpsertSrcCell(ctx, &SrcCell{
		TaskID: src.ID, AssetID: assetID, Area: "authz", Item: "*",
		State: SrcStateClear, Reason: "发过请求，无越权", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("src clear cell: %v", err)
	}
	if _, err := store.UpsertSrcCell(ctx, &SrcCell{
		TaskID: src.ID, AssetID: assetID, Area: "xss", Item: "*",
		State: SrcStateNA, Reason: "无输入点", NACode: "static_page", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("src na cell: %v", err)
	}
	if _, err := store.UpsertSrcCheck(ctx, &SrcCheck{
		TaskID: src.ID, AssetID: assetID, CheckKey: "paths_enum",
		State: SrcCheckDone, Metrics: map[string]int{"paths": 214}, UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("src check: %v", err)
	}
	if _, err := store.UpsertSrcCheck(ctx, &SrcCheck{
		TaskID: src.ID, AssetID: assetID, CheckKey: "js_enum",
		State: SrcCheckNA, NACode: "no_js", Reason: "无 JS", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("src na check: %v", err)
	}

	// 来源选择：显式 preferred 命中同资产重叠的任务。
	gotSrc, err := store.LatestInheritSource(ctx, dst.ID, 0, nil, []int64{src.ID}, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("latest source: %v", err)
	}
	if gotSrc != src.ID {
		t.Fatalf("source=%d want %d", gotSrc, src.ID)
	}
	// 没有项目/企业/显式来源时，按新任务范围内的共同资产兜底选源。
	if autoSrc, err := store.LatestInheritSource(ctx, dst.ID, 0, nil, nil, time.Now().Add(-24*time.Hour)); err != nil || autoSrc != src.ID {
		t.Fatalf("asset-overlap source=%d err=%v want %d", autoSrc, err, src.ID)
	}

	res, err := store.InheritSrcCoverage(ctx, src.ID, dst.ID)
	if err != nil {
		t.Fatalf("inherit: %v", err)
	}
	if res.Cells != 1 || res.Checks != 1 {
		t.Fatalf("inherited cells/checks=%d/%d want 1/1 (NA/budget_stop 与 NA check 不应复制)", res.Cells, res.Checks)
	}

	cell, err := store.GetSrcCell(ctx, dst.ID, assetID, "authz", "*")
	if err != nil || cell == nil {
		t.Fatalf("dst cell: %v %v", cell, err)
	}
	if cell.State != SrcStateClear || cell.InheritedFromTaskID != src.ID {
		t.Fatalf("dst cell=%+v want clear inherited from %d", cell, src.ID)
	}
	if !strings.Contains(cell.Reason, "继承自任务 #") {
		t.Fatalf("dst cell reason=%q 缺少继承来源", cell.Reason)
	}
	if cell.EvidenceNodeID != 0 {
		t.Fatalf("继承格不应伪造本任务证据节点：%d", cell.EvidenceNodeID)
	}
	if na, err := store.GetSrcCell(ctx, dst.ID, assetID, "xss", "*"); err != nil || na != nil {
		t.Fatalf("na cell should not be inherited: %+v %v", na, err)
	}

	check, err := store.GetSrcCheck(ctx, dst.ID, assetID, "paths_enum")
	if err != nil || check == nil {
		t.Fatalf("dst check: %v %v", check, err)
	}
	if check.State != SrcCheckDone || check.Metrics["paths"] != 214 || check.InheritedFromTaskID != src.ID {
		t.Fatalf("dst check=%+v", check)
	}
	if na, err := store.GetSrcCheck(ctx, dst.ID, assetID, "js_enum"); err != nil || na != nil {
		t.Fatalf("na check should not be inherited: %+v %v", na, err)
	}

	// 重测覆盖：正常 upsert 必须清掉继承标记，attempts 递增。
	re, err := store.UpsertSrcCell(ctx, &SrcCell{
		TaskID: dst.ID, AssetID: assetID, Area: "authz", Item: "*",
		State: SrcStateClear, Reason: "本轮复测，仍无越权", UpdatedBy: "worker",
	})
	if err != nil {
		t.Fatalf("re-mark: %v", err)
	}
	if re.InheritedFromTaskID != 0 {
		t.Fatalf("re-marked cell still inherited: %+v", re)
	}
	if re.Attempts != 1 {
		t.Fatalf("attempts=%d want 1（继承不消耗本轮尝试预算）", re.Attempts)
	}
	reCheck, err := store.UpsertSrcCheck(ctx, &SrcCheck{
		TaskID: dst.ID, AssetID: assetID, CheckKey: "paths_enum",
		State: SrcCheckDone, Metrics: map[string]int{"paths": 180}, UpdatedBy: "worker",
	})
	if err != nil {
		t.Fatalf("re-mark check: %v", err)
	}
	if reCheck.InheritedFromTaskID != 0 {
		t.Fatalf("re-marked check still inherited: %+v", reCheck)
	}
}

func TestInheritedCoverageBecomesStaleWhenSourceChanges(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	newTask := func(desc string) *Task {
		task, err := d.CreateTaskWithOptions(desc, "inherit-stale", TaskCreateOptions{Mode: "src"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
		return task
	}
	src, dst := newTask("stale-src"), newTask("stale-dst")
	ctx := context.Background()
	store := d.Src()
	const domain = "inherit-stale.example"
	assetID, err := d.Assets().UpsertSubdomain(UpsertSubdomainReq{Domain: domain, TaskID: src.ID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM src_cells WHERE asset_id=$1`, assetID)
		_, _ = d.Exec(`DELETE FROM src_checks WHERE asset_id=$1`, assetID)
		_, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, assetID)
	})
	if _, err := d.Exec(`INSERT INTO task_scope(task_id, kind, domain, source, reason)
VALUES ($1,'subdomain',$2,'manual','stale test')`, dst.ID, domain); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSrcCell(ctx, &SrcCell{TaskID: src.ID, AssetID: assetID, Area: "authz", Item: "*", State: SrcStateClear, Reason: "initial", UpdatedBy: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSrcCheck(ctx, &SrcCheck{TaskID: src.ID, AssetID: assetID, CheckKey: "paths_enum", State: SrcCheckDone, Metrics: map[string]int{"paths": 200}, UpdatedBy: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InheritSrcCoverage(ctx, src.ID, dst.ID); err != nil {
		t.Fatal(err)
	}
	staleCells, err := store.StaleInheritedCells(ctx, dst.ID)
	if err != nil || staleCells[""+strconv.FormatInt(assetID, 10)+"|authz|*"] {
		t.Fatalf("fresh inherited cell flagged stale: %v %v", staleCells, err)
	}
	// Source changes after the copy: the inherited result is now only historical.
	if _, err := store.UpsertSrcCell(ctx, &SrcCell{TaskID: src.ID, AssetID: assetID, Area: "authz", Item: "*", State: SrcStateClear, Reason: "changed after copy", UpdatedBy: "test"}); err != nil {
		t.Fatal(err)
	}
	staleCells, err = store.StaleInheritedCells(ctx, dst.ID)
	if err != nil || !staleCells[""+strconv.FormatInt(assetID, 10)+"|authz|*"] {
		t.Fatalf("changed inherited cell not stale: %v %v", staleCells, err)
	}
	if _, err := store.UpsertSrcCheck(ctx, &SrcCheck{TaskID: src.ID, AssetID: assetID, CheckKey: "paths_enum", State: SrcCheckDone, Metrics: map[string]int{"paths": 300}, UpdatedBy: "test"}); err != nil {
		t.Fatal(err)
	}
	chk, err := store.GetSrcCheck(ctx, dst.ID, assetID, "paths_enum")
	if err != nil || chk == nil {
		t.Fatalf("dst check: %v %v", chk, err)
	}
	if stale, err := store.InheritedCheckStale(ctx, *chk); err != nil || !stale {
		t.Fatalf("changed inherited check not stale: %v %v", stale, err)
	}
}
