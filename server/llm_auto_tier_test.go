package server

import (
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestAutoTierForAgentMapping(t *testing.T) {
	cases := map[string]string{
		"planner":         db.ProfileTierCheap,
		"goals":           db.ProfileTierCheap,
		"worker":          db.ProfileTierStrong,
		"worker-cheap":    db.ProfileTierCheap,
		"worker-standard": db.ProfileTierStandard,
		"worker-strong":   db.ProfileTierStrong,
		"compress":        db.ProfileTierCheap,
		"mainagent":       "",
		"retester":        "",
		"":                "",
	}
	for key, want := range cases {
		if got := autoTierForAgent(key); got != want {
			t.Fatalf("autoTierForAgent(%q)=%q want %q", key, got, want)
		}
	}
}

// TestTierProfileCandidatesOrdering pins the selection rule: active profile wins,
// then priority DESC, then id ASC; unknown/empty tiers count as standard and are
// only selected when standard is what was asked for.
func TestTierProfileCandidatesOrdering(t *testing.T) {
	profs := []*db.LLMProfile{
		{ID: 1, Tier: "strong", Priority: 1},
		{ID: 2, Tier: "strong", Priority: 9},
		{ID: 3, Tier: "", Priority: 99}, // empty → standard, must not appear for strong
		{ID: 4, Tier: "STRONG", Priority: 9, IsDefault: true},
		{ID: 5, Tier: "cheap", Priority: 100},
	}
	got := tierProfileCandidates(profs, db.ProfileTierStrong)
	if len(got) != 3 {
		t.Fatalf("candidates=%d want 3", len(got))
	}
	want := []int64{4, 2, 1} // default first, then priority, then id
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("candidate[%d]=%d want %d (order %+v)", i, got[i].ID, id, got)
		}
	}
	if got := tierProfileCandidates(profs, db.ProfileTierCheap); len(got) != 1 || got[0].ID != 5 {
		t.Fatalf("cheap candidates=%+v want [5]", got)
	}
	if got := tierProfileCandidates(profs, db.ProfileTierStandard); len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("standard candidates=%+v want [3]", got)
	}
}
