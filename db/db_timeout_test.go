package db

import (
	"net/url"
	"strings"
	"testing"
)

func TestStatementTimeoutDSN(t *testing.T) {
	t.Setenv("BLADEPRO_DB_STATEMENT_TIMEOUT_MS", "")
	got := statementTimeoutDSN("postgres://u:p@127.0.0.1:5432/app?sslmode=disable")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if opt := u.Query().Get("options"); !strings.Contains(opt, "statement_timeout=60000") {
		t.Fatalf("options=%q", opt)
	}
	if got := statementTimeoutDSN("host=127.0.0.1 dbname=x"); got != "host=127.0.0.1 dbname=x" {
		t.Fatalf("non-URL DSN changed: %q", got)
	}
	t.Setenv("BLADEPRO_DB_STATEMENT_TIMEOUT_MS", "0")
	if got := statementTimeoutDSN("postgres://u@h/db"); strings.Contains(got, "statement_timeout") {
		t.Fatalf("timeout zero should disable: %q", got)
	}
}
