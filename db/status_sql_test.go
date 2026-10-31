package db

import (
	"strings"
	"testing"
)

func TestTerminalStatusSQLIsSingleSource(t *testing.T) {
	if err := ValidateTerminalStatuses(); err != nil {
		t.Fatal(err)
	}
	sql := TerminalStatusSQL()
	for _, status := range TerminalStatuses {
		if !strings.Contains(sql, "'"+status+"'") {
			t.Fatalf("terminal SQL %q missing %q", sql, status)
		}
	}
	if strings.Contains(strings.ToLower(sql), "drop") {
		t.Fatalf("unexpected SQL fragment: %q", sql)
	}
}

func TestSetStatusStampsPolicyBlockedAndClearsOnReopen(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	task, err := d.CreateTask("terminal-status", "terminal-status", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)

	if err := d.SetStatus(task.ID, "policy_blocked"); err != nil {
		t.Fatal(err)
	}
	var completed bool
	if err := d.QueryRow(`SELECT completed_at IS NOT NULL FROM tasks WHERE id=$1`, task.ID).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("policy_blocked did not stamp completed_at")
	}
	if err := d.SetStatus(task.ID, "running"); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT completed_at IS NULL FROM tasks WHERE id=$1`, task.ID).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("reopening did not clear completed_at")
	}
}
