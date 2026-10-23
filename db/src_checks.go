package db

// 收尾闸（src_checks）：技能的 rules/site-close-checklist.md 落成的数据。
//
// 与 src_cells 的分工：cell 管"这个风险面测过没有"，check 管"测够没有"
// （JS 抽全、路径够不够 200 条、非 404 复测、方法矩阵、参数变体、防护对照、
// 判死三条件、截图）。两者都只存终态，空格靠计算；两者都进收尾门。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// 收尾项终态。done 必须带数字（metrics 达标）与证据节点；na 必须给理由码。
const (
	SrcCheckDone       = "done"
	SrcCheckNA         = "na"
	SrcCheckBudgetStop = "budget_stop"
)

// IsSrcCheckState reports whether s is a legal terminal check state.
func IsSrcCheckState(s string) bool {
	return s == SrcCheckDone || s == SrcCheckNA || s == SrcCheckBudgetStop
}

// SrcCheck is one (asset × closure item) terminal row of a task.
type SrcCheck struct {
	ID             int64          `json:"id"`
	TaskID         int64          `json:"task_id"`
	AssetID        int64          `json:"asset_id"`
	Asset          string         `json:"asset,omitempty"`
	AssetType      string         `json:"asset_type,omitempty"`
	CheckKey       string         `json:"check_key"`
	State          string         `json:"state"`
	NACode         string         `json:"na_code,omitempty"`
	Reason         string         `json:"reason,omitempty"`
	EvidenceNodeID int64          `json:"evidence_node_id,omitempty"`
	Metrics        map[string]int `json:"metrics,omitempty"`
	UpdatedBy      string         `json:"updated_by,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

const srcCheckCols = `c.id, c.task_id, c.asset_id,
COALESCE(a.url, a.domain, a.ip, a.app_name, a.root_domain, ''), COALESCE(a.type,''),
c.check_key, c.state, c.na_code, c.reason,
COALESCE(c.evidence_node_id,0), c.metrics, c.updated_by, c.created_at, c.updated_at`

func scanSrcCheck(sc interface{ Scan(...any) error }) (*SrcCheck, error) {
	var c SrcCheck
	var metrics []byte
	if err := sc.Scan(&c.ID, &c.TaskID, &c.AssetID, &c.Asset, &c.AssetType,
		&c.CheckKey, &c.State, &c.NACode, &c.Reason,
		&c.EvidenceNodeID, &metrics, &c.UpdatedBy, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	if len(metrics) > 0 {
		_ = json.Unmarshal(metrics, &c.Metrics)
	}
	return &c, nil
}

// UpsertSrcCheck writes (or overwrites) one closure item's terminal state.
func (s *SrcStore) UpsertSrcCheck(ctx context.Context, c *SrcCheck) (*SrcCheck, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("src store unavailable")
	}
	if c == nil || c.TaskID <= 0 || c.AssetID <= 0 || c.CheckKey == "" || !IsSrcCheckState(c.State) {
		return nil, fmt.Errorf("invalid src check")
	}
	metrics := c.Metrics
	if metrics == nil {
		metrics = map[string]int{}
	}
	raw, err := json.Marshal(metrics)
	if err != nil {
		return nil, err
	}
	var ev any
	if c.EvidenceNodeID > 0 {
		ev = c.EvidenceNodeID
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `
INSERT INTO src_checks(task_id, asset_id, check_key, state, na_code, reason, evidence_node_id, metrics, updated_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (task_id, asset_id, check_key) DO UPDATE SET
    state            = EXCLUDED.state,
    na_code          = EXCLUDED.na_code,
    reason           = EXCLUDED.reason,
    evidence_node_id = COALESCE(EXCLUDED.evidence_node_id, src_checks.evidence_node_id),
    metrics          = EXCLUDED.metrics,
    updated_by       = EXCLUDED.updated_by
RETURNING id`,
		c.TaskID, c.AssetID, c.CheckKey, c.State, c.NACode, c.Reason, ev, string(raw), c.UpdatedBy).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetSrcCheck(ctx, c.TaskID, c.AssetID, c.CheckKey)
}

// GetSrcCheck returns one closure row, or (nil, nil) when still blank.
func (s *SrcStore) GetSrcCheck(ctx context.Context, taskID, assetID int64, key string) (*SrcCheck, error) {
	if s == nil || s.db == nil || taskID <= 0 || assetID <= 0 || key == "" {
		return nil, nil
	}
	row, err := scanSrcCheck(s.db.QueryRowContext(ctx,
		`SELECT `+srcCheckCols+` FROM src_checks c LEFT JOIN assets a ON a.id=c.asset_id
WHERE c.task_id=$1 AND c.asset_id=$2 AND c.check_key=$3`, taskID, assetID, key))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

// ListSrcChecks returns every closure row of a task.
func (s *SrcStore) ListSrcChecks(ctx context.Context, taskID int64) ([]SrcCheck, error) {
	if s == nil || s.db == nil || taskID <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+srcCheckCols+` FROM src_checks c LEFT JOIN assets a ON a.id=c.asset_id
WHERE c.task_id=$1 ORDER BY c.asset_id, c.check_key`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcCheck{}
	for rows.Next() {
		c, err := scanSrcCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// SrcChecksByAsset groups a task's closure rows by asset id then check key.
func (s *SrcStore) SrcChecksByAsset(ctx context.Context, taskID int64) (map[int64]map[string]SrcCheck, error) {
	rows, err := s.ListSrcChecks(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]map[string]SrcCheck, len(rows))
	for _, c := range rows {
		if out[c.AssetID] == nil {
			out[c.AssetID] = map[string]SrcCheck{}
		}
		out[c.AssetID][c.CheckKey] = c
	}
	return out, nil
}
