package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestAbandonGoalAndStrategyExhausted(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("strategy-exhausted", "strategy-exhausted", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	ts := d.Exploration(task.ExplorationID)
	tools := NewToolSet(ts, "planner")
	tools.SetTaskID(task.ID)
	goal, err := ts.AddNode(db.KindGoal, map[string]any{"summary": "unreachable goal"}, 1, "open", "human", nil)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := ts.AddNode(db.KindFact, map[string]any{"summary": "authorization exhausted evidence"}, 1, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"goal_id": goal, "reason": "目标不在授权范围内"})
	if res, err := tools.abandonGoal().Call(context.Background(), raw, nil); err != nil || !strings.Contains(res.Flatten(), "abandoned") {
		t.Fatalf("abandon_goal: %v / %s", err, res.Flatten())
	}
	node, err := ts.GetNode(goal)
	if err != nil || node == nil || node.State != "abandoned" {
		t.Fatalf("goal state=%+v err=%v", node, err)
	}
	if !strings.Contains(string(node.Payload), "目标不在授权范围内") {
		t.Fatalf("abandon reason missing: %s", node.Payload)
	}
	raw, _ = json.Marshal(map[string]any{"reason": "策略内没有可行方向", "evidence_ids": []int64{fact}})
	if res, err := tools.markStrategyExhausted().Call(context.Background(), raw, nil); err != nil || !strings.Contains(res.Flatten(), "policy_blocked") {
		t.Fatalf("mark_strategy_exhausted: %v / %s", err, res.Flatten())
	}
	if tools.strategyExhausted != "策略内没有可行方向" {
		t.Fatalf("strategy flag=%q", tools.strategyExhausted)
	}
	// Missing evidence must be rejected.
	raw, _ = json.Marshal(map[string]any{"reason": "no evidence"})
	if res, err := tools.markStrategyExhausted().Call(context.Background(), raw, nil); err != nil || !strings.Contains(res.Flatten(), "必填") {
		t.Fatalf("empty evidence accepted: %v / %s", err, res.Flatten())
	}
}
