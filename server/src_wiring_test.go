package server

import (
	"context"
	"github.com/lin9527ya/bladepro/db"
	"reflect"
	"testing"
)

func TestTaskAgentsHaveScreenshotHostImplementation(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	task, err := m.CreateTaskWithOptions("wiring", "wiring", db.TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatal(err)
	}
	defer m.pg.DeleteTask(parseIntDefault(task.ID))
	s := &Server{m: m}
	s.agents.bundles = map[string]*taskAgentBundle{}
	b := s.agentsForTask(task)
	for name, worker := range map[string]any{"worker": b.wk, "planner": b.pl} {
		if reflect.ValueOf(worker).Elem().FieldByName("srcScreenshotter").IsNil() {
			t.Fatalf("%s screenshot implementation not wired", name)
		}
	}
}

func TestSrcFinishGateQueryErrorDoesNotComplete(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	task, err := m.CreateTaskWithOptions("gate", "gate", db.TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatal(err)
	}
	defer m.pg.DeleteTask(parseIntDefault(task.ID))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	engine := NewEngine(m)
	if engine.srcFinishGate(ctx, task, parseIntDefault(task.ID)) {
		t.Fatal("gate query error allowed completion")
	}
	if state := engine.srcGate[task.ID]; state != nil && state.rejects > 0 {
		t.Fatal("infrastructure failure counted as policy exhaustion")
	}
}
