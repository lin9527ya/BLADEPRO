package server

import (
	"testing"

	"github.com/lin9527ya/bladepro/agent"
)

func TestRestoreTriggersPreservesOrderAndCoalesces(t *testing.T) {
	task := &Task{}
	task.pendingTriggers = []agent.TriggerEvent{{Kind: "heartbeat"}}
	task.restoreTriggers([]agent.TriggerEvent{{Kind: "done", IntentID: 1}, {Kind: "finding", IntentID: 2}})
	got := task.drainTriggers()
	if len(got) != 3 || got[0].Kind != "done" || got[1].Kind != "finding" || got[2].Kind != "heartbeat" {
		t.Fatalf("restored order=%+v", got)
	}
}
