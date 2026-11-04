package db

import (
	"context"
	"testing"
)

func TestSrcAuthorizationRefinementDoesNotWiden(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil {
		t.Skip("isolated PostgreSQL not configured")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("scope", "scope", TaskCreateOptions{Mode: "src", ScopeRows: []TaskScopeInput{{Kind: "subdomain", Domain: "api.authz-test.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	if err := d.SealSrcAuthorization(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	as := d.Assets()
	if _, err := as.AddAgentScope(task.ID, "subdomain", "api.authz-test.example", "refine", "agent"); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"evil.example", "child.api.authz-test.example"} {
		if _, err := as.AddAgentScope(task.ID, "subdomain", host, "widen", "agent"); err == nil {
			t.Fatalf("widen accepted: %s", host)
		}
	}
	if _, err := as.AddAgentScope(task.ID, "root_domain", "authz-test.example", "widen", "agent"); err == nil {
		t.Fatal("root expansion accepted")
	}
	// Even a later operator scope row cannot silently rewrite the sealed envelope.
	if _, err := d.Exec(`INSERT INTO task_scope(task_id,kind,domain,source) VALUES($1,'root_domain','example','manual')`, task.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := d.Src().SrcHostInScope(context.Background(), task.ID, "evil.example"); err != nil || ok {
		t.Fatalf("authorization drift: %v %v", ok, err)
	}
}

func TestSrcAuthorizationIntersectsExplicitProgramScope(t *testing.T) {
	a := SrcAuthorization{Allowed: []TaskScope{{Kind: "root_domain", Domain: "example.com"}}, ProgramAllowed: []TaskScope{{Kind: "subdomain", Domain: "api.example.com"}}, Excluded: []TaskScope{{Kind: "subdomain", Domain: "admin.example.com"}}}
	if !a.AllowsHost("api.example.com") {
		t.Fatal("explicit target rejected")
	}
	for _, host := range []string{"admin.example.com", "other.example.com", "api.example.com.evil.com"} {
		if a.AllowsHost(host) {
			t.Fatal("explicit program boundary widened")
		}
	}
}

// SRC 任务创建时没解析出目标 → 授权快照为空；此时人工在任务详情补范围必须能成功，
// 并触发重新冻结。此前人工补范围被当成 "Agent 扩大授权" 直接 400。
func TestSrcManualScopeReauthorizes(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	task, err := d.CreateTaskWithOptions("manual-scope", "scope", TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	if err := d.SealSrcAuthorization(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	host := "api5.novelfm-test.example"
	if ok, _, err := d.Src().SrcHostInScope(context.Background(), task.ID, host); err != nil || ok {
		t.Fatalf("空授权快照不应放行: ok=%v err=%v", ok, err)
	}
	if _, err := d.Assets().AddAgentScope(task.ID, "root_domain", "novelfm-test.example", "人工补充授权", "manual"); err != nil {
		t.Fatalf("人工补范围应成功: %v", err)
	}
	rows, err := d.Assets().ListTaskScope(task.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("读取范围失败: rows=%+v err=%v", rows, err)
	}
	scope := rows[0]
	if ok, _, err := d.Src().SrcHostInScope(context.Background(), task.ID, host); err != nil || !ok {
		t.Fatalf("重新授权后应放行: ok=%v err=%v", ok, err)
	}
	if _, err := d.Assets().AddAgentScope(task.ID, "subdomain", "evil.example", "越权", "agent"); err == nil {
		t.Fatal("agent 仍不能扩大授权范围")
	}
	// 人工删除范围必须收紧授权：旧快照不能继续放行已删掉的 host。
	if deleted, err := d.Assets().DeleteTaskScope(task.ID, scope.ID); err != nil || !deleted {
		t.Fatalf("删除范围失败: deleted=%v err=%v", deleted, err)
	}
	if ok, _, err := d.Src().SrcHostInScope(context.Background(), task.ID, host); err != nil || ok {
		t.Fatalf("删除范围后不应再放行: ok=%v err=%v", ok, err)
	}
}
