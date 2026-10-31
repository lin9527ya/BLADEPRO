package server

import (
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestAgentRowCacheCopiesAndInvalidates(t *testing.T) {
	c := newAgentRowCache()
	id := int64(7)
	row := &db.Agent{Key: "worker", MaxTurns: 3, LLMProfileID: &id}
	c.put(row.Key, row)
	got := c.get(row.Key)
	if got == nil || got.MaxTurns != 3 || got.LLMProfileID == nil || *got.LLMProfileID != 7 {
		t.Fatalf("cache miss: %+v", got)
	}
	// Callers must not mutate the cached row through a shared pointer.
	*got.LLMProfileID = 99
	got.MaxTurns = 99
	again := c.get(row.Key)
	if again.MaxTurns != 3 || *again.LLMProfileID != 7 {
		t.Fatalf("cache row was mutated: %+v", again)
	}
	c.invalidate()
	if c.get(row.Key) != nil {
		t.Fatal("invalidate did not clear the row")
	}
}
