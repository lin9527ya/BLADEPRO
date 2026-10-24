package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lin9527ya/bladepro/db"
)

func TestAutoShardPriority(t *testing.T) {
	assets := []db.BatchAsset{
		{Tier: "primary"}, {Tier: "api"}, {Tier: "third_party"}, {Tier: ""},
	}
	if got := autoShardPriority(assets, 5, 0); got != 5+12+8-3 {
		t.Fatalf("auto priority=%d", got)
	}
	if got := autoShardPriority(assets, 5, -20); got != -20 {
		t.Fatalf("explicit priority must win: %d", got)
	}
}

func TestCampaignBudgetForTask(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	ctx := context.Background()
	campaign, err := m.pg.CreateCampaign(fmt.Sprintf("budget-%d", time.Now().UnixNano()), "预算", nil, 7, 1000)
	if err != nil {
		t.Fatalf("campaign: %v", err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM campaigns WHERE id=$1`, campaign.ID) })
	task, err := m.CreateTaskWithOptions("campaign-budget", "战役预算", db.TaskCreateOptions{})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID) })
	if err := m.pg.SetTaskCampaign(parseIntDefault(task.ID), &campaign.ID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := m.pg.InsertLLMUsage(&db.LLMUsage{TaskID: task.ID, InputTokens: 300, OutputTokens: 200, Status: "ok"}); err != nil {
		t.Fatalf("usage: %v", err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM llm_usage WHERE task_id=$1`, task.ID) })
	cid, budget, used, err := m.pg.CampaignBudgetForTask(ctx, parseIntDefault(task.ID))
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if cid != campaign.ID || budget != 1000 || used != 500 {
		t.Fatalf("cid=%d budget=%d used=%d", cid, budget, used)
	}
}
