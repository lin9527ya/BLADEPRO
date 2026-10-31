package server

import (
	"context"
	"sync"
	"sync/atomic"
)

type backgroundLoops struct {
	mu      sync.Mutex
	wg      sync.WaitGroup
	closing bool
	live    atomic.Int64
}

func (b *backgroundLoops) start(fn func()) {
	b.mu.Lock()
	if b.closing {
		b.mu.Unlock()
		return
	}
	b.wg.Add(1)
	b.live.Add(1)
	b.mu.Unlock()
	goSafe("server/background", &b.wg, func() { defer b.live.Add(-1); fn() })
}
func (b *backgroundLoops) wait(ctx context.Context) int64 {
	b.mu.Lock()
	b.closing = true
	b.mu.Unlock()
	done := make(chan struct{})
	go func() { b.wg.Wait(); close(done) }()
	select {
	case <-done:
		return 0
	case <-ctx.Done():
		return b.live.Load()
	}
}

// Shutdown cancels owned work and waits before the caller closes the database.
// All stages share the caller's deadline; unfinished work is reported, not hidden.
func (s *Server) Shutdown(ctx context.Context) int64 {
	if s == nil {
		return 0
	}
	if s.cancel != nil {
		s.cancel()
	}
	var executionLeft int64
	if s.m != nil {
		executionLeft = s.m.Executions().ShutdownContext(ctx)
	}
	left := executionLeft + s.WaitAllQuiescent(ctx) + s.loops.wait(ctx)
	done := make(chan struct{})
	go func() { s.archive.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		left++
	}
	if s.side != nil {
		select {
		case <-s.side.done:
		case <-ctx.Done():
			left++
		}
	}
	if !logSink.CloseDB(ctx) {
		left++
	}
	return left
}
