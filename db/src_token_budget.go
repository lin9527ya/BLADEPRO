package db

// 资产级 token 预算的读取面：llm_usage 按 intent 的主资产归因（AssetID），
// 这里只做 taskID(int64) → llm_usage.task_id(text) 的转换与薄封装。

import (
	"context"
	"database/sql"
	"strconv"
	"time"
)

// AssetTimeUsage is one asset's wall-clock spend inside a task: the timestamp of
// its first attributed LLM call and how long ago that was.
type AssetTimeUsage struct {
	AssetID     int64     `json:"asset_id"`
	StartedAt   time.Time `json:"started_at"`
	UsedMinutes int       `json:"used_minutes"`
}

// TokenUsedByAsset sums the token spend attributed to one asset in this task.
func (s *SrcStore) TokenUsedByAsset(ctx context.Context, taskID, assetID int64) (int64, error) {
	if s == nil || s.db == nil || taskID <= 0 || assetID <= 0 {
		return 0, nil
	}
	var used int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(input_tokens+output_tokens),0)
FROM llm_usage WHERE task_id=$1 AND asset_id=$2`, strconv.FormatInt(taskID, 10), assetID).Scan(&used)
	return used, err
}

// AssetTokenUsageByTask returns per-asset spend for one task (asset_id asc).
func (s *SrcStore) AssetTokenUsageByTask(ctx context.Context, taskID int64) ([]AssetTokenUsage, error) {
	if s == nil || s.db == nil || taskID <= 0 {
		return []AssetTokenUsage{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT asset_id, COALESCE(SUM(input_tokens+output_tokens),0)
FROM llm_usage WHERE task_id=$1 AND asset_id > 0
GROUP BY asset_id ORDER BY asset_id`, strconv.FormatInt(taskID, 10))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssetTokenUsage{}
	for rows.Next() {
		var u AssetTokenUsage
		if err := rows.Scan(&u.AssetID, &u.Tokens); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// AssetsOverTokenBudget returns assets whose attributed spend reached budget.
func (s *SrcStore) AssetsOverTokenBudget(ctx context.Context, taskID int64, budget int64) ([]AssetTokenUsage, error) {
	if s == nil || s.db == nil || taskID <= 0 || budget <= 0 {
		return []AssetTokenUsage{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT asset_id, COALESCE(SUM(input_tokens+output_tokens),0) AS used
FROM llm_usage WHERE task_id=$1 AND asset_id > 0
GROUP BY asset_id HAVING COALESCE(SUM(input_tokens+output_tokens),0) >= $2
ORDER BY used DESC, asset_id`, strconv.FormatInt(taskID, 10), budget)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssetTokenUsage{}
	for rows.Next() {
		var u AssetTokenUsage
		if err := rows.Scan(&u.AssetID, &u.Tokens); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// AssetsOverTimeBudget returns the assets whose first attributed call is older
// than the per-asset wall-clock budget. An asset that has not produced any LLM
// usage yet has no start time and is never considered exhausted.
func (s *SrcStore) AssetsOverTimeBudget(ctx context.Context, taskID int64, minutes int) ([]AssetTimeUsage, error) {
	if s == nil || s.db == nil || taskID <= 0 || minutes <= 0 {
		return []AssetTimeUsage{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT asset_id, MIN(ts)
FROM llm_usage WHERE task_id=$1 AND asset_id > 0
GROUP BY asset_id
HAVING MIN(ts) <= now() - ($2::int * interval '1 minute')
ORDER BY MIN(ts), asset_id`, strconv.FormatInt(taskID, 10), minutes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now()
	out := []AssetTimeUsage{}
	for rows.Next() {
		var u AssetTimeUsage
		if err := rows.Scan(&u.AssetID, &u.StartedAt); err != nil {
			return nil, err
		}
		u.UsedMinutes = int(now.Sub(u.StartedAt).Minutes())
		if u.UsedMinutes < minutes {
			u.UsedMinutes = minutes
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// AssetStartedAt returns when an asset first produced LLM usage in this task
// (zero time when it has none yet). The asset time budget measures from here:
// an asset that was never worked on has no clock running.
func (s *SrcStore) AssetStartedAt(ctx context.Context, taskID, assetID int64) (time.Time, error) {
	if s == nil || s.db == nil || taskID <= 0 || assetID <= 0 {
		return time.Time{}, nil
	}
	var started sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT MIN(ts) FROM llm_usage WHERE task_id=$1 AND asset_id=$2`,
		strconv.FormatInt(taskID, 10), assetID).Scan(&started)
	if err != nil {
		return time.Time{}, err
	}
	if !started.Valid {
		return time.Time{}, nil
	}
	return started.Time, nil
}

// AssetCostUsage is one asset's dollar spend inside a task.
type AssetCostUsage struct {
	AssetID int64   `json:"asset_id"`
	CostUSD float64 `json:"cost_usd"`
}

// AssetsOverCostBudget returns assets whose attributed cost reached the budget.
func (s *SrcStore) AssetsOverCostBudget(ctx context.Context, taskID int64, budget float64) ([]AssetCostUsage, error) {
	if s == nil || s.db == nil || taskID <= 0 || budget <= 0 {
		return []AssetCostUsage{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT asset_id, COALESCE(SUM(cost_usd),0)
FROM llm_usage WHERE task_id=$1 AND asset_id > 0
GROUP BY asset_id HAVING COALESCE(SUM(cost_usd),0) >= $2
ORDER BY 2 DESC, asset_id`, strconv.FormatInt(taskID, 10), budget)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssetCostUsage{}
	for rows.Next() {
		var u AssetCostUsage
		if err := rows.Scan(&u.AssetID, &u.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// AssetCostUsed returns one asset's attributed cost in the task.
func (s *SrcStore) AssetCostUsed(ctx context.Context, taskID, assetID int64) (float64, error) {
	if s == nil || s.db == nil || taskID <= 0 || assetID <= 0 {
		return 0, nil
	}
	var cost float64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_usd),0)
FROM llm_usage WHERE task_id=$1 AND asset_id=$2`, strconv.FormatInt(taskID, 10), assetID).Scan(&cost)
	return cost, err
}
