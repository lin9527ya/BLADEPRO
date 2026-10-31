package traffic

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpillFailureCannotBecomeCompleteEvidence(t *testing.T) {
	tr, dir := openTraffic(t)
	if err := os.WriteFile(filepath.Join(dir, "_blobs", "sha256"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	tr.record(newFlow("partial.example", "GET", "/", nil, bytes.Repeat([]byte("x"), maxInlineBody+1)))
	rows, err := tr.Since("partial.example", 0, 1)
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	var state string
	if err := tr.db.QueryRow(`SELECT resp_state FROM exchanges WHERE id=?`, rows[0].ID).Scan(&state); err != nil || state != "partial" {
		t.Fatalf("integrity=%q %v", state, err)
	}
	if err := tr.ReadEvidence(context.Background(), []string{rows[0].ID}, func(EvidenceExchange) error { return nil }); err == nil {
		t.Fatal("partial body accepted as full evidence")
	}
	_, preview, err := tr.Get(rows[0].ID)
	if err != nil || !strings.Contains(preview, "partial") {
		t.Fatal("partial preview hides integrity", err)
	}
}
