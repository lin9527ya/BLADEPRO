package agent

import (
	"strconv"
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestRememberRoundKeepsOnlyNewConclusions(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("planner-round", "轮次摘要", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	ts := d.Exploration(task.ExplorationID)

	oldID, err := ts.AddIntent(map[string]any{"summary": "上一轮已经派过的方向"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	prev := latestIntentID(ts)
	if prev != oldID {
		t.Fatalf("latestIntentID=%d, want %d", prev, oldID)
	}
	newID, err := ts.AddIntent(map[string]any{"summary": "本轮新派的方向"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}

	p := NewPlanner(nil, "m", "", nil, 0, 0)
	p.rememberRound(ts, prev, "本轮结论：发现一个上传点")
	note := p.lastRoundNote
	if !strings.Contains(note, "本轮结论：发现一个上传点") {
		t.Fatalf("note missing last conclusion: %q", note)
	}
	if !strings.Contains(note, "#"+strconv.FormatInt(newID, 10)) {
		t.Fatalf("note missing new intent #%d: %q", newID, note)
	}
	if strings.Contains(note, "#"+strconv.FormatInt(oldID, 10)) {
		t.Fatalf("note must not repeat pre-round intents: %q", note)
	}
	if strings.Contains(note, "上一轮已经派过的方向") {
		t.Fatalf("note must not include old intent summary: %q", note)
	}
}

func TestRememberRoundEmpty(t *testing.T) {
	p := NewPlanner(nil, "m", "", nil, 0, 0)
	p.rememberRound(nil, 0, "")
	if p.lastRoundNote != "" {
		t.Fatalf("nil store must not invent a round note: %q", p.lastRoundNote)
	}
}

func TestRenderTriggersDistinguishesTerminalStates(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("trigger-state", "trigger-state", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	ts := d.Exploration(task.ExplorationID)
	id, err := ts.AddIntent(map[string]any{"summary": "trigger intent"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	noOutput := renderTriggers(ts, []TriggerEvent{{Kind: "done", IntentID: id, State: "no_output"}})
	if !strings.Contains(noOutput, "no_output") || !strings.Contains(noOutput, "不是已测完") {
		t.Fatalf("no_output trigger misleading: %s", noOutput)
	}
	blocked := renderTriggers(ts, []TriggerEvent{{Kind: "done", IntentID: id, State: "blocked"}})
	if !strings.Contains(blocked, "blocked") || !strings.Contains(blocked, "需重派") {
		t.Fatalf("blocked trigger misleading: %s", blocked)
	}
	requeued := renderTriggers(ts, []TriggerEvent{{Kind: "done", IntentID: id, Requeued: true}})
	if !strings.Contains(requeued, "退回待领") {
		t.Fatalf("requeued trigger misleading: %s", requeued)
	}
}
