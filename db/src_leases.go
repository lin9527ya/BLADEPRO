package db

// SRC 单元格租约（P2）：同一 (task, asset, risk_area) 同时只允许一个意图占用。
//
// 语义刻意放在**意图创建时**而不是 worker 运行时：重复发包的根因是 planner 在
// 同一个格子上换措辞连派多条意图。add_intent 先占租约，失败就直接拒绝；worker
// 结束时释放。租约按"意图风险面"（db.RiskAreas）而不是 srcmode 的粗面记账，
// 避免把同一资产上不同手法的并行测试误伤。

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// SrcLeaseTTL 是单元格租约的默认存活时间；worker 活动时会续租。
const SrcLeaseTTL = 20 * time.Minute

// SrcLease 是一条活动租约（供 planner/UI 展示"谁在测什么"）。
type SrcLease struct {
	ID            int64     `json:"id"`
	TaskID        int64     `json:"task_id"`
	AssetID       int64     `json:"asset_id"`
	Area          string    `json:"area"`
	OwnerIntentID int64     `json:"owner_intent_id,omitempty"`
	Worker        string    `json:"worker,omitempty"`
	ExpiresAt     time.Time `json:"expires_at"`
}

const srcLeaseCols = `id, task_id, asset_id, area, owner_intent_id, worker, expires_at`

func scanSrcLease(sc interface{ Scan(...any) error }) (*SrcLease, error) {
	var l SrcLease
	err := sc.Scan(&l.ID, &l.TaskID, &l.AssetID, &l.Area, &l.OwnerIntentID, &l.Worker, &l.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

func normalizeLeaseArea(area string) string {
	area = strings.ToLower(strings.TrimSpace(area))
	if area == "" {
		return "other"
	}
	return area
}

// AcquireSrcLeases 为一组资产占租约（全有或全无）。返回 busy 时调用方不应创建意图；
// 已占到的租约会在内部回滚，不会留下半套。ownerIntentID=0 表示先占位、待意图落库后 bind。
func (s *SrcStore) AcquireSrcLeases(ctx context.Context, taskID int64, assetIDs []int64, area string, ownerIntentID int64, worker string, ttl time.Duration) (acquired []SrcLease, busy []SrcLease, err error) {
	if s == nil || s.db == nil || taskID <= 0 || len(assetIDs) == 0 {
		return nil, nil, nil
	}
	if ttl <= 0 {
		ttl = SrcLeaseTTL
	}
	area = normalizeLeaseArea(area)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	// 本任务的过期租约先清掉：租约过期即可被重新占用。
	if _, err := tx.ExecContext(ctx, `DELETE FROM src_cell_leases WHERE task_id=$1 AND expires_at < now()`, taskID); err != nil {
		return nil, nil, err
	}
	seen := map[int64]bool{}
	for _, assetID := range assetIDs {
		if assetID <= 0 || seen[assetID] {
			continue
		}
		seen[assetID] = true
		var l *SrcLease
		l, err = scanSrcLease(tx.QueryRowContext(ctx, `
INSERT INTO src_cell_leases(task_id, asset_id, area, owner_intent_id, worker, expires_at)
VALUES ($1,$2,$3,$4,$5,now()+make_interval(secs => $6))
ON CONFLICT (task_id, asset_id, area) DO NOTHING
RETURNING `+srcLeaseCols, taskID, assetID, area, ownerIntentID, worker, int(ttl.Seconds())))
		if err == sql.ErrNoRows {
			held, qerr := scanSrcLease(tx.QueryRowContext(ctx,
				`SELECT `+srcLeaseCols+` FROM src_cell_leases WHERE task_id=$1 AND asset_id=$2 AND area=$3`, taskID, assetID, area))
			if qerr != nil {
				return nil, nil, qerr
			}
			busy = append(busy, *held)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		acquired = append(acquired, *l)
	}
	if len(busy) > 0 {
		return nil, busy, nil // defer rollback 释放本次已插入的行
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return acquired, nil, nil
}

// BindSrcLeaseIntent 把占位租约绑到刚创建的意图上。
func (s *SrcStore) BindSrcLeaseIntent(ctx context.Context, taskID int64, assetIDs []int64, area string, intentID int64) error {
	if s == nil || s.db == nil || taskID <= 0 || intentID <= 0 || len(assetIDs) == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE src_cell_leases SET owner_intent_id=$4
WHERE task_id=$1 AND asset_id = ANY($2) AND area=$3 AND owner_intent_id=0`,
		taskID, assetIDs, normalizeLeaseArea(area), intentID)
	return err
}

// ReleaseSrcLeasesByCells 释放"尚未绑定意图"的占位租约（意图创建失败时回滚用）。
func (s *SrcStore) ReleaseSrcLeasesByCells(ctx context.Context, taskID int64, assetIDs []int64, area string) error {
	if s == nil || s.db == nil || taskID <= 0 || len(assetIDs) == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM src_cell_leases
WHERE task_id=$1 AND asset_id = ANY($2) AND area=$3 AND owner_intent_id=0`,
		taskID, assetIDs, normalizeLeaseArea(area))
	return err
}

// ReleaseSrcLeases 释放某条意图持有的全部租约（worker 正常/异常结束时调用）。
func (s *SrcStore) ReleaseSrcLeases(ctx context.Context, taskID, intentID int64) error {
	if s == nil || s.db == nil || taskID <= 0 || intentID <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM src_cell_leases WHERE task_id=$1 AND owner_intent_id=$2`, taskID, intentID)
	return err
}

// RenewSrcLeases 续租；租约已被他人接管时返回 false（与意图租约同一套"心跳即进度"语义）。
func (s *SrcStore) RenewSrcLeases(ctx context.Context, taskID, intentID int64, ttl time.Duration) (bool, error) {
	if s == nil || s.db == nil || taskID <= 0 || intentID <= 0 {
		return false, nil
	}
	if ttl <= 0 {
		ttl = SrcLeaseTTL
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE src_cell_leases SET expires_at=now()+make_interval(secs => $3)
WHERE task_id=$1 AND owner_intent_id=$2 AND expires_at > now()`,
		taskID, intentID, int(ttl.Seconds()))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListActiveSrcLeases 返回任务当前有效的租约（planner/UI 用）。
func (s *SrcStore) ListActiveSrcLeases(ctx context.Context, taskID int64) ([]SrcLease, error) {
	if s == nil || s.db == nil || taskID <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+srcLeaseCols+` FROM src_cell_leases
WHERE task_id=$1 AND expires_at > now() ORDER BY asset_id, area LIMIT 500`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcLease{}
	for rows.Next() {
		l, err := scanSrcLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}
