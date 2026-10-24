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

	// program 企业白名单：企业资产即使不在显式 in 也放行。
	companyAsset := mkAsset("company."+root, companyID)
	if ok, why, err := d.Src().AutoScopeAllowed(ctx, progTask.ID, companyAsset); err != nil || !ok {
		t.Fatalf("program company asset denied: %v %q %v", ok, why, err)
	}
}
