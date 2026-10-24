package server

import "testing"

// TestSortQueuedTasksPriority pins the admission order: priority DESC first, then
// FIFO by queued_at, then task id. Priority must not disturb FIFO within a tier.
func TestSortQueuedTasksPriority(t *testing.T) {
	mk := func(id string, priority int, queuedAt int64) queuedTaskEntry {
		return queuedTaskEntry{
			task:      &Task{ID: id, Priority: priority},
			lifecycle: taskLifecycleState{QueuedAt: queuedAt},
		}
	}
	queued := []queuedTaskEntry{
		mk("1", 0, 100),
		mk("2", 10, 200),
		mk("3", 10, 100),
		mk("4", -5, 50),
		mk("5", 0, 50),
	}
	sortQueuedTasks(queued)
	got := make([]string, 0, len(queued))
	for _, q := range queued {
		got = append(got, q.task.ID)
	}
	want := []string{"3", "2", "5", "1", "4"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order=%v want %v", got, want)
		}
	}
}
