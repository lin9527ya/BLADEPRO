package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lin9527ya/bladepro/db"
)

// TestPurgeRetentionHonoursProgramOverrides pins both directions of the program
// retention contract:
//   - shorter than global: rows older than the program's cutoff are removed;
//   - longer than global: rows the global cutoff would delete are protected.
func TestPurgeRetentionHonoursProgramOverrides(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	ctx := context.Background()
	s := &Server{m: m}

	mkTask := func(name string, programID int64, age time.Duration) string {
		task, err := m.CreateTaskWithOptions(name, "program retention", db.TaskCreateOptions{
			Mode: "src", SrcProgramID: &programID,
		})
		if err != nil {
			t.Fatalf("create task %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID) })
		if err := m.SetTaskStatus(task.ID, "done"); err != nil {
			t.Fatalf("set done: %v", err)
		}
		if err := m.pg.InsertLLMUsage(&db.LLMUsage{
			TaskID: task.ID, Worker: "worker", InputTokens: 1, OutputTokens: 1, Status: "ok",
		}); err != nil {
			t.Fatalf("insert usage: %v", err)
		}
		if _, err := m.pg.Exec(`UPDATE llm_usage SET ts=now()-$1::interval WHERE task_id=$2`,
			fmt.Sprintf("%d seconds", int(age.Seconds())), task.ID); err != nil {
			t.Fatalf("backdate usage: %v", err)
		}
		return task.ID
	}
	mkProgram := func(name string, days int) int64 {
		p, err := m.pg.Src().CreateSrcProgram(ctx, db.SrcProgramInput{Name: name, RetentionDays: days})
		if err != nil {
			t.Fatalf("create program: %v", err)
		}
		t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM src_programs WHERE id=$1`, p.ID) })
		return p.ID
	}

	short := mkProgram(fmt.Sprintf("short-%d", time.Now().UnixNano()), 1)
	shortTask := mkTask("short-retention", short, 2*24*time.Hour)
	long := mkProgram(fmt.Sprintf("long-%d", time.Now().UnixNano()), 60)
	longTask := mkTask("long-retention", long, 10*24*time.Hour)

	spec := storageCategorySpec{Key: "db_llm_usage", DBTable: db.RetentionLLMUsage}
	// 全局 30 天：短保留程序的 2 天旧数据应被删除；长保留程序的 10 天旧数据应保留。
	if _, err := s.purgeRetentionCategory(ctx, spec, 30); err != nil {
		t.Fatalf("purge: %v", err)
	}
	count := func(taskID string) int {
		var n int
		if err := m.pg.QueryRow(`SELECT count(*) FROM llm_usage WHERE task_id=$1`, taskID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(shortTask); n != 0 {
		t.Fatalf("short-retention rows left=%d want 0 (task %s)", n, shortTask)
	}
	if n := count(longTask); n != 1 {
		t.Fatalf("long-retention rows=%d want 1 (task %s)", n, longTask)
	}
}
