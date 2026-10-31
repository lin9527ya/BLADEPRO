package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lin9527ya/Norma/llm"
	"github.com/lin9527ya/bladepro/db"
)

func TestWorkerSlotIdentity(t *testing.T) {
	if got := workerSlotNumber("work#3"); got != 3 {
		t.Fatalf("work#3 slot=%d", got)
	}
	for _, name := range []string{"chat", "work#", "work#x", "work#0"} {
		if got := workerSlotNumber(name); got != 0 {
			t.Fatalf("%q slot=%d, want 0", name, got)
		}
	}
	if got := WorkerSessionIDForSlot(7, 2); got != "exp7-worker-slot2" {
		t.Fatalf("slot session=%q", got)
	}
	if got := WorkerSessionID(7, 9); got != "exp7-worker-i9" {
		t.Fatalf("legacy session=%q", got)
	}
}

func TestWorkerIntentTurnIsCacheBoundary(t *testing.T) {
	intent := &db.Node{ID: 42, Payload: json.RawMessage(`{"summary":"测试上传点"}`)}
	turn := renderWorkerIntentTurn(intent, "\n\n资产 A", "/data/tasks/1/i42", 2)
	for _, want := range []string{workerIntentMarker(42), "测试上传点", "历史对话", "/data/tasks/1/i42", "Worker #2", "意图 id：42"} {
		if !strings.Contains(turn, want) {
			t.Fatalf("intent turn missing %q:\n%s", want, turn)
		}
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.TextBlock(workerIntentMarker(1) + "\nold intent")}},
		llm.UserText("overview\n\n" + turn),
	}
	if !hasWorkerIntentMessage(messages, 42) {
		t.Fatal("marker 42 should be found anywhere in the launch turn")
	}
	if hasWorkerIntentMessage(messages, 7) {
		t.Fatal("marker 7 must not match intent 42")
	}
	if got := messagesSinceWorkerIntent(messages); len(got) != 1 || !strings.Contains(got[0].Text(), workerIntentMarker(42)) {
		t.Fatalf("approval history should start at the latest intent, got %d messages", len(got))
	}
}

func TestWorkerStaticSystemHasNoIntentData(t *testing.T) {
	sys := workerStaticSystem("127.0.0.1:8080", "", "/data", "work#2")
	for _, forbidden := range []string{"intent id", "worker-slot", "/tasks/1/i"} {
		if strings.Contains(strings.ToLower(sys), strings.ToLower(forbidden)) {
			t.Fatalf("static worker system must not contain per-intent %q", forbidden)
		}
	}
}

func TestLastWorkerIntentIDUsesLatestMarker(t *testing.T) {
	messages := []llm.Message{
		llm.UserText(workerIntentMarker(1) + "\nfirst"),
		llm.UserText(workerIntentMarker(2) + "\nsecond"),
	}
	if got := lastWorkerIntentID(messages); got != 2 {
		t.Fatalf("last intent=%d, want 2", got)
	}
	if !hasWorkerIntentMessage(messages, 1) || !hasWorkerIntentMessage(messages, 2) {
		t.Fatal("both historical markers should remain discoverable")
	}
}
