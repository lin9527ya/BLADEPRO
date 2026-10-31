package db

import (
	"context"
	"database/sql"
	"encoding/json"
)

// TransactionView keeps all domain reads on the locked completion transaction.
// It does not own the pool or transaction; only its creator commits/rolls back.
func (d *DB) TransactionView(tx *sql.Tx) *DB { return &DB{DB: d.DB, tx: tx} }
func (d *DB) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	if d.tx != nil {
		return d.tx.ExecContext(ctx, q, a...)
	}
	return d.DB.ExecContext(ctx, q, a...)
}
func (d *DB) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	if d.tx != nil {
		return d.tx.QueryContext(ctx, q, a...)
	}
	return d.DB.QueryContext(ctx, q, a...)
}
func (d *DB) QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row {
	if d.tx != nil {
		return d.tx.QueryRowContext(ctx, q, a...)
	}
	return d.DB.QueryRowContext(ctx, q, a...)
}
func (d *DB) Exec(q string, a ...any) (sql.Result, error) {
	return d.ExecContext(context.Background(), q, a...)
}
func (d *DB) Query(q string, a ...any) (*sql.Rows, error) {
	return d.QueryContext(context.Background(), q, a...)
}
func (d *DB) QueryRow(q string, a ...any) *sql.Row {
	return d.QueryRowContext(context.Background(), q, a...)
}

// CompleteSrcTask locks the publication row while the domain validator reads its
// entire proof. Mutation fences use the same row, so no late goal can cross done.
func (d *DB) CompleteSrcTask(ctx context.Context, id int64, validate func(*DB, *Task) (json.RawMessage, error)) (bool, error) {
	if err := d.SealSrcAuthorization(ctx, id); err != nil {
		return false, err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT id FROM tasks WHERE id=$1 FOR UPDATE`, id); err != nil {
		return false, err
	}
	view := d.TransactionView(tx)
	task, err := view.GetTask(id)
	if err != nil {
		return false, err
	}
	if task == nil || task.Mode != "src" || IsTerminal(task.Status) || task.Paused {
		return false, nil
	}
	proof, err := validate(view, task)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO src_completion_proofs(task_id,revision,proof) SELECT id,src_completion_revision,$2 FROM tasks WHERE id=$1`, id, proof); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET status='done',completed_at=now(),src_completed_revision=src_completion_revision WHERE id=$1`, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
