package db

// 资产级 token 预算的读取面：llm_usage 按 intent 的主资产归因（AssetID），
// 这里只做 taskID(int64) → llm_usage.task_id(text) 的转换与薄封装。

import (
	"context"
	"strconv"
)

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
