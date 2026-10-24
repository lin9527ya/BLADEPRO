package db

// 证据产物元数据（截图/附件）。文件本身由 evidence.Store 按 sha256 内容寻址保存；
// 本表只记录归属（任务/资产/节点）与可核对的哈希/大小/MIME。

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// EvidenceArtifact is one stored evidence file.
type EvidenceArtifact struct {
	ID        int64     `json:"id"`
	TaskID    int64     `json:"task_id,omitempty"`
	AssetID   int64     `json:"asset_id,omitempty"`
	NodeID    int64     `json:"node_id,omitempty"`
	Kind      string    `json:"kind"`
	Filename  string    `json:"filename,omitempty"`
	MIME      string    `json:"mime"`
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

const evidenceArtifactCols = `id, COALESCE(task_id,0), COALESCE(asset_id,0), COALESCE(node_id,0),
kind, filename, mime, sha256, size, created_at`

func scanEvidenceArtifact(sc interface{ Scan(...any) error }) (*EvidenceArtifact, error) {
	var a EvidenceArtifact
	if err := sc.Scan(&a.ID, &a.TaskID, &a.AssetID, &a.NodeID, &a.Kind, &a.Filename, &a.MIME, &a.SHA256, &a.Size, &a.CreatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

// CreateEvidenceArtifact inserts metadata. The caller must have already written the
// content-addressed file; sha256/size are verified by the evidence store.
func (d *DB) CreateEvidenceArtifact(ctx context.Context, a *EvidenceArtifact) (*EvidenceArtifact, error) {
	if d == nil || a == nil || a.SHA256 == "" {
		return nil, fmt.Errorf("invalid evidence artifact")
	}
	var taskID, assetID any
	if a.TaskID > 0 {
		taskID = a.TaskID
	}
	if a.AssetID > 0 {
		assetID = a.AssetID
	}
	row, err := scanEvidenceArtifact(d.QueryRowContext(ctx, `
INSERT INTO evidence_artifacts(task_id, asset_id, kind, filename, mime, sha256, size)
VALUES ($1,$2,$3,$4,$5,$6,$7)
RETURNING `+evidenceArtifactCols,
		taskID, assetID, a.Kind, a.Filename, a.MIME, a.SHA256, a.Size))
	if err != nil {
		return nil, err
	}
	return row, nil
}

// SetEvidenceArtifactNode links an artifact to the exploration fact node that
// carries it as evidence.
func (d *DB) SetEvidenceArtifactNode(ctx context.Context, id, nodeID int64) error {
	if d == nil || id <= 0 || nodeID <= 0 {
		return nil
	}
	_, err := d.ExecContext(ctx, `UPDATE evidence_artifacts SET node_id=$2 WHERE id=$1`, id, nodeID)
	return err
}

// GetEvidenceArtifact returns nil when absent.
func (d *DB) GetEvidenceArtifact(ctx context.Context, id int64) (*EvidenceArtifact, error) {
	if d == nil || id <= 0 {
		return nil, nil
	}
	a, err := scanEvidenceArtifact(d.QueryRowContext(ctx, `SELECT `+evidenceArtifactCols+` FROM evidence_artifacts WHERE id=$1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

// ListEvidenceArtifacts returns a task's artifacts, newest first.
func (d *DB) ListEvidenceArtifacts(ctx context.Context, taskID int64) ([]EvidenceArtifact, error) {
	if d == nil || taskID <= 0 {
		return []EvidenceArtifact{}, nil
	}
	rows, err := d.QueryContext(ctx, `SELECT `+evidenceArtifactCols+`
FROM evidence_artifacts WHERE task_id=$1 ORDER BY id DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EvidenceArtifact{}
	for rows.Next() {
		a, err := scanEvidenceArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// ScreenshotArtifactForNode returns the image artifact attached to an exploration
// node, or nil when the node has no verified image. Used by the screenshots check.
func (d *DB) ScreenshotArtifactForNode(ctx context.Context, taskID, nodeID int64) (*EvidenceArtifact, error) {
	if d == nil || taskID <= 0 || nodeID <= 0 {
		return nil, nil
	}
	a, err := scanEvidenceArtifact(d.QueryRowContext(ctx, `SELECT `+evidenceArtifactCols+`
FROM evidence_artifacts
WHERE task_id=$1 AND node_id=$2 AND mime LIKE 'image/%'
ORDER BY id DESC LIMIT 1`, taskID, nodeID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

// ScreenshotArtifactForNode is the SrcStore-facing wrapper used by the check
// validators (they hold a SrcStore, not the DB).
func (s *SrcStore) ScreenshotArtifactForNode(ctx context.Context, taskID, nodeID int64) (*EvidenceArtifact, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	return s.db.ScreenshotArtifactForNode(ctx, taskID, nodeID)
}
