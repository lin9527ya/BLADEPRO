package server

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/lin9527ya/bladepro/db"
)

// TestProxyScopeGuard pins the execution-layer backstop: a task-attributed host
// outside task_scope is rejected, an in-scope host passes, and loopback stays
// reachable (platform/local range).
func TestProxyScopeGuard(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	root := fmt.Sprintf("proxy-scope-%d.example", time.Now().UnixNano())
	task, err := m.CreateTaskWithOptions("proxy-scope", "出口白名单", db.TaskCreateOptions{
		ScopeRows: []db.TaskScopeInput{{Kind: "root_domain", Domain: root}},
	})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	t.Cleanup(func() { _ = m.pg.DeleteTask(taskID) })
	s := &Server{m: m}
	if err := s.checkProxyTarget(taskID, "api."+root); err != nil {
		t.Fatalf("in-scope host rejected: %v", err)
	}
	if err := s.checkProxyTarget(taskID, "evil.example"); err == nil {
		t.Fatal("out-of-scope host must be rejected")
	}
	if err := s.checkProxyTarget(taskID, "127.0.0.1"); err != nil {
		t.Fatalf("loopback must stay reachable: %v", err)
	}
	if err := s.checkProxyTarget(0, "evil.example"); err != nil {
		t.Fatalf("unattributed traffic must pass: %v", err)
	}
}
