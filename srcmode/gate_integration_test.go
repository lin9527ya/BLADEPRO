package srcmode

// 收尾门的集成测试：走真实 PostgreSQL（本仓既有测试的习惯），没有库就整体跳过。
// 放在 srcmode 包而不是 db 包，是因为 gate 的判定逻辑在这里——db 只提供行。

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

// TestCheckFinishAgainstRealCells 走一遍 SRC 任务的账面闭环：
// 新任务 → 有必做面 → 全部是空格 → 逐格点 tested_clear → 收尾门放行。
func TestCheckFinishAgainstRealCells(t *testing.T) {
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
	// 本用例只验证「格」这条线；收尾闸那一层由 TestCheckFinishChecksGate 覆盖。
	doc.SkipCheckGate = true
	task, err := d.CreateTaskWithOptions("src-gate-test", "把覆盖格清零", db.TaskCreateOptions{
		Mode: ModeSrc, ModeDoc: raw,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	var expID int64
	if err := d.QueryRow(`SELECT exploration_id FROM tasks WHERE id=$1`, task.ID).Scan(&expID); err != nil {
		t.Fatalf("exploration for task: %v", err)
	}
	t.Cleanup(func() {
		// task 删除会级联 src_cells/session 相关行；exploration 单独删。
		_, _ = d.Exec(`DELETE FROM src_cells WHERE task_id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	})

	as := d.Assets()
	domain := "src-gate-test.example"
	if _, err := as.AddAgentScope(task.ID, "root_domain", domain, "集成测试范围", "agent"); err != nil {
		t.Fatalf("add scope: %v", err)
	}
	assetID, err := as.UpsertSubdomain(db.UpsertSubdomainReq{Domain: "api." + domain, TaskID: task.ID})
	if err != nil {
		t.Fatalf("upsert subdomain: %v", err)
	}
	store := d.Src()
	ctx := context.Background()

	// 1) 模式快照能读回来。
	mode, back, err := store.TaskMode(ctx, task.ID)
	if err != nil {
		t.Fatalf("task mode: %v", err)
	}
	if mode != ModeSrc {
		t.Fatalf("mode = %q, want %q", mode, ModeSrc)
	}
	if parsed := Parse(back); !parsed.IsSrc() {
		t.Fatalf("mode_doc 快照丢失：%s", back)
	}

	// 2) 范围内资产被算进分母。
	assets, err := store.InScopeSrcAssets(ctx, task.ID)
	if err != nil {
		t.Fatalf("in-scope assets: %v", err)
	}
	if len(assets) != 1 || assets[0].ID != assetID {
		t.Fatalf("范围内资产应为 1 个（%d），got %+v", assetID, assets)
	}

	// 3) 一格没点 → 全是缺口，不许收尾。
	rep, err := CheckFinish(ctx, store, task.ID, doc)
	if err != nil {
		t.Fatalf("check finish: %v", err)
	}
	required := doc.RequiredFor(assets[0].Type)
	if rep.CellsTotal != len(required) {
		t.Fatalf("cells_total = %d, want %d（%s 的必做面数）", rep.CellsTotal, len(required), assets[0].Type)
	}
	if rep.GapsTotal != len(required) || rep.Finishable {
		t.Fatalf("空格应全部算缺口且不可收尾：gaps=%d finishable=%v", rep.GapsTotal, rep.Finishable)
	}
	for _, g := range rep.Gaps {
		if g.Kind != GapBlank {
			t.Fatalf("空格缺口 kind = %q, want %q", g.Kind, GapBlank)
		}
	}

	// 4) 逐格点 tested_clear（带说明）→ 缺口清零。
	for _, area := range required {
		if err := ValidateCell(ctx, store, task.ID, doc, db.SrcCell{
			TaskID: task.ID, AssetID: assetID, Area: area, Item: AreaAll,
			State: db.SrcStateClear, Reason: "集成测试：发过请求，无异常",
		}); err != nil {
			t.Fatalf("validate %s: %v", area, err)
		}
		if _, err := store.UpsertSrcCell(ctx, &db.SrcCell{
			TaskID: task.ID, AssetID: assetID, Area: area, Item: AreaAll,
			State: db.SrcStateClear, Reason: "集成测试：发过请求，无异常", UpdatedBy: "test",
		}); err != nil {
			t.Fatalf("upsert %s: %v", area, err)
		}
	}
	rep, err = CheckFinish(ctx, store, task.ID, doc)
	if err != nil {
		t.Fatalf("check finish after marks: %v", err)
	}
	if !rep.Finishable || rep.GapsTotal != 0 {
		t.Fatalf("全部点终态后应可收尾：gaps=%d finishable=%v (%+v)", rep.GapsTotal, rep.Finishable, rep.Gaps)
	}
	if rep.CellsMeasured != len(required) {
		t.Fatalf("cells_measured = %d, want %d", rep.CellsMeasured, len(required))
	}

	// 5) 范围外资产不能点格。
	otherID, err := as.UpsertSubdomain(db.UpsertSubdomainReq{Domain: "outside.example"})
	if err != nil {
		t.Fatalf("upsert outside subdomain: %v", err)
	}
	err = ValidateCell(ctx, store, task.ID, doc, db.SrcCell{
		TaskID: task.ID, AssetID: otherID, Area: "paths", Item: AreaAll,
		State: db.SrcStateClear, Reason: "越界",
	})
	if err == nil {
		t.Fatal("范围外资产点格必须被拒绝")
	}

	// 6) 尝试上限：同一格反复点，超过 max_attempts_per_cell 后只能用 budget_stop 收口。
	for i := 0; i < doc.MaxAttemptsPerCell; i++ {
		if _, err := store.UpsertSrcCell(ctx, &db.SrcCell{
			TaskID: task.ID, AssetID: assetID, Area: "paths", Item: AreaAll,
			State: db.SrcStateClear, Reason: "重试", UpdatedBy: "test",
		}); err != nil {
			t.Fatalf("bump attempts: %v", err)
		}
	}
	cell, err := store.GetSrcCell(ctx, task.ID, assetID, "paths", AreaAll)
	if err != nil || cell == nil {
		t.Fatalf("read cell: %v %v", cell, err)
	}
	if cell.Attempts < doc.MaxAttemptsPerCell {
		t.Fatalf("attempts = %d, want >= %d", cell.Attempts, doc.MaxAttemptsPerCell)
	}
	err = ValidateCell(ctx, store, task.ID, doc, db.SrcCell{
		TaskID: task.ID, AssetID: assetID, Area: "paths", Item: AreaAll,
		State: db.SrcStateClear, Reason: "还想再试",
	})
	if err == nil {
		t.Fatal("超过尝试上限后必须拒绝再用 tested_clear，要求 budget_stop")
	}
	if err := ValidateCell(ctx, store, task.ID, doc, db.SrcCell{
		TaskID: task.ID, AssetID: assetID, Area: "paths", Item: AreaAll,
		State: db.SrcStateBudgetStop, Reason: "目录枚举预算用尽",
	}); err != nil {
		t.Fatalf("budget_stop 应被允许收口: %v", err)
	}
}

// TestSrcCellJSONShape pins the wire shape the UI/agent read (a renamed field would
// silently blank the matrix rather than fail loudly).
func TestSrcCellJSONShape(t *testing.T) {
	raw, err := json.Marshal(db.SrcCell{AssetID: 3, Area: "authz", Item: "*", State: db.SrcStateFound, FindingID: 9})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"asset_id", "area", "item", "state", "finding_id"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("SrcCell JSON 缺字段 %q：%s", key, raw)
		}
	}
}
