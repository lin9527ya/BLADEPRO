package db

import (
	"context"
	"fmt"
	"testing"
)

func TestEnsureSrcLeasesAtClaim(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil {
		t.Skip("isolated PostgreSQL not configured")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("lease", "lease", TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	var asset int64
	if err := d.QueryRow(`INSERT INTO assets(type,domain) VALUES('subdomain',$1) RETURNING id`,
		fmt.Sprintf("lease-%d.example", task.ID)).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, asset) })
	ctx := context.Background()
	store := d.Src()
	busy, err := store.EnsureSrcLeasesForIntent(ctx, task.ID, 11, []int64{asset}, "sqli", "worker")
	if err != nil || len(busy) != 0 {
		t.Fatalf("first lease failed: %v %+v", err, busy)
	}
	if busy, err := store.EnsureSrcLeasesForIntent(ctx, task.ID, 12, []int64{asset}, "sqli", "worker"); err != nil || len(busy) != 1 || busy[0].OwnerIntentID != 11 {
		t.Fatalf("second intent should be busy: %v %+v", err, busy)
	}
	if busy, err := store.EnsureSrcLeasesForIntent(ctx, task.ID, 11, []int64{asset}, "sqli", "worker"); err != nil || len(busy) != 0 {
		t.Fatalf("same intent should refresh: %v %+v", err, busy)
	}
	if err := store.ReleaseSrcLeases(ctx, task.ID, 11); err != nil {
		t.Fatal(err)
	}
	if busy, err := store.EnsureSrcLeasesForIntent(ctx, task.ID, 12, []int64{asset}, "sqli", "worker"); err != nil || len(busy) != 0 {
		t.Fatalf("released lease not reusable: %v %+v", err, busy)
	}
}
