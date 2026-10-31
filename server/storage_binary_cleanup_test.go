package server

import "testing"

func TestBinaryCleanupResidueAllowlist(t *testing.T) {
	for _, name := range []string{"bladepro.old", "bladepro.new", "bladepro.failed", "bladepro.upgrade.json", "bladepro.old.exe"} {
		if !isBinaryResidue(name) {
			t.Fatalf("residue rejected: %q", name)
		}
	}
	for _, name := range []string{"bladepro", "bladepro.exe", "data", "config.json", "jwt.key", "skills", "bladepro.old.bak", "", ".", "..", "sub/bladepro.old", `sub\bladepro.old`} {
		if isBinaryResidue(name) {
			t.Fatalf("non-residue accepted: %q", name)
		}
	}
}
