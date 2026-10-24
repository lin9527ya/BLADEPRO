package db

import "testing"

// TestTaskPriorityRoundTrip pins the storage/update contract for queue priority.
func TestTaskPriorityRoundTrip(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	task, err := d.CreateTaskWithOptions("priority", "优先级", TaskCreateOptions{Priority: 7})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID) })
	if task.Priority != 7 {
		t.Fatalf("created priority=%d want 7", task.Priority)
	}
	got, err := d.GetTask(task.ID)
	if err != nil || got == nil || got.Priority != 7 {
		t.Fatalf("get=%+v err=%v", got, err)
	}
	p := -3
	updated, err := d.UpdateTask(task.ID, TaskPatch{Priority: &p})
	if err != nil || updated == nil || updated.Priority != -3 {
		t.Fatalf("patch=%+v err=%v", updated, err)
	}
}
