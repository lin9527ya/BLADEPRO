package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// instanceLockKey is a PRIVATE advisory-lock key for "one process per database".
// Do not reuse the test-suite lock, retest lock, schema-migration lock or any
// BLADE key: all of those are acquired by different subsystems.
const instanceLockKey int64 = 7337741011

// ErrInstanceLocked means another process already owns this database.
var ErrInstanceLocked = errors.New("another BLADEPRO instance is using this database")

// AcquireInstanceLock pins one pooled connection and takes a session advisory
// lock. The connection must stay checked out: closing it would release the lock.
func (d *DB) AcquireInstanceLock(ctx context.Context) error {
	if d == nil || d.DB == nil {
		return errors.New("database unavailable")
	}
	if v := strings.TrimSpace(os.Getenv("BLADEPRO_ALLOW_MULTI_INSTANCE")); v == "1" || strings.EqualFold(v, "true") {
		return nil
	}
	d.lockMu.Lock()
	defer d.lockMu.Unlock()
	if d.lockConn != nil {
		return nil
	}
	conn, err := d.Conn(ctx)
	if err != nil {
		return fmt.Errorf("instance lock connection: %w", err)
	}
	var got bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, instanceLockKey).Scan(&got); err != nil {
		conn.Close()
		return fmt.Errorf("instance lock: %w", err)
	}
	if !got {
		conn.Close()
		return ErrInstanceLocked
	}
	d.lockConn = conn
	return nil
}

// InstanceLockHeld reports whether this handle currently owns the instance lock.
func (d *DB) InstanceLockHeld() bool {
	if d == nil {
		return false
	}
	d.lockMu.Lock()
	defer d.lockMu.Unlock()
	return d.lockConn != nil
}

// ReleaseInstanceLock is idempotent and also runs from DB.Close.
func (d *DB) ReleaseInstanceLock() {
	if d == nil {
		return
	}
	d.lockMu.Lock()
	conn := d.lockConn
	d.lockConn = nil
	d.lockMu.Unlock()
	if conn == nil {
		return
	}
	_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, instanceLockKey)
	_ = conn.Close()
}

// Close releases the instance lock before closing the pool.
func (d *DB) Close() error {
	if d == nil {
		return nil
	}
	d.ReleaseInstanceLock()
	if d.DB == nil {
		return nil
	}
	return d.DB.Close()
}
