package db

import "context"

// Human confirmation and a fresh independent reproduction are distinct from a
// model's confidence label. Closed conclusions never enter confirmed reports.
func (d *DB) ConfirmedReportNodes(ctx context.Context, taskID int64) (map[int64]bool, error) {
	rows, err := d.QueryContext(ctx, `SELECT f.node_id FROM findings f WHERE f.task_id=$1 AND f.node_id IS NOT NULL
 AND f.status NOT IN ('false_positive','duplicate','retracted','ruled_out','open_proof_gap','ignored','risk_accepted','fixed','resolved')
 AND (f.status='confirmed' OR EXISTS(SELECT 1 FROM finding_retests r WHERE r.finding_id=f.id AND r.id=(SELECT max(r2.id) FROM finding_retests r2 WHERE r2.finding_id=f.id)
 AND r.status='completed' AND r.verdict='reproduced' AND r.evidence_version=f.evidence_version AND r.finished_at>now()-interval '7 days'))`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}
