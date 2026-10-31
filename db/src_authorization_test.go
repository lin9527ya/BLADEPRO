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
