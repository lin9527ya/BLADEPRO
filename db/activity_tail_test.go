package db

import (
	"testing"
)

func TestActivityTailReturnsNewestWindow(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil {
		t.Skip("isolated PostgreSQL not configured")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("tail", "tail", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	store := d.Exploration(task.ExplorationID)
	intent, err := store.AddIntentGraph(map[string]any{"summary": "tail"}, 5, nil, "planner", nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		nid := intent
		if _, err := store.AppendActivity(Activity{NodeID: &nid, Worker: "worker", Kind: "text", Summary: "row-" + string(rune('a'+i%26))}); err != nil {
			t.Fatal(err)
		}
	}
	acts, err := store.ActivityTail(intent, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 10 {
		t.Fatalf("tail size=%d", len(acts))
	}
	for i := 1; i < len(acts); i++ {
		if acts[i-1].ID >= acts[i].ID {
			t.Fatalf("tail not ascending: %d then %d", acts[i-1].ID, acts[i].ID)
		}
	}
	if acts[len(acts)-1].Summary != "row-"+string(rune('a'+39%26)) {
		t.Fatalf("tail missed newest row: %q", acts[len(acts)-1].Summary)
	}
}
