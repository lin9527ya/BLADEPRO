package server

import (
	"context"
	"testing"
)

// TestSrcRequireIsolationGate pins fail-closed behavior: when the operator
// requires SRC tasks to run sandboxed, an instance without a working
// BLADEPRO_AGENT_USER must refuse to create the task instead of pretending.
func TestSrcRequireIsolationGate(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	s := &Server{m: m}
	ctx := context.Background()
	if err := m.pg.SetBool(settingSrcRequireIso, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.pg.SetBool(settingSrcRequireIso, false) })

	_, perr := s.prepareTaskCreate(ctx, createTaskReq{Mode: "src", Description: "isolation", Goal: "g"})
	if perr == nil {
		t.Fatal("SRC task should be rejected without agent isolation")
	}
	if perr.code != 400 {
		t.Fatalf("code=%d msg=%s", perr.code, perr.msg)
	}
	// 非 SRC 任务不受这条要求影响。
	if _, perr := s.prepareTaskCreate(ctx, createTaskReq{Description: "plain", Goal: "g"}); perr != nil {
		t.Fatalf("plain task should not be gated: %v", perr)
	}
	// 显式在退出前复位：t.Cleanup 可能晚于 defer m.Close()，那时写设置会静默失败。
	if err := m.pg.SetBool(settingSrcRequireIso, false); err != nil {
		t.Fatalf("reset isolation setting: %v", err)
	}
}
