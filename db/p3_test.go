package db

import (
	"strings"
	"testing"
)

// TestTestAccountLifecycle 账号池：掩码、明文读取、探活状态机、节流。
func TestTestAccountLifecycle(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	as := d.Assets()
	companyID, err := d.Companies().UpsertByName("账号池测试公司")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM companies WHERE id=$1`, companyID)

	a, err := as.CreateTestAccount(TestAccount{
		CompanyID: companyID, Label: "管理员", RoleTier: "admin", CredType: "cookie", OwnerPhone: "13800000000",
	}, "session=abcdefghijklmn")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer d.Exec(`DELETE FROM test_accounts WHERE id=$1`, a.ID)
	if a.CredMasked == "" || strings.Contains(a.CredMasked, "abcdef") {
		t.Fatalf("列表必须掩码，实际 %q", a.CredMasked)
	}
	got, cred, err := as.GetTestAccountCredential(a.ID)
	if err != nil || got == nil || cred != "session=abcdefghijklmn" {
		t.Fatalf("明文读取异常: got=%+v cred=%q err=%v", got, cred, err)
	}
	if err := as.RecordAccountCheck(a.ID, false, "401"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = as.GetTestAccountCredential(a.ID)
	if got.Status != "expired" || got.CheckFailStreak != 1 {
		t.Fatalf("失败一次应为 expired/streak=1: %+v", got)
	}
	if !as.AccountCheckThrottled(a.ID) {
		t.Fatal("刚探活过应命中 10 分钟节流")
	}
	for i := 0; i < 2; i++ {
		_ = as.RecordAccountCheck(a.ID, false, "401")
	}
	got, _, _ = as.GetTestAccountCredential(a.ID)
	if got.Status != "banned" {
		t.Fatalf("连续失败 3 次应为 banned: %+v", got)
	}
	if err := as.RecordAccountCheck(a.ID, true, "200"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = as.GetTestAccountCredential(a.ID)
	if got.Status != "active" || got.CheckFailStreak != 0 {
		t.Fatalf("成功后应恢复 active 并清零: %+v", got)
	}
}

// TestEffectiveDepthAndVendorBlock 深度优先级（任务覆盖 > mode_doc > 公司档案 > 2）
// 与厂商档案注入内容。
func TestEffectiveDepthAndVendorBlock(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	cs := d.Companies()
	companyID, err := cs.UpsertByName("深度测试公司")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM companies WHERE id=$1`, companyID)
	if err := cs.UpsertCompanyProfile(CompanyProfile{
		CompanyID: companyID, MaxDepth: 3, BannedVulnClasses: []string{"dos"},
		Domains: []string{"depth-test.example"}, TestWindow: "09:00-18:00",
	}); err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTaskWithOptions("深度测试", "depth", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	if _, err := d.Exec(`INSERT INTO task_scope(task_id,kind,company_id,source)
		VALUES ($1,'company',$2,'manual')`, task.ID, companyID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM task_scope WHERE task_id=$1`, task.ID) })

	if got := cs.EffectiveDepth(task.ID); got != 3 {
		t.Fatalf("公司档案 max_depth=3 应生效，实际 %d", got)
	}
	// program 编译进 mode_doc 的 max_depth 更严 → 覆盖公司档案。
	if _, err := d.Exec(`UPDATE tasks SET mode_doc='{"max_depth":1}'::jsonb WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if got := cs.EffectiveDepth(task.ID); got != 1 {
		t.Fatalf("mode_doc.max_depth=1 应生效，实际 %d", got)
	}
	// 任务级覆盖最优先。
	if _, err := d.Exec(`UPDATE tasks SET depth_override=2 WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if got := cs.EffectiveDepth(task.ID); got != 2 {
		t.Fatalf("depth_override=2 应生效，实际 %d", got)
	}
	block := cs.VendorContextBlock(task.ID)
	for _, want := range []string{"深度测试公司", "不收洞型", "dos", "09:00-18:00"} {
		if !strings.Contains(block, want) {
			t.Fatalf("厂商块缺少 %q：%s", want, block)
		}
	}
}

// TestRatingRulesCompanyOverride 公司规则覆盖通用规则，且 upsert 幂等。
func TestRatingRulesCompanyOverride(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	as := d.Assets()
	companyID, err := d.Companies().UpsertByName("定级规则测试公司")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM companies WHERE id=$1`, companyID)
	defer d.Exec(`DELETE FROM rating_rules WHERE vulnclass LIKE 'p3-test-%'`)

	generic, err := as.UpsertRatingRule(0, "p3-test-idor", "medium", "通用口径", "")
	if err != nil {
		t.Fatal(err)
	}
	company, err := as.UpsertRatingRule(companyID, "p3-test-idor", "high", "厂商口径", "厂商要求升一级")
	if err != nil {
		t.Fatal(err)
	}
	if generic.ID == company.ID {
		t.Fatal("公司规则应独立于通用规则")
	}
	if _, err := as.UpsertRatingRule(companyID, "p3-test-idor", "critical", "", ""); err != nil {
		t.Fatal(err)
	}
	rules, err := as.ListRatingRules(companyID)
	if err != nil {
		t.Fatal(err)
	}
	var sawCompany bool
	for _, r := range rules {
		if r.VulnClass == "p3-test-idor" && r.CompanyID == companyID {
			if r.Severity != "critical" {
				t.Fatalf("upsert 应覆盖为 critical，实际 %s", r.Severity)
			}
			sawCompany = true
		}
	}
	if !sawCompany {
		t.Fatal("按公司筛选应包含公司规则")
	}
}
