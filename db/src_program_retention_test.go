package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestProgramRetentionMaps pins the two lookups the cleanup sweeper relies on:
// task→program retention overrides and program→tasks grouping.
func TestProgramRetentionMaps(t *testing.T) {
	d, _, _ := testSetup(t)
	defer d.Close()
	ctx := context.Background()

	program, err := d.Src().CreateSrcProgram(ctx, SrcProgramInput{
		Name: fmt.Sprintf("retention-%d", time.Now().UnixNano()), RetentionDays: 3,
	})
	if err != nil {
		t.Fatalf("create program: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM src_programs WHERE id=$1`, program.ID) })

	task, err := d.CreateTaskWithOptions("retention-task", "保留策略", TaskCreateOptions{
		Mode: "src", SrcProgramID: &program.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	byTask, err := d.TaskProgramRetention(ctx)
	if err != nil {
		t.Fatalf("task retention map: %v", err)
	}
	if got := byTask[fmt.Sprint(task.ID)]; got != 3 {
		t.Fatalf("task retention=%d want 3", got)
	}
	groups, err := d.ProgramRetentionGroups(ctx)
	if err != nil {
		t.Fatalf("groups: %v", err)
	}
	var found bool
	for _, g := range groups {
		if g.ProgramID != program.ID {
			continue
		}
		found = g.Days == 3 && len(g.TaskIDs) == 1 && g.TaskIDs[0] == fmt.Sprint(task.ID)
	}
	if !found {
		t.Fatalf("program group missing or wrong: %+v", groups)
	}

	// retention_days=0 的 program 不出现在覆盖表里（跟随全局）。
	program.RetentionDays = 0
	if _, err := d.Src().UpdateSrcProgram(ctx, program.ID, SrcProgramInput{Name: program.Name, RetentionDays: 0}); err != nil {
		t.Fatalf("clear retention: %v", err)
	}
	byTask, _ = d.TaskProgramRetention(ctx)
	if _, ok := byTask[fmt.Sprint(task.ID)]; ok {
		t.Fatal("retention=0 must not produce an override")
	}
}
