package server

import (
	"context"
	"net/http"
	"runtime"
	"time"
)

var processStarted = time.Now()

// systemMetrics is the authenticated operational view: why a task is slow,
// stopped or spending money. It deliberately exposes task IDs, never task names,
// assets, prompts or credentials.
func (s *Server) systemMetrics(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"now":            time.Now().UTC(),
		"uptime_seconds": int64(time.Since(processStarted).Seconds()),
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	out["runtime"] = map[string]any{
		"goroutines":       runtime.NumGoroutine(),
		"heap_alloc_bytes": ms.HeapAlloc,
		"heap_sys_bytes":   ms.HeapSys,
		"gc_cycles":        ms.NumGC,
	}
	if pg := s.m.PG(); pg != nil {
		st := pg.Stats()
		out["db"] = map[string]any{
			"open": st.OpenConnections, "in_use": st.InUse, "idle": st.Idle,
			"wait": st.WaitCount, "max_open": st.MaxOpenConnections,
		}
		if p50, p95, err := pg.LLMLatencyPercentiles(r.Context(), 24*time.Hour); err == nil {
			out["llm_latency_24h"] = map[string]any{"p50_ms": p50, "p95_ms": p95}
		}
	}
	tasks := s.m.List()
	rows := make([]map[string]any, 0, len(tasks))
	statusCounts := map[string]int{}
	dropped := map[string]int64{}
	totalDropped := int64(0)
	for _, t := range tasks {
		status := t.lifecycleSnapshot().Status
		statusCounts[status]++
		states, _ := t.Store.IntentStateCounts()
		claimable, _ := t.Store.CountClaimableIntents()
		workers := s.engine.TaskWorkers(t.ID)
		running := states["running"]
		idle := workers - running
		if idle < 0 {
			idle = 0
		}
		rows = append(rows, map[string]any{
			"id": t.ID, "status": status, "workers": workers,
			"running_intents": running, "idle_slots": idle,
			"claimable_intents": claimable, "intent_states": states,
			"inflight": s.engine.inflightCount(t.ID), "active_llm_calls": s.engine.ActiveLLMCalls(t.ID),
		})
		if n := s.engine.DroppedActivity(t.ID); n > 0 {
			dropped[t.ID] = n
			totalDropped += n
		}
	}
	out["tasks"] = rows
	out["task_status_counts"] = statusCounts
	out["dropped_activity"] = map[string]any{"total": totalDropped, "by_task": dropped}
	writeJSON(w, http.StatusOK, out)
}

// systemDependencies is a read-only dependency health surface for the SRE/UI.
// It does not return DSNs, profile names or credential values.
func (s *Server) systemDependencies(w http.ResponseWriter, r *http.Request) {
	checks := map[string]any{}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	pgOK := true
	if pg := s.m.PG(); pg == nil {
		pgOK = false
		checks["postgres"] = map[string]any{"ok": false, "detail": "not configured"}
	} else if err := pg.PingContext(ctx); err != nil {
		pgOK = false
		checks["postgres"] = map[string]any{"ok": false, "detail": "ping failed"}
	} else {
		checks["postgres"] = map[string]any{"ok": true}
		if v, ok, _ := pg.GetSetting("schema_fingerprint"); ok && v != "" {
			checks["schema_fingerprint"] = map[string]any{"ok": true}
		}
		checks["instance_lock"] = map[string]any{"ok": pg.InstanceLockHeld()}
	}
	checks["llm"] = map[string]any{"ok": s.engine != nil && s.engine.Ready()}
	checks["browser"] = map[string]any{"ok": screenshotBrowser() != ""}
	checks["executions"] = map[string]any{"ok": s.m.Executions() != nil}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": pgOK, "checks": checks, "now": time.Now().UTC(),
	})
}
