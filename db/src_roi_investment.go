package db

import "context"

// SrcAreaInvestment is real testing spend per risk area: only measured cells
// (tested_found/tested_clear), excluding NA, budget_stop and inherited rows.
type SrcAreaInvestment struct {
	Area     string
	Measured int
	Attempts int
}

// ListSrcAreaInvestment aggregates actual tested cells/attempts for ROI
// denominators. Ledger rows alone only count submitted findings and make
// unproductive directions look free.
func (s *SrcStore) ListSrcAreaInvestment(ctx context.Context, programID int64) ([]SrcAreaInvestment, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT c.area,
       count(*) FILTER (WHERE c.state IN ('tested_found','tested_clear')),
       COALESCE(sum(c.attempts),0)
FROM src_cells c
JOIN tasks t ON t.id = c.task_id
WHERE t.mode='src' AND t.deleted_at IS NULL
  AND ($1 = 0 OR t.src_program_id = $1)
  AND c.state IN ('tested_found','tested_clear')
  AND COALESCE(c.inherited_from_task_id,0) = 0
GROUP BY c.area`, programID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcAreaInvestment{}
	for rows.Next() {
		var a SrcAreaInvestment
		if err := rows.Scan(&a.Area, &a.Measured, &a.Attempts); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
