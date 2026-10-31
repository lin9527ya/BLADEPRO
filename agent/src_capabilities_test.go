package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

// The host has always injected SrcIdentityDiffer into Worker/Planner fields, but the
// per-run ToolSet never received it. This pins the whole capability set at the
// ToolSet boundary so a future capability cannot be half-wired in only one loop.
func TestSetSrcHostCapabilitiesWiresIdentityDiffer(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("src-capabilities", "能力接线", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	tools := NewToolSet(d.Exploration(task.ExplorationID), "work#1")
	tools.SetTaskID(task.ID)
	tools.SetSrcMode(srcmode.DefaultDoc(), d.Src())
	called := false
	tools.SetSrcHostCapabilities(nil, nil, nil, nil, func(_ context.Context, req SrcIdentityRequest) (*SrcIdentityResult, error) {
		called = true
		if req.TaskID != task.ID || req.AssetID != 3 || req.Path != "/api/users/1" {
			t.Fatalf("unexpected request: %+v", req)
		}
		return &SrcIdentityResult{AssetID: req.AssetID, URL: "https://example.test/api/users/1", Distinct: true}, nil
	})
	raw, _ := json.Marshal(map[string]any{
		"asset_id": 3, "path": "/api/users/1", "identity_a": "A", "identity_b": "B",
	})
	res, err := tools.srcIdentityDiffTool().Call(context.Background(), raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatalf("identity differ was not called through the ToolSet; ready=%v store=%v mode=%v result=%s", tools.srcReady(), tools.srcStore != nil, tools.srcMode, res.Flatten())
	}
	if !strings.Contains(res.Flatten(), `"distinct":true`) {
		t.Fatalf("unexpected result: %s", res.Flatten())
	}
}
