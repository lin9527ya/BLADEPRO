package db

import "testing"

func TestNoOutputIsTerminalAndReopenable(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil {
		t.Skip("isolated PostgreSQL not configured")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("no-output", "no-output", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	store := d.Exploration(task.ExplorationID)
	id, err := store.AddIntentGraph(map[string]any{"summary": "empty"}, 5, nil, "planner", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ClaimIntent(id, "worker", IntentLeaseTTL); err != nil || !ok {
		t.Fatal(err)
	}
	if ok, err := store.CompareAndSetIntentState(id, "running", "no_output"); err != nil || !ok {
		t.Fatalf("no_output transition: %v %v", ok, err)
	}
	n, err := store.GetNode(id)
	if err != nil || n == nil || n.State != "no_output" {
		t.Fatalf("state=%+v %v", n, err)
	}
	if ok, err := store.ReopenIntent(id); err != nil || !ok {
		t.Fatalf("reopen no_output: %v %v", ok, err)
	}
}
