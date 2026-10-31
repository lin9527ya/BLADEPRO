package server

import (
	"context"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestSubmissionPackageExcludesUnreadyFindings(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	task, err := m.CreateTaskWithOptions("batch", "batch", db.TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatal(err)
	}
	defer m.pg.DeleteTask(parseIntDefault(task.ID))
	if _, err := m.pg.AddFinding(parseIntDefault(task.ID), 0, "xss", "candidate", "medium", "summary", "proof", "test", nil); err != nil {
		t.Fatal(err)
	}
	s := &Server{m: m}
	audit, err := s.buildSrcBatch(context.Background(), task, false)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Included != 1 || audit.SubmissionOnly {
		t.Fatalf("audit package should include the finding: %+v", audit)
	}
	submission, err := s.buildSrcBatch(context.Background(), task, true)
	if err != nil {
		t.Fatal(err)
	}
	if !submission.SubmissionOnly || submission.Included != 0 || len(submission.Entries) != 1 || submission.Entries[0].Included || submission.Entries[0].SkipReason == "" {
		t.Fatalf("submission package accepted an unready finding: %+v", submission)
	}
}
