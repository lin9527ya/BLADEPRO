package db

import (
	"encoding/json"
	"testing"
)

// TestKnowledgeEntriesLifecycle 覆盖统一知识表的写入/检索/反馈/回填幂等。
func TestKnowledgeEntriesLifecycle(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	cs := d.Companies()
	as := d.Assets()

	companyID, err := cs.UpsertByName("知识体系测试公司")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM companies WHERE id=$1`, companyID)

	e, err := as.InsertKnowledgeEntry(KnowledgeEntry{
		Scope: KnowledgeScopeCompany, CompanyID: companyID,
		EntryType: KnowledgeTypeMemory, Key: "waf_profile", Summary: "目标 WAF 对 union select 返回 403",
		BodyMD: "细节：...", MemoryKind: "waf_profile", Confidence: "observed",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if e.ID == 0 || e.Status != KnowledgeStatusActive {
		t.Fatalf("写入结果异常: %+v", e)
	}

	rows, err := as.LookupKnowledge(KnowledgeQuery{
		Scope: KnowledgeScopeCompany, CompanyID: companyID,
		Types: []string{KnowledgeTypeMemory}, Text: "union", Limit: 5,
	})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != e.ID {
		t.Fatalf("检索命中异常: %+v", rows)
	}

	// 注入面不 bump hits；显式 Touch 才计数。
	if rows[0].Hits != 0 {
		t.Fatalf("注入/检索不应自增 hits，实际 %d", rows[0].Hits)
	}
	if err := as.TouchKnowledge([]int64{e.ID}); err != nil {
		t.Fatal(err)
	}
	got, _ := as.GetKnowledgeEntry(e.ID)
	if got == nil || got.Hits != 1 {
		t.Fatalf("Touch 后 hits 应为 1: %+v", got)
	}

	// 反馈：worked → successes+1 且复核时间更新。
	if err := as.FeedbackKnowledge(e.ID, "worked", "打穿成功"); err != nil {
		t.Fatal(err)
	}
	got, _ = as.GetKnowledgeEntry(e.ID)
	if got.Successes != 1 || got.LastOutcome != "worked" || got.LastVerifiedAt == nil {
		t.Fatalf("反馈未生效: %+v", got)
	}

	// 状态机：stale 后按 active 优先排序不再命中第一梯队（此处只验证状态写入）。
	if err := as.FeedbackKnowledge(e.ID, "stale", "目标已改版"); err != nil {
		t.Fatal(err)
	}
	got, _ = as.GetKnowledgeEntry(e.ID)
	if got.Status != KnowledgeStatusStale {
		t.Fatalf("stale 未生效: %+v", got)
	}
}

// TestKnowledgeBackfillIdempotent 旧表回填可重跑且不产生重复。
func TestKnowledgeBackfillIdempotent(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	companyID, err := d.Companies().UpsertByName("回填测试公司")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM companies WHERE id=$1`, companyID)
	if _, err := d.Assets().CreateCompanyKnowledge(CompanyKnowledge{
		CompanyID: companyID, Kind: KnowledgeKindFact, Summary: "回填测试事实", Detail: "细节",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.BackfillKnowledgeFromLegacy(); err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	if _, _, err := d.BackfillKnowledgeFromLegacy(); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM knowledge_entries
		WHERE legacy_table='company_knowledge' AND company_id=$1`, companyID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("回填应幂等（1 条），实际 %d", n)
	}
}

// TestApprovalsLifecycle 审批创建/列表/幂等裁决。
func TestApprovalsLifecycle(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	as := d.Assets()
	a, err := as.CreateApproval(ApprovalKindKnowledge, json.RawMessage(`{"title":"t"}`), "测试提案")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM approvals WHERE id=$1`, a.ID)
	if a.Status != "pending" {
		t.Fatalf("新提案应为 pending: %+v", a)
	}
	list, err := as.ListApprovals("pending", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range list {
		if x.ID == a.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("pending 列表里找不到刚创建的提案")
	}
	dec, changed, err := as.DecideApproval(a.ID, true, "tester")
	if err != nil || !changed || dec.Status != "approved" {
		t.Fatalf("首次裁决异常: %+v changed=%v err=%v", dec, changed, err)
	}
	again, changed2, err := as.DecideApproval(a.ID, false, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if changed2 || again.Status != "approved" {
		t.Fatalf("重复裁决应幂等且不覆盖: changed=%v status=%s", changed2, again.Status)
	}
}

// TestCompanyProfileCRUDAndDepth 公司档案读写与深度解析。
func TestCompanyProfileCRUDAndDepth(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	cs := d.Companies()
	companyID, err := cs.UpsertByName("厂商档案测试公司")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM companies WHERE id=$1`, companyID)
	if err := cs.UpsertCompanyProfile(CompanyProfile{
		CompanyID: companyID, MaxDepth: 3, BannedVulnClasses: []string{"dos"},
		Domains: []string{"example.com"}, CheckURL: "https://example.com/login",
	}); err != nil {
		t.Fatal(err)
	}
	p, err := cs.GetCompanyProfile(companyID)
	if err != nil || p == nil {
		t.Fatalf("读取档案失败: %v", err)
	}
	if p.MaxDepth != 3 || len(p.BannedVulnClasses) != 1 || p.Domains[0] != "example.com" {
		t.Fatalf("档案字段异常: %+v", p)
	}
}
