package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSystemMetricsAndDependenciesShape(t *testing.T) {
	m, task, _ := p0Fixture(t, "metrics-"+time.Now().Format("150405")+".example")
	s := &Server{m: m, engine: NewEngine(m)}
	rec := httptest.NewRecorder()
	s.systemMetrics(rec, httptest.NewRequest("GET", "/api/system/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("metrics status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["tasks"]; !ok {
		t.Fatalf("metrics missing tasks: %v", out)
	}
	if _, ok := out["dropped_activity"]; !ok {
		t.Fatalf("metrics missing dropped_activity: %v", out)
	}
	raw := rec.Body.String()
	if task.Name != "" && task.Name != "P0 fixture" {
		t.Fatal("unexpected fixture name")
	}
	if len(raw) == 0 {
		t.Fatal("empty metrics")
	}

	rec = httptest.NewRecorder()
	s.systemDependencies(rec, httptest.NewRequest("GET", "/api/system/dependencies", nil))
	if rec.Code != 200 {
		t.Fatalf("dependencies status=%d body=%s", rec.Code, rec.Body.String())
	}
	var dep map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &dep); err != nil {
		t.Fatal(err)
	}
	checks, ok := dep["checks"].(map[string]any)
	if !ok || checks["postgres"] == nil {
		t.Fatalf("dependencies missing checks: %v", dep)
	}
}
