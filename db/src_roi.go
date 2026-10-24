package db

import "context"

// SrcClassStatus 是一条"漏洞类型 × 台账状态"的计数（ROI 先验用）。
type SrcClassStatus struct {
	VulnClass string
	Status    string
	Count     int
}

// ListSrcClassStatusCounts 聚合某项目（programID=0 表示全局）的台账结果。
// 只做原始计数；类型归一与风险面映射在 server 侧（依赖 report 的受控词表）。
func (s *SrcStore) ListSrcClassStatusCounts(ctx context.Context, programID int64) ([]SrcClassStatus, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT COALESCE(f.vulnclass,''), s.status, count(*)
FROM src_submissions s
JOIN findings f ON f.id = s.finding_id
WHERE ($1 = 0 OR s.program_id = $1)
GROUP BY 1, 2`, programID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcClassStatus{}
	for rows.Next() {
		var c SrcClassStatus
		if err := rows.Scan(&c.VulnClass, &c.Status, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
