package db

import "testing"

// TestLLMUsageCostAndIntentAggregation pins the cost-attribution contract: each
// metering row carries its intent id and the cost computed at call time, and the
// per-model aggregate sums those costs instead of recomputing them from today's
// profile prices (a later price edit must not rewrite history).
func TestLLMUsageCostAndIntentAggregation(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := d.EnsureLLMUsageTable(); err != nil {
		t.Fatalf("ensure llm_usage table: %v", err)
	}
	const taskID = "99002-cost-test"
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM llm_usage WHERE task_id=$1`, taskID)
		d.Close()
	})

	rows := []LLMUsage{
		{TaskID: taskID, Worker: "worker", IntentID: 7, Model: "m1", InputTokens: 100, OutputTokens: 50, CostUSD: 0.001, Status: "ok"},
		{TaskID: taskID, Worker: "worker", IntentID: 7, Model: "m1", InputTokens: 200, OutputTokens: 80, CostUSD: 0.0025, Status: "ok"},
		{TaskID: taskID, Worker: "planner", Model: "m1", InputTokens: 40, OutputTokens: 10, CostUSD: 0.0004, Status: "ok"},
	}
	for i := range rows {
		if err := d.InsertLLMUsage(&rows[i]); err != nil {
			t.Fatal(err)
		}
	}

	var intentID int64
	var cost float64
	if err := d.QueryRow(`SELECT intent_id, cost_usd FROM llm_usage WHERE task_id=$1 AND intent_id=7 ORDER BY id LIMIT 1`, taskID).Scan(&intentID, &cost); err != nil {
		t.Fatal(err)
	}
	if intentID != 7 || cost != 0.001 {
		t.Fatalf("intent/cost row = (%d,%v), want (7,0.001)", intentID, cost)
	}

	models, err := d.TokenByModel(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("models=%+v want one row", models)
	}
	if models[0].CostUSD < 0.0039-1e-12 || models[0].CostUSD > 0.0039+1e-12 {
		t.Fatalf("aggregated cost=%v want 0.0039", models[0].CostUSD)
	}
	if models[0].Calls != 3 {
		t.Fatalf("calls=%d want 3", models[0].Calls)
	}
}
