package db

import (
	"context"
	"testing"
)

// TestSrcDuplicateGroups pins the cluster query: same strong fingerprint groups,
// degraded fingerprints group only when no strong one exists, and singletons are
// omitted.
func TestSrcDuplicateGroups(t *testing.T) {
	d, _, _ := testSetup(t)
	defer d.Close()
	ctx := context.Background()
	task, err := d.CreateTaskWithOptions("dup-groups", "重复组", TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	var taskID int64
	if err := d.QueryRow(`SELECT id FROM tasks WHERE exploration_id=$1`, task.ExplorationID).Scan(&taskID); err != nil {
		t.Fatalf("task id: %v", err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(taskID) })

	add := func(name, fp, dfp string) {
		var id int64
		if err := d.QueryRow(`INSERT INTO findings(task_id, vulnclass, name, severity, status)
VALUES ($1,'IDOR',$2,'high','confirmed') RETURNING id`, taskID, name).Scan(&id); err != nil {
			t.Fatalf("finding: %v", err)
		}
		if _, err := d.Exec(`INSERT INTO src_finding_meta(finding_id, task_id, fingerprint, degraded_fingerprint)
VALUES ($1,$2,$3,$4)`, id, taskID, fp, dfp); err != nil {
			t.Fatalf("meta: %v", err)
		}
	}
	add("强指纹甲", "strong-a", "degraded-a")
	add("强指纹乙", "strong-a", "degraded-b")
	add("降级甲", "", "degraded-c")
	add("降级乙", "", "degraded-c")
	add("单条", "strong-single", "")

	groups, err := d.Src().SrcDuplicateGroups(ctx, taskID, 20, 10)
	if err != nil {
		t.Fatalf("groups: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups=%d want 2: %+v", len(groups), groups)
	}
	byFP := map[string]SrcDuplicateGroup{}
	for _, g := range groups {
		byFP[g.Fingerprint] = g
	}
	if g := byFP["strong-a"]; !g.Strong || g.Count != 2 {
		t.Fatalf("strong group=%+v", g)
	}
	if g := byFP["degraded-c"]; g.Strong || g.Count != 2 {
		t.Fatalf("degraded group=%+v", g)
	}
	if _, ok := byFP["strong-single"]; ok {
		t.Fatal("singleton group must be omitted")
	}
}
