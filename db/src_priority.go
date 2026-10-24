package db

import "context"

// AutoTaskPriority derives a queue priority from the asset tiers in the task's
// scope. It is only used when the caller did not set an explicit priority:
// campaign fan-out uses the same weights, so single tasks and shards rank
// consistently. Unclassified assets score 0.
func (s *SrcStore) AutoTaskPriority(ctx context.Context, taskID int64) (int, error) {
	if s == nil || s.db == nil || taskID <= 0 {
		return 0, nil
	}
	var score int
	err := s.db.QueryRowContext(ctx, `WITH `+srcTargetCTE+`
SELECT COALESCE(SUM(CASE tier
  WHEN 'primary' THEN 12
  WHEN 'api' THEN 8
  WHEN 'admin' THEN 8
  WHEN 'mobile' THEN 4
  WHEN 'third_party' THEN -3
  ELSE 0 END),0) FROM target`, taskID).Scan(&score)
	if err != nil {
		return 0, err
	}
	if score < -1000 {
		score = -1000
	}
	if score > 1000 {
		score = 1000
	}
	return score, nil
}
