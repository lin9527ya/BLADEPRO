package server

import (
	"sync"
	"time"

	"github.com/lin9527ya/bladepro/db"
)

// agentRowCacheTTL bounds staleness for agent settings that are read on every
// worker-loop availability check. Writes invalidate explicitly; the TTL is only a
// safety net for a missed write path.
const agentRowCacheTTL = 5 * time.Second

type agentRowEntry struct {
	row     *db.Agent
	expires time.Time
}

type agentRowCache struct {
	mu   sync.RWMutex
	rows map[string]agentRowEntry
}

func newAgentRowCache() *agentRowCache {
	return &agentRowCache{rows: map[string]agentRowEntry{}}
}

func (c *agentRowCache) get(key string) *db.Agent {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	entry, ok := c.rows[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(entry.expires) {
		return nil
	}
	return copyAgentRow(entry.row)
}

func (c *agentRowCache) put(key string, row *db.Agent) {
	if c == nil || row == nil {
		return
	}
	c.mu.Lock()
	c.rows[key] = agentRowEntry{row: copyAgentRow(row), expires: time.Now().Add(agentRowCacheTTL)}
	c.mu.Unlock()
}

func (c *agentRowCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.rows = map[string]agentRowEntry{}
	c.mu.Unlock()
}

func copyAgentRow(row *db.Agent) *db.Agent {
	if row == nil {
		return nil
	}
	cp := *row
	if row.LLMProfileID != nil {
		id := *row.LLMProfileID
		cp.LLMProfileID = &id
	}
	return &cp
}

// agentRow is the cached read path for hot agent-row lookups. Writes and
// consistency-sensitive admin reads should keep using GetAgentByKey directly.
func (s *Server) agentRow(key string) (*db.Agent, error) {
	if s == nil || s.m == nil || s.m.pg == nil {
		return nil, nil
	}
	s.agents.rowMu.Lock()
	if s.agents.rows == nil {
		s.agents.rows = newAgentRowCache()
	}
	cache := s.agents.rows
	s.agents.rowMu.Unlock()
	if row := cache.get(key); row != nil {
		return row, nil
	}
	row, err := s.m.pg.GetAgentByKey(key)
	if err != nil {
		return nil, err
	}
	cache.put(key, row)
	return copyAgentRow(row), nil
}

func (s *Server) invalidateAgentRows() {
	if s == nil {
		return
	}
	s.agents.rowMu.Lock()
	if s.agents.rows != nil {
		s.agents.rows.invalidate()
	}
	s.agents.rowMu.Unlock()
}
