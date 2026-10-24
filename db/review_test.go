package db

// 人工复核队列的聚合与裁决留痕。

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestReviewQueueAggregatesAndDecisions(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	ctx := context.Background()

	task, err := d.CreateTaskWithOptions("review-queue", "复核队列", TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID) })

	suffix := time.Now().UnixNano()
	var assetA, assetB int64
	for i, target := range []*int64{&assetA, &assetB} {
		if err := d.QueryRow(`INSERT INTO assets(type, domain, root_domain)
VALUES ('subdomain',$1,$2) RETURNING id`,
			fmt.Sprintf("review-%d-%d.example", suffix, i), "review.example").Scan(target); err != nil {
			t.Fatalf("asset: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM assets WHERE id = ANY($1::bigint[])`, []int64{assetA, assetB})
	})

	// budget_stop 格
	if _, err := d.Src().UpsertSrcCell(ctx, &SrcCell{
		TaskID: task.ID, AssetID: assetA, Area: "authz", Item: "*",
		State: SrcStateBudgetStop, Reason: "预算用尽", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("budget cell: %v", err)
	}
	// 四件套全 N/A 的资产
	for _, area := range []string{"injection", "ssrf", "xss", "rce"} {
		if _, err := d.Src().UpsertSrcCell(ctx, &SrcCell{
			TaskID: task.ID, AssetID: assetB, Area: area, Item: "*",
			State: SrcStateNA, NACode: "static_page", Reason: "无输入", UpdatedBy: "test",
		}); err != nil {
			t.Fatalf("na cell: %v", err)
		}
	}

	// report_missing：medium 发现没有 report_ready。
	var reportFinding int64
	if err := d.QueryRow(`INSERT INTO findings(task_id, vulnclass, name, severity, status)
VALUES ($1,'IDOR','越权读取','medium','pending') RETURNING id`, task.ID).Scan(&reportFinding); err != nil {
		t.Fatalf("finding: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO src_finding_meta(finding_id, task_id, report_ready) VALUES($1,$2,false)`, reportFinding, task.ID); err != nil {
		t.Fatalf("meta: %v", err)
	}
	// dedupe_pending：dedupe_of 指向 reportFinding，且没有 duplicate 台账。
	var dupFinding int64
	if err := d.QueryRow(`INSERT INTO findings(task_id, vulnclass, name, severity, status)
VALUES ($1,'IDOR','同洞另一入口','high','pending') RETURNING id`, task.ID).Scan(&dupFinding); err != nil {
		t.Fatalf("dup finding: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO src_finding_meta(finding_id, task_id, report_ready, dedupe_of)
VALUES($1,$2,true,$3)`, dupFinding, task.ID, reportFinding); err != nil {
		t.Fatalf("dedupe meta: %v", err)
	}
	// retest_inconclusive：最近一次 completed 结论。
	if _, err := d.Exec(`INSERT INTO finding_retests(finding_id, status, verdict, snapshot, summary, finished_at)
VALUES ($1,'completed','inconclusive','{}'::jsonb,'签名已过期，无法确认',now())`, dupFinding); err != nil {
		t.Fatalf("retest: %v", err)
	}

	// SLA：ready 超过 7 天未提交也要进队列。
	var staleFinding int64
	if err := d.QueryRow(`INSERT INTO findings(task_id, vulnclass, name, severity, status)
VALUES ($1,'XSS','待提交超时','medium','confirmed') RETURNING id`, task.ID).Scan(&staleFinding); err != nil {
		t.Fatalf("stale finding: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO src_submissions(task_id, finding_id, platform, status, title, severity, created_at, updated_at)
VALUES ($1,$2,'','ready','待提交超时','medium', now()-interval '10 days', now()-interval '10 days')`, task.ID, staleFinding); err != nil {
		t.Fatalf("stale submission: %v", err)
	}

	items, err := d.ReviewQueue(ctx, 100, 7)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	byKind := map[string]ReviewItem{}
	for _, it := range items {
		if it.TaskID == task.ID {
			byKind[it.Kind] = it
		}
	}
	for _, kind := range []string{"report_missing", "retest_inconclusive", "dedupe_pending", "budget_stop", "suspicious_na", "ready_stale"} {
		if _, ok := byKind[kind]; !ok {
			t.Fatalf("missing kind %s in %+v", kind, items)
		}
	}
	if byKind["budget_stop"].AssetID != assetA || byKind["budget_stop"].Area != "authz" {
		t.Fatalf("budget item=%+v", byKind["budget_stop"])
	}
	if byKind["suspicious_na"].AssetID != assetB {
		t.Fatalf("na item=%+v", byKind["suspicious_na"])
	}
	if byKind["dedupe_pending"].FindingID != dupFinding {
		t.Fatalf("dedupe item=%+v", byKind["dedupe_pending"])
	}

	// 裁决留痕：已裁决项可按 kind|ref 读回；同名 upsert 更新 action。
	it := byKind["budget_stop"]
	if err := d.RecordReviewDecision(ctx, it.Kind, it.RefKey, "accept", "接受预算停"); err != nil {
		t.Fatalf("decision: %v", err)
	}
	decisions, err := d.ReviewDecisions(ctx)
	if err != nil || decisions[it.Kind+"|"+it.RefKey] != "accept" {
		t.Fatalf("decisions=%v err=%v", decisions, err)
	}
	if err := d.RecordReviewDecision(ctx, it.Kind, it.RefKey, "retest", "改判重测"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.GetReviewDecision(ctx, it.Kind, it.RefKey); got != "retest" {
		t.Fatalf("decision=%q want retest", got)
	}
}
