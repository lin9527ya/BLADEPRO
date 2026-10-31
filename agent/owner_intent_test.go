package agent

import "testing"

func TestWorkerWriteBackCannotTargetOldIntent(t *testing.T) {
	tools := NewToolSet(nil, "worker")
	tools.SetOwnerIntent(42)
	if id, err := tools.writeIntentID(0); err != nil || id != 42 {
		t.Fatalf("default owner=%d %v", id, err)
	}
	if _, err := tools.writeIntentID(41); err == nil {
		t.Fatal("old intent accepted")
	}
	if id, err := tools.writeIntentID(42); err != nil || id != 42 {
		t.Fatal("current intent rejected")
	}
}
