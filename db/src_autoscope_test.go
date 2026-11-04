package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAutoScopeWhitelist(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	root := fmt.Sprintf("autoscope-%d.example", suffix)

	var companyID int64
	if err := d.QueryRow(`INSERT INTO companies(name,nkey) VALUES($1,$2) RETURNING id`,
		"自动范围测试", fmt.Sprintf("autoscope-%d", suffix)).Scan(&companyID); err != nil {
		t.Fatalf("company: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM companies WHERE id=$1`, companyID) })

	mkAsset := func(domain string, company int64) int64 {
		t.Helper()
		var c any
		if company > 0 {
			c = company
		}
		rootDomain, _ := RootDomain(domain)
		if rootDomain == "" {
			rootDomain = domain
		}
		var id int64
		if err := d.QueryRow(`INSERT INTO assets(type, domain, root_domain, company_id)
VALUES ('subdomain',$1,$2,$3) RETURNING id`, domain, rootDomain, c).Scan(&id); err != nil {
			t.Fatalf("asset %s: %v", domain, err)
		}
		t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, id) })
		return id
	}
	public := mkAsset("public."+root, 0)
	secret := mkAsset("secret."+root, 0)
	other := mkAsset(fmt.Sprintf("other-%d.example", suffix), 0)

	// 普通渗透：保持旧行为。
	normal, err := d.CreateTaskWithOptions("autoscope-normal", "普通", TaskCreateOptions{})
	if err != nil {
		t.Fatalf("normal task: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, normal.ID) })
	if ok, _, err := d.Src().AutoScopeAllowed(ctx, normal.ID, other); err != nil || !ok {
		t.Fatalf("plain task should allow auto scope: %v %v", ok, err)
	}

	// SRC 无 program：只有任务现有 scope 覆盖的资产才允许。
	srcTask, err := d.CreateTaskWithOptions("autoscope-src", "SRC", TaskCreateOptions{
		Mode: "src", ScopeRows: []TaskScopeInput{{Kind: "root_domain", Domain: root}},
	})
	if err != nil {
		t.Fatalf("src task: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, srcTask.ID) })
	if ok, why, err := d.Src().AutoScopeAllowed(ctx, srcTask.ID, public); err != nil || !ok {
		t.Fatalf("in-scope asset denied: %v %q %v", ok, why, err)
	}
	if ok, why, err := d.Src().AutoScopeAllowed(ctx, srcTask.ID, other); err != nil || ok {
		t.Fatalf("out-of-scope asset allowed: %v %q %v", ok, why, err)
	}

	// program in/out：in 根域放行、out 子域拒绝、范围外拒绝。
	program, err := d.Src().CreateSrcProgram(ctx, SrcProgramInput{
		Name: fmt.Sprintf("autoscope-program-%d", suffix), Platform: "test", CompanyID: &companyID,
	})
	if err != nil {
		t.Fatalf("program: %v", err)
	}
	t.Cleanup(func() { _ = d.Src().DeleteSrcProgram(context.Background(), program.ID) })
	if _, err := d.Src().ReplaceProgramScope(ctx, program.ID, []ProgramScopeInput{
		{Direction: "in", Kind: "root_domain", Value: root},
		{Direction: "out", Kind: "subdomain", Value: "secret." + root},
	}); err != nil {
		t.Fatalf("program scope: %v", err)
	}
	progTask, err := d.CreateTaskWithOptions("autoscope-prog", "SRC program", TaskCreateOptions{
		Mode: "src", SrcProgramID: &program.ID,
	})
	if err != nil {
		t.Fatalf("program task: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, progTask.ID) })
	if ok, why, err := d.Src().AutoScopeAllowed(ctx, progTask.ID, public); err != nil || !ok {
		t.Fatalf("program in-scope denied: %v %q %v", ok, why, err)
	}
	if ok, why, err := d.Src().AutoScopeAllowed(ctx, progTask.ID, secret); err != nil || ok {
		t.Fatalf("program out-of-scope allowed: %v %q %v", ok, why, err)
	}
	if ok, why, err := d.Src().AutoScopeAllowed(ctx, progTask.ID, other); err != nil || ok {
		t.Fatalf("outside program allowed: %v %q %v", ok, why, err)
	}

	// 企业归属不是授权，不能越过显式 in。
	companyAsset := mkAsset(fmt.Sprintf("company-out-%d.example", suffix), companyID)
	if ok, why, err := d.Src().AutoScopeAllowed(ctx, progTask.ID, companyAsset); err != nil || ok {
		t.Fatalf("out-of-scope company asset allowed: %v %q %v", ok, why, err)
	}
}

// APP/开放式 SRC 任务没有 program/企业边界：agent 在任务内登记、且有证据节点或意图
// 锚定的资产应能自动入范围；只存在于全局库、没有任务内归属证据的资产仍然拒绝。
func TestAutoScopeEvidenceExpansion(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatalf("postgres unavailable (%v)", err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	task, err := d.CreateTaskWithOptions("app-autoscope", "APP 资产", TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	domain := fmt.Sprintf("app-asset-%d.example", suffix)
	assetID, err := d.Assets().UpsertSubdomain(UpsertSubdomainReq{Domain: domain, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE domain=$1`, domain) })

	// 还没有任务内归属证据：拒绝。
	if ok, why, err := d.Src().AutoScopeAllowed(ctx, task.ID, assetID); err != nil || ok {
		t.Fatalf("无归属证据不应自动扩权: ok=%v why=%q err=%v", ok, why, err)
	}

	// planner 通过 insert_assets 登记时带任务起点事实作为证据节点。
	origin, err := d.Exploration(task.ExplorationID).OriginFactID()
	if err != nil || origin <= 0 {
		t.Fatalf("origin fact: id=%d err=%v", origin, err)
	}
	if err := d.Assets().SetTaskAssetSource(task.ID, assetID, "agent", "insert_assets", &origin); err != nil {
		t.Fatalf("set source: %v", err)
	}
	if ok, why, err := d.Src().AutoScopeAllowed(ctx, task.ID, assetID); err != nil || !ok {
		t.Fatalf("有任务内证据应允许扩权: ok=%v why=%q err=%v", ok, why, err)
	}
	if err := d.Assets().AddAutoScope(task.ID, "subdomain", domain, "", ""); err != nil {
		t.Fatalf("add auto scope: %v", err)
	}
	if ok, decidable, err := d.Src().SrcHostInScope(ctx, task.ID, domain); err != nil || !decidable || !ok {
		t.Fatalf("自动入范围后应放行: ok=%v decidable=%v err=%v", ok, decidable, err)
	}

	// 全局库里有、但与本任务没有任何关系的资产仍不自动入范围。
	otherDomain := fmt.Sprintf("unrelated-%d.example", suffix)
	otherID, err := d.Assets().UpsertSubdomain(UpsertSubdomainReq{Domain: otherDomain})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE domain=$1`, otherDomain) })
	if ok, why, err := d.Src().AutoScopeAllowed(ctx, task.ID, otherID); err != nil || ok {
		t.Fatalf("无任务关联的资产不应自动扩权: ok=%v why=%q err=%v", ok, why, err)
	}
}

// add_task_scope 的 agent 路径：没有 program 边界时，任务内已有证据的资产可以被
// agent 显式纳入范围；无归属证据的域名仍然拒绝。
func TestAgentScopeEvidenceExpansion(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatalf("postgres unavailable (%v)", err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	task, err := d.CreateTaskWithOptions("agent-expand", "APP 资产", TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	domain := fmt.Sprintf("agent-expand-%d.example", suffix)
	assetID, err := d.Assets().UpsertSubdomain(UpsertSubdomainReq{Domain: domain, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE domain=$1`, domain) })
	origin, err := d.Exploration(task.ExplorationID).OriginFactID()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Assets().SetTaskAssetSource(task.ID, assetID, "agent", "insert_assets", &origin); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Assets().AddAgentScope(task.ID, "subdomain", domain, "APP 调用到的所属资产", "agent"); err != nil {
		t.Fatalf("有任务内证据时 agent 应能扩范围: %v", err)
	}
	if ok, decidable, err := d.Src().SrcHostInScope(ctx, task.ID, domain); err != nil || !decidable || !ok {
		t.Fatalf("agent 扩范围后应放行: ok=%v decidable=%v err=%v", ok, decidable, err)
	}
	if _, err := d.Assets().AddAgentScope(task.ID, "subdomain", fmt.Sprintf("evil-%d.example", suffix), "无依据", "agent"); err == nil {
		t.Fatal("无归属证据的 agent 扩范围仍应拒绝")
	}
}

// 只写了 scope_note、没有任何 in 范围的 SRC program（常见于 APP/品牌任务）：
// agent 可以用任务里的 keyword 做首次引导，补充精确根域；program out 仍然优先。
func TestAgentScopeBootstrapsOpenProgram(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatalf("postgres unavailable (%v)", err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	prog, err := d.Src().CreateSrcProgram(ctx, SrcProgramInput{
		Name: fmt.Sprintf("open-program-%d", suffix), Platform: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Src().DeleteSrcProgram(context.Background(), prog.ID) })
	if _, err := d.Src().ReplaceProgramScope(ctx, prog.ID, []ProgramScopeInput{
		{Direction: "out", Kind: "subdomain", Value: "forbidden.open-app.example"},
	}); err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTaskWithOptions("open-app", "APP", TaskCreateOptions{
		Mode: "src", SrcProgramID: &prog.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	if _, err := d.Assets().AddAgentScope(task.ID, "keyword", "番茄测试", "APP 关键词", "manual"); err != nil {
		t.Fatalf("add keyword scope: %v", err)
	}

	root := "open-app.example"
	if _, err := d.Assets().AddAgentScope(task.ID, "root_domain", root, "APP 调用的所属资产", "agent"); err != nil {
		t.Fatalf("开放式 program 应允许 agent 用 keyword 引导补根域: %v", err)
	}
	if assets, err := d.Src().InScopeSrcAssets(ctx, task.ID); err != nil || len(assets) == 0 {
		t.Fatalf("补根域后应落成可测资产: assets=%+v err=%v", assets, err)
	}
	if ok, decidable, err := d.Src().SrcHostInScope(ctx, task.ID, "api."+root); err != nil || !decidable || !ok {
		t.Fatalf("补根域后应放行子域: ok=%v decidable=%v err=%v", ok, decidable, err)
	}
	if ok, _, err := d.Src().SrcHostInScope(ctx, task.ID, "forbidden.open-app.example"); err != nil || ok {
		t.Fatalf("program out 排除仍应生效: ok=%v err=%v", ok, err)
	}
	if _, err := d.Assets().AddAgentScope(task.ID, "company", "不存在的公司", "", "agent"); err == nil {
		t.Fatal("company 类扩权不应走关键词引导")
	}
}
