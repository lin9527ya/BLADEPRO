package server

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

func TestLogQueueDrainsAndSubscriberCloseIsSafe(t *testing.T) {
	m, _, _ := p0Fixture(t, "logs-fixture.example")
	sink := &logSinkT{cap: 100, out: io.Discard, subs: map[chan LogLine]struct{}{}}
	sink.SetDB(t.Context(), m.pg)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, unsubscribe := sink.subscribe()
				sink.add(LogLine{Tag: "p0-log-fixture", Level: "info", Text: "fixture"})
				unsubscribe()
			}
		}()
	}
	wg.Wait()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if !sink.CloseDB(ctx) {
		t.Fatal("log drain timed out")
	}
	var count int
	if err := m.pg.QueryRow(`SELECT count(*) FROM server_logs WHERE tag='p0-log-fixture'`).Scan(&count); err != nil || count != 80 {
		t.Fatalf("persisted=%d %v", count, err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM server_logs WHERE tag='p0-log-fixture'`) })
	dropped, failed := sink.DBLoss()
	if dropped != 0 || failed != 0 {
		t.Fatalf("unexpected loss %d/%d", dropped, failed)
	}
}

func TestLogShutdownDeadlineCancelsBlockedWrite(t *testing.T) {
	m, _, _ := p0Fixture(t, "logs-deadline.example")
	sink := &logSinkT{cap: 100, out: io.Discard, subs: map[chan LogLine]struct{}{}}
	sink.SetDB(t.Context(), m.pg)
	tx, err := m.pg.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`LOCK TABLE server_logs IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	sink.add(LogLine{Level: "info", Tag: "blocked-p0", Text: "fixture"})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if sink.CloseDB(ctx) {
		t.Fatal("blocked log unexpectedly drained")
	}
	if time.Since(start) > time.Second {
		t.Fatal("shutdown exceeded deadline")
	}
	select {
	case <-sink.dbDone:
	case <-time.After(time.Second):
		t.Fatal("cancelled writer did not stop")
	}
}
