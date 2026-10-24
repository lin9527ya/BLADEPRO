package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSrcFunnelAggregates(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	ctx := context.Background()

	task, err := d.CreateTaskWithOptions("funnel", "漏斗", TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID) })

	campaign, err := d.CreateCampaign(fmt.Sprintf("funnel-campaign-%d", time.Now().UnixNano()), "漏斗战役", nil)
	if err != nil {
		t.Fatalf("campaign: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM campaigns WHERE id=$1`, campaign.ID) })
	if _, err := d.Exec(`UPDATE tasks SET campaign_id=$1 WHERE id=$2`, campaign.ID, task.ID); err != nil {
		t.Fatalf("attach campaign: %v", err)
	}

	suffix := time.Now().UnixNano()
	root := fmt.Sprintf("funnel-%d.example", suffix)
	var assetA, assetB int64
	for i, target := range []*int64{&assetA, &assetB} {
		if err := d.QueryRow(`INSERT INTO assets(type, domain, root_domain)
VALUES ('subdomain',$1,$2) RETURNING id`, fmt.Sprintf("s%d.%s", i, root), root).Scan(target); err != nil {
			t.Fatalf("asset: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM assets WHERE id = ANY($1::bigint[])`, []int64{assetA, assetB})
	})
	if _, err := d.Exec(`INSERT INTO task_scope(task_id, kind, domain, source, reason)
VALUES ($1,'root_domain',$2,'manual','funnel')`, task.ID, root); err != nil {
		t.Fatalf("scope: %v", err)
	}
	if _, err := d.Src().UpsertSrcCell(ctx, &SrcCell{
		TaskID: task.ID, AssetID: assetA, Area: "authz", Item: "*",
		State: SrcStateClear, Reason: "已测", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("cell clear: %v", err)
	}
	var findingID int64
	if err := d.QueryRow(`INSERT INTO findings(task_id, vulnclass, name, severity, status)
VALUES ($1,'IDOR','漏斗发现','high','confirmed') RETURNING id`, task.ID).Scan(&findingID); err != nil {
		t.Fatalf("finding: %v", err)
	}
	if _, err := d.Src().UpsertSrcCell(ctx, &SrcCell{
		TaskID: task.ID, AssetID: assetB, Area: "authz", Item: "*",
		State: SrcStateFound, FindingID: findingID, UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("cell found: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO src_finding_meta(finding_id, task_id, report_ready) VALUES($1,$2,true)`, findingID, task.ID); err != nil {
		t.Fatalf("meta: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO src_submissions(task_id, finding_id, platform, status, title, severity)
VALUES ($1,$2,'test','accepted','漏斗发现','high')`, task.ID, findingID); err != nil {
		t.Fatalf("submission: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := d.InsertLLMUsage(&LLMUsage{TaskID: fmt.Sprint(task.ID), InputTokens: 100, OutputTokens: 50, CostUSD: 0.01, Status: "ok"}); err != nil {
			t.Fatalf("usage: %v", err)
		}
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM llm_usage WHERE task_id=$1`, fmt.Sprint(task.ID)) })

	f, err := d.SrcFunnel(ctx, SrcFunnelQuery{Days: 30})
	if err != nil {
		t.Fatalf("funnel: %v", err)
	}
	stages := map[string]int64{}
	for _, s := range f.Stages {
		stages[s.Key] = s.Value
	}
	if stages["findings_medium_plus"] < 1 || stages["reports_ready"] < 1 || stages["accepted"] < 1 {
		t.Fatalf("stages=%+v", stages)
	}
	if stages["assets_in_scope"] < 2 || stages["assets_terminal"] < 2 || stages["assets_found"] < 1 {
		t.Fatalf("asset stages=%+v", stages)
	}
	if f.Tasks < 1 || f.CellsTerminal < 2 || f.Tokens < 300 {
		t.Fatalf("totals=%+v", f)
	}
	if f.CostPerEffective <= 0 {
		t.Fatalf("cost per effective=%v", f.CostPerEffective)
	}
	foundProgram := false
	for _, p := range f.ByProgram {
		if p.ProgramID == 0 && p.Tasks >= 1 {
			foundProgram = true
		}
	}
	if !foundProgram {
		t.Fatalf("by_program=%+v", f.ByProgram)
	}
	foundCampaign := false
	for _, c := range f.ByCampaign {
		if c.CampaignID == campaign.ID && c.Tasks >= 1 && c.Accepted >= 1 {
			foundCampaign = true
		}
	}
	if !foundCampaign {
		t.Fatalf("by_campaign=%+v", f.ByCampaign)
	}
	foundSeverity := false
	for _, s := range f.BySeverity {
		if s.Severity == "high" && s.Findings >= 1 && s.Accepted >= 1 {
			foundSeverity = true
		}
	}
	if !foundSeverity {
		t.Fatalf("by_severity=%+v", f.BySeverity)
	}
	if len(f.Daily) == 0 {
		t.Fatal("daily series empty")
	}
}
