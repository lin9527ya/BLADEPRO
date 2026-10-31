package srcmode

// 收尾闸的集成测试：走真实 PostgreSQL（与 gate_integration_test.go 同一套准备），
// 覆盖「缺项 → 缺口」「数字不达标 → 缺口」「达标 → 放行」三段。

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestCheckFinishChecksGate(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skipf("no postgres configured (%v) — skipping", err)
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	raw, doc, err := Resolve(ModeSrc, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// 说明型 clear 由专门的 ValidateCell 用例覆盖，这里放开以聚焦收尾闸。
	doc.AllowReasonOnlyClear = true
	task, err := d.CreateTaskWithOptions("src-checks-test", "收尾闸闭环", db.TaskCreateOptions{
		Mode: ModeSrc, ModeDoc: raw,
		ScopeRows: []db.TaskScopeInput{{Kind: "subdomain", Domain: "cdn.src-checks-test.example", Reason: "人工声明测试范围"}},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	var expID int64
	if err := d.QueryRow(`SELECT exploration_id FROM tasks WHERE id=$1`, task.ID).Scan(&expID); err != nil {
		t.Fatalf("exploration for task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM src_checks WHERE task_id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM src_cells WHERE task_id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	})

	as := d.Assets()
	domain := "src-checks-test.example"
	// 用 subdomain scope 只框住这一条资产（root_domain scope 会连自动补建的根域一起算）。
	if _, err := as.AddAgentScope(task.ID, "subdomain", "cdn."+domain, "集成测试范围", "agent"); err != nil {
		t.Fatalf("add scope: %v", err)
	}
	assetID, err := as.UpsertSubdomain(db.UpsertSubdomainReq{Domain: "cdn." + domain, TaskID: task.ID})
	if err != nil {
		t.Fatalf("upsert subdomain: %v", err)
	}

	ctx := context.Background()
	store := d.Src()
	assets, err := store.InScopeSrcAssets(ctx, task.ID)
	if err != nil || len(assets) != 1 {
		t.Fatalf("in-scope assets: %v %+v", err, assets)
	}

	// 先把「面」全部点掉：本用例只观察收尾闸那一层的缺口。
	for _, area := range doc.RequiredFor(assets[0].Type) {
		if _, err := store.UpsertSrcCell(ctx, &db.SrcCell{
			TaskID: task.ID, AssetID: assetID, Area: area, Item: AreaAll,
			State: db.SrcStateClear, Reason: "集成测试：发过请求", UpdatedBy: "test",
		}); err != nil {
			t.Fatalf("upsert cell %s: %v", area, err)
		}
	}

	defs := ChecksFor(assets[0].Type)
	if len(defs) != 1 || defs[0].Key != "unreachable_proof" {
		t.Fatalf("subdomain 只应适用判死三条件，got %+v", defs)
	}

	// 1) 收尾项没做 → missing_check 缺口，不许收尾。
	rep, err := CheckFinish(ctx, store, task.ID, doc)
	if err != nil {
		t.Fatalf("check finish: %v", err)
	}
	if rep.ChecksTotal != 1 || rep.ChecksDone != 0 {
		t.Fatalf("checks_total/done = %d/%d, want 1/0", rep.ChecksTotal, rep.ChecksDone)
	}
	if rep.Finishable {
		t.Fatal("收尾项没做不该 finishable")
	}
	var found bool
	for _, g := range rep.Gaps {
		if g.Kind == GapMissingCheck && g.Item == "unreachable_proof" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应出现 missing_check 缺口：%+v", rep.Gaps)
	}

	// 2) 标 done 但数字不达标 → 仍是缺口，且理由写清差多少。
	if _, err := store.UpsertSrcCheck(ctx, &db.SrcCheck{
		TaskID: task.ID, AssetID: assetID, CheckKey: "unreachable_proof",
		State: db.SrcCheckDone, UpdatedBy: "test",
		Metrics: map[string]int{"local_unreachable": 1, "external_exits": 1, "dns_resolvers": 2},
	}); err != nil {
		t.Fatalf("upsert check: %v", err)
	}
	rep, err = CheckFinish(ctx, store, task.ID, doc)
	if err != nil {
		t.Fatalf("check finish: %v", err)
	}
	if rep.ChecksDone != 0 {
		t.Fatalf("未经核验的数字不能记为完成，got %d", rep.ChecksDone)
	}
	if rep.Finishable {
		t.Fatal("数字不达标不该 finishable")
	}
	found = false
	for _, g := range rep.Gaps {
		if g.Kind == GapMissingCheck && strings.Contains(g.Reason, "external_exits") {
			found = true
		}
	}
	if !found {
		t.Fatalf("应出现「数字不达标」缺口：%+v", rep.Gaps)
	}

	// 3) 补齐数字 → 缺口清零，可收尾。
	nodeID, err := d.Exploration(expID).AddNode(db.KindFact, map[string]any{"summary": "人工核对了不可达证明的两个出口及两个 DNS 结果"}, 1, "confirmed", "test", []int64{assetID})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.UpsertSrcCheck(ctx, &db.SrcCheck{
		TaskID: task.ID, AssetID: assetID, CheckKey: "unreachable_proof",
		State: db.SrcCheckDone, UpdatedBy: "test",
		EvidenceNodeID: nodeID,
		Metrics:        map[string]int{"local_unreachable": 1, "external_exits": 2, "dns_resolvers": 2},
	}); err != nil {
		t.Fatalf("upsert check: %v", err)
	}
	pending, err := store.GetSrcCheck(ctx, task.ID, assetID, "unreachable_proof")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApproveSrcCheck(ctx, task.ID, assetID, "unreachable_proof", fmt.Sprintf("check_unverified:%d:%d", pending.ID, pending.UpdatedAt.UnixNano()), "人工核对出口与 DNS 原始结果"); err != nil {
		t.Fatal(err)
	}
	rep, err = CheckFinish(ctx, store, task.ID, doc)
	if err != nil {
		t.Fatalf("check finish: %v", err)
	}
	if !rep.Finishable || rep.GapsTotal != 0 {
		t.Fatalf("补齐后应可收尾：gaps=%d finishable=%v (%+v)", rep.GapsTotal, rep.Finishable, rep.Gaps)
	}

	// 4) N/A 走理由码：把行改成 na(reachable) 也应当放行（并且不再是缺口）。
	if _, err := store.UpsertSrcCheck(ctx, &db.SrcCheck{
		TaskID: task.ID, AssetID: assetID, CheckKey: "unreachable_proof",
		State: db.SrcCheckNA, NACode: "reachable", Reason: "资产可达", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("upsert check na: %v", err)
	}
	rep, err = CheckFinish(ctx, store, task.ID, doc)
	if err != nil {
		t.Fatalf("check finish: %v", err)
	}
	if !rep.Finishable || rep.ChecksDone != 1 {
		t.Fatalf("na(reachable) 应算终态并放行：done=%d finishable=%v", rep.ChecksDone, rep.Finishable)
	}
}
