package db

import "testing"

func TestConsumeHintIsIdempotent(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil {
		t.Skip("isolated PostgreSQL not configured")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("hint", "hint", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	store := d.Exploration(task.ExplorationID)
	id, err := store.AddNode(KindHint, map[string]any{"text": "adopt me"}, 0, "active", "human", nil)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.ConsumeHint(id)
	if err != nil || !ok {
		t.Fatalf("consume=%v %v", ok, err)
	}
	if ok, err := store.ConsumeHint(id); err != nil || ok {
		t.Fatalf("second consume should be a no-op: %v %v", ok, err)
	}
	n, err := store.GetNode(id)
	if err != nil || n == nil || n.State != "consumed" {
		t.Fatalf("hint state=%+v %v", n, err)
	}
}
