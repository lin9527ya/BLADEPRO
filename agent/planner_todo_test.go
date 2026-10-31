package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPlannerTodoPersistsAcrossPlannerInstances(t *testing.T) {
	dir := t.TempDir()
	p1 := NewPlanner(nil, "m", dir, nil, 0, 0)
	store := p1.todoFor(7)
	raw, _ := json.Marshal(map[string]any{"todos": []map[string]string{
		{"content": "确认上传点", "status": "completed"},
		{"content": "构造绕过链", "status": "in_progress"},
	}})
	if _, err := store.Tool().Call(context.Background(), raw, nil); err != nil {
		t.Fatal(err)
	}
	p1.persistTodos(store, 42)

	p2 := NewPlanner(nil, "m", dir, nil, 0, 0)
	restored := p2.todoFor(7)
	p2.seedTodos(restored, 42)
	items := restored.List()
	if len(items) != 2 || items[0].Content != "确认上传点" || items[1].Status != "in_progress" {
		t.Fatalf("restored todos=%+v", items)
	}
}
