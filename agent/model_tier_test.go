package agent

import (
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestIntentWorkerTier(t *testing.T) {
	if got := intentWorkerTier(nil); got != "" {
		t.Fatalf("nil intent tier=%q", got)
	}
	if got := intentWorkerTier(&db.Node{Payload: []byte(`{"summary":"x"}`)}); got != "" {
		t.Fatalf("missing tier=%q", got)
	}
	if got := intentWorkerTier(&db.Node{Payload: []byte(`{"worker_tier":"cheap"}`)}); got != "cheap" {
		t.Fatalf("tier=%q", got)
	}
}
