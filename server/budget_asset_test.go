package server

// 资产级 token 预算的引擎侧测试：达到上限后自动把该资产未完成格转 budget_stop，
// 已有的 worker 结论不改写，重复扫描不重复收口。

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

func TestCheckAssetTokenBudgetsStopsAsset(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()

	doc := srcmode.DefaultDoc()
	doc.AssetTokenBudget = 1000
	doc.AllowBudgetStop = true
	raw, _ := json.Marshal(doc)
	task, err := m.CreateTaskWithOptions("asset-budget", "资产级 token 预算",
		db.TaskCreateOptions{Mode: srcmode.ModeSrc, ModeDoc: raw})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	taskID, err := strconv.ParseInt(task.ID, 10, 64)
	if err != nil || taskID <= 0 {
		t.Fatalf("bad task id %q", task.ID)
	}
	t.Cleanup(func() { _ = m.pg.DeleteTask(taskID) })

	domain := "asset-budget-" + strconv.FormatInt(time.Now().UnixNano(), 10) + ".example"
	assetID, err := m.pg.Assets().UpsertSubdomain(db.UpsertSubdomainReq{Domain: domain, TaskID: taskID})
	if err != nil {
		t.Fatalf("upsert asset: %v", err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM assets WHERE id=$1`, assetID) })
	// UpsertSubdomain 只自动补根域 scope；本用例要按子域资产判定，显式加一条。
	if _, err := m.pg.Exec(`INSERT INTO task_scope(task_id, kind, domain, source, reason)
VALUES ($1,'subdomain',$2,'manual','asset budget test')`, taskID, domain); err != nil {
		t.Fatalf("dst scope: %v", err)
	}

	areas := doc.RequiredFor("subdomain")
	if len(areas) < 2 {
		t.Fatalf("fixture needs at least 2 required areas, got %v", areas)
	}
	ctx := context.Background()
	// 第一个面先放一条“本轮已测”的结论，预算收口必须保留它。
	if _, err := m.pg.Src().UpsertSrcCell(ctx, &db.SrcCell{
		TaskID: taskID, AssetID: assetID, Area: areas[0], Item: srcmode.AreaAll,
		State: db.SrcStateClear, Reason: "本轮已测", UpdatedBy: "worker",
	}); err != nil {
		t.Fatalf("seed clear cell: %v", err)
	}
	if err := m.pg.InsertLLMUsage(&db.LLMUsage{
		TaskID: strconv.FormatInt(taskID, 10), AssetID: assetID,
		Worker: "worker", InputTokens: 900, OutputTokens: 200, Status: "ok",
	}); err != nil {
		t.Fatalf("insert usage: %v", err)
	}

	tk, ok := m.Task(task.ID)
	if !ok {
		t.Fatal("task not in manager")
	}
	e := NewEngine(m)
	e.checkAssetTokenBudgets(ctx, tk)

	kept, err := m.pg.Src().GetSrcCell(ctx, taskID, assetID, areas[0], srcmode.AreaAll)
	if err != nil || kept == nil || kept.State != db.SrcStateClear {
		t.Fatalf("existing fresh cell must not be overwritten: %+v %v", kept, err)
	}
	items := []string{srcmode.AreaAll}
	if hard := doc.HardItemsFor(areas[1]); len(hard) > 0 {
		items = hard
	}
	stopped, err := m.pg.Src().GetSrcCell(ctx, taskID, assetID, areas[1], items[0])
	if err != nil || stopped == nil {
		t.Fatalf("blank cell should be budget_stop: %+v %v", stopped, err)
	}
	if stopped.State != db.SrcStateBudgetStop || !strings.Contains(stopped.Reason, "token 预算") {
		t.Fatalf("stopped cell=%+v", stopped)
	}
	if stopped.Attempts != 1 {
		t.Fatalf("attempts=%d want 1", stopped.Attempts)
	}

	// 再扫一次：同一个 (task, asset) 不重复收口。
	e.checkAssetTokenBudgets(ctx, tk)
	again, err := m.pg.Src().GetSrcCell(ctx, taskID, assetID, areas[1], items[0])
	if err != nil || again == nil || again.Attempts != 1 {
		t.Fatalf("second sweep touched the cell again: %+v %v", again, err)
	}
}
