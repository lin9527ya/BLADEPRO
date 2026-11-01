package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestUpdateTaskSoftBriefRevisionCAS pins the runtime-edit contract: only
// mode_doc.soft_brief changes, the expected revision must match the row, and the
// new revision must be exactly one greater. Hard keys survive untouched.
func TestUpdateTaskSoftBriefRevisionCAS(t *testing.T) {
	d, _, _ := testSetup(t)
	defer d.Close()
	ctx := context.Background()

	initial, err := json.Marshal(map[string]any{
		"mode":           "src",
		"required_areas": map[string][]string{"subdomain": {"takeover", "host"}},
		"program_brief":  map[string]any{"exclude_areas": []string{"xss"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTaskWithOptions(fmt.Sprintf("soft-brief-%d", time.Now().UnixNano()), "soft brief CAS", TaskCreateOptions{
		Mode: "src", ModeDoc: initial,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	first := json.RawMessage(`{"focus_areas":["logic"],"exclude_areas":["paths"],"revision":1}`)
	if _, err := d.UpdateTaskSoftBrief(ctx, task.ID, 0, first); err != nil {
		t.Fatalf("first update: %v", err)
	}
	// Replaying the same request must fail closed instead of last-writer-wins.
	if _, err := d.UpdateTaskSoftBrief(ctx, task.ID, 0, json.RawMessage(`{"focus_areas":["ssrf"],"revision":1}`)); !errors.Is(err, ErrSoftBriefRevision) {
		t.Fatalf("stale update err=%v, want ErrSoftBriefRevision", err)
	}
	// The new revision must advance by exactly one.
	if _, err := d.UpdateTaskSoftBrief(ctx, task.ID, 1, json.RawMessage(`{"focus_areas":["ssrf"],"revision":3}`)); err == nil {
		t.Fatal("non-consecutive revision accepted")
	}

	var raw []byte
	if err := d.QueryRow(`SELECT mode_doc FROM tasks WHERE id=$1`, task.ID).Scan(&raw); err != nil {
		t.Fatalf("read mode_doc: %v", err)
	}
	var doc struct {
		Mode          string              `json:"mode"`
		RequiredAreas map[string][]string `json:"required_areas"`
		ProgramBrief  json.RawMessage     `json:"program_brief"`
		SoftBrief     struct {
			FocusAreas   []string `json:"focus_areas"`
			ExcludeAreas []string `json:"exclude_areas"`
			Revision     int      `json:"revision"`
		} `json:"soft_brief"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse mode_doc: %v", err)
	}
	if doc.Mode != "src" || len(doc.RequiredAreas["subdomain"]) != 2 || doc.ProgramBrief == nil {
		t.Fatalf("hard fields changed: %+v", doc)
	}
	if doc.SoftBrief.Revision != 1 || len(doc.SoftBrief.FocusAreas) != 1 || doc.SoftBrief.FocusAreas[0] != "logic" {
		t.Fatalf("soft brief not stored: %+v", doc.SoftBrief)
	}

	// A non-SRC task is rejected without touching its mode_doc.
	normal, err := d.CreateTaskWithOptions(fmt.Sprintf("not-src-%d", time.Now().UnixNano()), "not src", TaskCreateOptions{})
	if err != nil {
		t.Fatalf("create normal task: %v", err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(normal.ID) })
	if _, err := d.UpdateTaskSoftBrief(ctx, normal.ID, 0, json.RawMessage(`{"revision":1}`)); err == nil {
		t.Fatal("soft brief update accepted for non-SRC task")
	}
}
