package db

// 结构化负知识：记录“这个资产/端点/风险面的这个打法确认走不通”。
//
// 与 src_cells 的 tested_clear 分工：格是“这个面测过”，负知识是“这个具体打法
// 在这个具体入口上不成立”。后者更细，能跨任务复用，避免下一个任务再交一遍学费。

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SrcNegative is one structured negative-knowledge row.
type SrcNegative struct {
	ID        int64     `json:"id"`
	TaskID    int64     `json:"task_id"`
	AssetID   int64     `json:"asset_id"`
	Asset     string    `json:"asset,omitempty"`
	Area      string    `json:"area"`
	Endpoint  string    `json:"endpoint,omitempty"`
	Technique string    `json:"technique,omitempty"`
	Reason    string    `json:"reason"`
	Source    string    `json:"source,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const srcNegativeCols = `n.id, n.task_id, n.asset_id,
COALESCE(a.url, a.domain, a.ip, a.app_name, a.root_domain, ''),
n.area, n.endpoint, n.technique, n.reason, n.source, n.created_at, n.updated_at`

func scanSrcNegative(sc interface{ Scan(...any) error }) (*SrcNegative, error) {
	var n SrcNegative
	if err := sc.Scan(&n.ID, &n.TaskID, &n.AssetID, &n.Asset, &n.Area, &n.Endpoint,
		&n.Technique, &n.Reason, &n.Source, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return nil, err
	}
	return &n, nil
}

// UpsertSrcNegative inserts or refreshes one negative fact. The unique key is
// (task, asset, area, endpoint, technique); re-reporting updates the reason.
func (s *SrcStore) UpsertSrcNegative(ctx context.Context, n SrcNegative) (*SrcNegative, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("src store unavailable")
	}
	if n.TaskID <= 0 || strings.TrimSpace(n.Reason) == "" {
		return nil, fmt.Errorf("负知识需要 task_id 与 reason")
	}
	source := strings.TrimSpace(n.Source)
	if source == "" {
		source = "agent"
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `
INSERT INTO src_negative_knowledge(task_id, asset_id, area, endpoint, technique, reason, source)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (task_id, asset_id, area, endpoint, technique)
DO UPDATE SET reason=EXCLUDED.reason, source=EXCLUDED.source, updated_at=now()
RETURNING id`, n.TaskID, n.AssetID, strings.TrimSpace(n.Area),
		strings.TrimSpace(n.Endpoint), strings.TrimSpace(n.Technique), strings.TrimSpace(n.Reason), source).Scan(&id)
	if err != nil {
		return nil, err
	}
	row, err := scanSrcNegative(s.db.QueryRowContext(ctx, `SELECT `+srcNegativeCols+`
FROM src_negative_knowledge n JOIN assets a ON a.id=n.asset_id WHERE n.id=$1`, id))
	if err != nil {
		return nil, err
	}
	return row, nil
}

// ListSrcNegativesForTask returns negatives for assets in this task's scope,
// including facts recorded by earlier tasks on the same assets (cross-task reuse).
// The current task's own rows are included too so the agent sees its own notes.
func (s *SrcStore) ListSrcNegativesForTask(ctx context.Context, taskID int64, limit int) ([]SrcNegative, error) {
	if s == nil || s.db == nil || taskID <= 0 {
		return []SrcNegative{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `WITH `+srcTargetCTE+`
SELECT `+srcNegativeCols+`
FROM src_negative_knowledge n
JOIN assets a ON a.id=n.asset_id
WHERE n.asset_id IN (SELECT id FROM target)
ORDER BY n.updated_at DESC, n.id DESC LIMIT $2`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcNegative{}
	for rows.Next() {
		n, err := scanSrcNegative(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// ListSrcNegativesForAsset returns negatives for one asset (all tasks).
func (s *SrcStore) ListSrcNegativesForAsset(ctx context.Context, assetID int64, limit int) ([]SrcNegative, error) {
	if s == nil || s.db == nil || assetID <= 0 {
		return []SrcNegative{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+srcNegativeCols+`
FROM src_negative_knowledge n JOIN assets a ON a.id=n.asset_id
WHERE n.asset_id=$1 ORDER BY n.updated_at DESC, n.id DESC LIMIT $2`, assetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcNegative{}
	for rows.Next() {
		n, err := scanSrcNegative(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// DeleteSrcNegative removes one row, scoped to the task that owns it. Returning
// found=false when the id belongs to another task keeps the tool from deleting
// another engagement's notes.
func (s *SrcStore) DeleteSrcNegative(ctx context.Context, taskID, id int64) (bool, error) {
	if s == nil || s.db == nil || taskID <= 0 || id <= 0 {
		return false, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM src_negative_knowledge WHERE id=$1 AND task_id=$2`, id, taskID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// srcNegativeCount is a small helper for reports/tests.
func (s *SrcStore) SrcNegativeCount(ctx context.Context, taskID int64) (int, error) {
	if s == nil || s.db == nil || taskID <= 0 {
		return 0, nil
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM src_negative_knowledge WHERE task_id=$1`, taskID).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}
