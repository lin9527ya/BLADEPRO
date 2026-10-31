package agent

import (
	"context"
	"iter"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lin9527ya/Norma/llm"
	"github.com/lin9527ya/Norma/transcript"
	"github.com/lin9527ya/bladepro/db"
)

type slotRecordingProvider struct{ reqs []llm.CompletionRequest }

func (p *slotRecordingProvider) Stream(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.reqs = append(p.reqs, req)
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "done"}, nil) {
			return
		}
		yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
	}
}

func (p *slotRecordingProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for ev, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(ev)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

// A worker slot reuses one transcript across intents: the second intent must see
// the first intent's conversation, while the system prefix stays byte-identical.
func TestWorkerSlotTranscriptReusedAcrossIntents(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("slot-resume", "槽位续跑", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	ts := d.Exploration(task.ExplorationID)
	firstID, err := ts.AddIntent(map[string]any{"summary": "first direction SENTINEL"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := ts.AddIntent(map[string]any{"summary": "second direction SENTINEL"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	first, err := ts.GetNode(firstID)
	if err != nil || first == nil {
		t.Fatalf("get first intent: %v", err)
	}
	second, err := ts.GetNode(secondID)
	if err != nil || second == nil {
		t.Fatalf("get second intent: %v", err)
	}
	dir := t.TempDir()
	store := transcript.NewStore(filepath.Join(dir, "transcripts"))
	prov := &slotRecordingProvider{}
	w := NewWorker(prov, "fixture", dir, store, 100000, 2)
	for _, intent := range []*db.Node{first, second} {
		if _, _, err := w.Execute(t.Context(), "work#1", task.ID, d.Assets(), ts, intent, nil, nil, nil, nil); err != nil {
			t.Fatalf("execute intent %d: %v", intent.ID, err)
		}
	}
	if len(prov.reqs) != 2 {
		t.Fatalf("provider calls=%d, want 2", len(prov.reqs))
	}
	if got, want := strings.Join(prov.reqs[0].System, "\n"), strings.Join(prov.reqs[1].System, "\n"); got != want {
		t.Fatal("slot system prompt changed between intents; prefix cache would miss")
	}
	if prov.reqs[0].DynamicBoundary != prov.reqs[1].DynamicBoundary {
		t.Fatalf("dynamic boundary changed: %d vs %d", prov.reqs[0].DynamicBoundary, prov.reqs[1].DynamicBoundary)
	}
	var secondText strings.Builder
	for _, msg := range prov.reqs[1].Messages {
		secondText.WriteString(msg.Text())
		for _, b := range msg.Content {
			if b.Type == llm.BlockText {
				secondText.WriteString(b.Text)
			}
		}
	}
	text := secondText.String()
	if !strings.Contains(text, "first direction SENTINEL") {
		t.Fatalf("second intent transcript did not resume first conversation:\n%s", text)
	}
	if !strings.Contains(text, workerIntentMarker(secondID)) {
		t.Fatalf("second request missing current intent marker:\n%s", text)
	}
}
