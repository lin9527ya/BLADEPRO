package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestIntentQueueBackpressure(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("queue-backpressure", "队列背压", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	tools := NewToolSet(d.Exploration(task.ExplorationID), "planner")
	tools.SetTaskID(task.ID)
	tools.SetWorkerCap(1) // soft limit = 2 claimable

	q := tools.beginIntentQuota()
	if _, _, err := tools.addOneIntent(intentItem{Summary: "方向一"}, q); err != nil {
		t.Fatalf("first intent: %v", err)
	}
	q = tools.beginIntentQuota()
	if _, _, err := tools.addOneIntent(intentItem{Summary: "方向二"}, q); err != nil {
		t.Fatalf("second intent (at limit): %v", err)
	}
	if _, _, err := tools.addOneIntent(intentItem{Summary: "方向三"}, q); err == nil || !strings.Contains(err.Error(), "队列里已有") {
		t.Fatalf("saturated queue must reject regular intent, got %v", err)
	}
	if _, _, err := tools.addOneIntent(intentItem{Summary: "材料性跟进", Priority: 8}, q); err != nil {
		t.Fatalf("priority>=8 must bypass backpressure: %v", err)
	}

	// A serial-chain step with an unsatisfied dependency is not claimable yet, so
	// it must not be blocked by the queue limit.
	first, err := tools.ts.Frontier(1)
	if err != nil || len(first) == 0 {
		t.Fatalf("frontier: %v %v", first, err)
	}
	deps := []json.RawMessage{json.RawMessage(fmt.Sprintf("%d", first[0].ID))}
	if _, _, err := tools.addOneIntent(intentItem{Summary: "串行链下一步", DependsOn: deps}, q); err != nil {
		t.Fatalf("unsatisfied dependency must bypass queue cap: %v", err)
	}

	ov := tools.graphOverviewData()
	if got, _ := ov["claimable_intents"].(int); got != 3 {
		t.Fatalf("claimable_intents=%v, want 3", ov["claimable_intents"])
	}
	queue, ok := ov["intent_queue"].(map[string]any)
	if !ok {
		t.Fatalf("missing intent_queue: %#v", ov["intent_queue"])
	}
	if queue["saturated"] != true || queue["workers"] != 1 || queue["soft_limit"] != 2 {
		t.Fatalf("queue shape=%+v", queue)
	}
	if _, err := tools.ts.CountClaimableIntents(); err != nil {
		t.Fatalf("CountClaimableIntents: %v", err)
	}
}
