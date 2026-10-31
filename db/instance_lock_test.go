package db

import (
	"errors"
	"testing"
)

func TestInstanceLockExcludesSecondHolder(t *testing.T) {
	dsn := testDSN(t)
	d1, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d1.Close()
	d2, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()

	if err := d1.AcquireInstanceLock(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := d2.AcquireInstanceLock(t.Context()); !errors.Is(err, ErrInstanceLocked) {
		t.Fatalf("second instance acquired the lock: %v", err)
	}
	d1.ReleaseInstanceLock()
	if err := d2.AcquireInstanceLock(t.Context()); err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
}
