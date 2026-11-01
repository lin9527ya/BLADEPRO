package server

import (
	"context"
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/db"
)

// TestPrepareSrcSubmissionRequiresPlatformFields pins the P0 contract: an
// internally complete-looking prepare can no longer reach ready while the
// butian/generic form fields (type/CWE, asset URL) are missing.
func TestPrepareSrcSubmissionRequiresPlatformFields(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	td := t.TempDir()
	s := New(context.Background(), m, td, td, td)
	task, err := m.CreateTaskWithOptions("prepare-fields", "交付字段校验", db.TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	tid := parseIntDefault(task.ID)
	t.Cleanup(func() { _ = m.PG().DeleteTask(tid) })
	fid, err := m.PG().AddFinding(tid, 0, "authz", "订单越权读取", "low",
		"我用自己的账号读取了其他租户的订单详情。", "", "test", nil)
	if err != nil {
		t.Fatalf("add finding: %v", err)
	}
	res, err := s.prepareSrcSubmission(context.Background(), agent.SrcPrepareRequest{
		TaskID: tid, FindingID: fid, Actor: "test",
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	joined := strings.Join(res.Issues, "；")
	if !strings.Contains(joined, "提交信息缺漏洞类型") {
		t.Fatalf("missing CWE/type not reported: %s", joined)
	}
	if !strings.Contains(joined, "提交信息缺资产/URL") && !strings.Contains(joined, "资产/URL") {
		t.Fatalf("missing asset URL not reported: %s", joined)
	}
	if res.ReportReady {
		t.Fatalf("report must not be ready with missing platform fields: %+v", res)
	}
	sub, err := m.PG().Src().GetSrcSubmission(context.Background(), res.SubmissionID)
	if err != nil || sub == nil {
		t.Fatalf("ledger detail: %v %v", sub, err)
	}
	if strings.TrimSpace(sub.ReportMD) == "" {
		t.Fatal("ledger detail must retain the frozen report body")
	}
}
