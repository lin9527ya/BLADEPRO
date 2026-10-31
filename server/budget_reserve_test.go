package server

import (
	"encoding/json"
	"testing"
)

func TestSrcDeliveryReserveReducesExplorationBudget(t *testing.T) {
	doc, _ := json.Marshal(map[string]any{"mode": "src", "delivery_reserve_pct": 20})
	e := &Engine{}
	task := &Task{Mode: "src", TokenBudget: 1000, ModeDoc: doc}
	if got := e.tokenBudgetFor(task); got != 800 {
		t.Fatalf("reserved budget=%d want 800", got)
	}
	task.ModeDoc = nil
	if got := e.tokenBudgetFor(task); got != 1000 {
		t.Fatalf("plain budget=%d want 1000", got)
	}
}
