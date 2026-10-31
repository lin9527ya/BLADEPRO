package db

import (
	"fmt"
	"testing"
)

func TestDependencyReadyFrontierAndClaim(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil {
		t.Skip("isolated PostgreSQL not configured")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("dependency", "dependency", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	store := d.Exploration(task.ExplorationID)
	first, err := store.AddIntentGraph(map[string]any{"summary": "first"}, 5, nil, "planner", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddIntentGraph(map[string]any{"summary": "second", "depends_on": []int64{first}}, 5, nil, "planner", nil)
	if err != nil {
		t.Fatal(err)
	}
	frontier, err := store.Frontier(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(frontier) != 1 || frontier[0].ID != first {
		t.Fatalf("blocked intent appeared in frontier: %+v", frontier)
	}
	if ok, err := store.ClaimIntent(second, "worker", IntentLeaseTTL); err != nil || ok {
		t.Fatalf("blocked intent was claimable: %v %v", ok, err)
	}
	deps, err := store.PendingIntentDependencies(10)
	if err != nil || len(deps) != 1 || deps[0].IntentID != second || len(deps[0].DependsOn) != 1 || deps[0].DependsOn[0] != first {
		t.Fatalf("pending dependencies=%+v %v", deps, err)
	}
	if err := store.SetNodeState(first, "done"); err != nil {
		t.Fatal(err)
	}
	if deps, err := store.PendingIntentDependencies(10); err != nil || len(deps) != 0 {
		t.Fatalf("resolved dependency still pending: %+v %v", deps, err)
	}
	if ok, err := store.ClaimIntent(second, "worker", IntentLeaseTTL); err != nil || !ok {
		t.Fatalf("ready intent was not claimable: %v %v", ok, err)
	}
}

func TestIntentIdempotencyKeyAndUnblock(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil {
		t.Skip("isolated PostgreSQL not configured")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("idem", "idem", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	store := d.Exploration(task.ExplorationID)
	key := fmt.Sprintf("k-%d", task.ID)
	id, err := store.AddIntentGraph(map[string]any{"summary": "same", "idempotency_key": key}, 5, nil, "planner", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.IntentByIdempotencyKey(key); err != nil || got != id {
		t.Fatalf("idempotency lookup=%d %v", got, err)
	}
	blocked, err := store.AddIntentGraph(map[string]any{"summary": "blocked", "depends_on": []int64{id}}, 5, nil, "planner", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := store.ClearIntentDependencies(blocked); err != nil || n != 1 {
		t.Fatalf("unblock=%d %v", n, err)
	}
	if ok, err := store.ClaimIntent(blocked, "worker", IntentLeaseTTL); err != nil || !ok {
		t.Fatalf("unblocked intent not claimable: %v %v", ok, err)
	}
}
