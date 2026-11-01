package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrSoftBriefRevision is returned when a runtime soft-brief update is based on
// a stale revision. Hard fields are never changed by this path.
var ErrSoftBriefRevision = errors.New("soft brief revision conflict")

// UpdateTaskSoftBrief replaces only mode_doc.soft_brief under a row lock. The
// expected revision is the current soft revision; a mismatch is a 409 to the
// caller, not a last-writer-wins overwrite.
func (d *DB) UpdateTaskSoftBrief(ctx context.Context, id int64, expectedRevision int, soft json.RawMessage) (json.RawMessage, error) {
	if d == nil || d.DB == nil || id <= 0 {
		return nil, fmt.Errorf("task not found")
	}
	if len(soft) == 0 || !json.Valid(soft) {
		return nil, fmt.Errorf("soft brief must be valid JSON")
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var mode string
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(mode,''), mode_doc FROM tasks
WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&mode, &raw); err != nil {
		return nil, err
	}
	if mode != "src" {
		return nil, fmt.Errorf("task is not SRC mode")
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse mode_doc: %w", err)
	}
	current := 0
	if old := doc["soft_brief"]; len(old) > 0 {
		var b struct {
			Revision int `json:"revision"`
		}
		_ = json.Unmarshal(old, &b)
		current = b.Revision
	}
	if current != expectedRevision {
		return nil, ErrSoftBriefRevision
	}
	var incoming struct {
		Revision int `json:"revision"`
	}
	_ = json.Unmarshal(soft, &incoming)
	if incoming.Revision != expectedRevision+1 {
		return nil, fmt.Errorf("soft brief revision must be %d", expectedRevision+1)
	}
	doc["soft_brief"] = soft
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET mode_doc=$2::jsonb WHERE id=$1`, id, out); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
