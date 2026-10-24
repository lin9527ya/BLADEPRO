package db

// 重复组：把同指纹（强指纹优先，没有强指纹时用降级指纹）的发现聚成一组，
// 让“这几条其实是同一个洞”在台账上看得见。它只做聚合展示，不自动写 dedupe_of
// ——是否合并仍由人在 src_dedupe / 复核队列里定案。

import (
	"context"
	"sort"
)

// SrcDuplicateGroupFinding is one member of a duplicate group.
type SrcDuplicateGroupFinding struct {
	FindingID    int64  `json:"finding_id"`
	TaskID       int64  `json:"task_id"`
	TaskName     string `json:"task_name,omitempty"`
	Title        string `json:"title"`
	Severity     string `json:"severity"`
	Status       string `json:"status"`
	SubmissionSt string `json:"submission_status,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// SrcDuplicateGroup is one fingerprint cluster (>=2 findings).
type SrcDuplicateGroup struct {
	Fingerprint string                     `json:"fingerprint"`
	Strong      bool                       `json:"strong"` // true = 由绑定流量算出的强指纹
	Count       int                        `json:"count"`
	Findings    []SrcDuplicateGroupFinding `json:"findings"`
}

// SrcDuplicateGroups lists duplicate groups, optionally restricted to one task.
// Groups are ordered by member count DESC then fingerprint; at most limit groups
// and maxPerGroup findings each are returned.
func (s *SrcStore) SrcDuplicateGroups(ctx context.Context, taskID int64, limit, maxPerGroup int) ([]SrcDuplicateGroup, error) {
	if s == nil || s.db == nil {
		return []SrcDuplicateGroup{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if maxPerGroup <= 0 || maxPerGroup > 50 {
		maxPerGroup = 10
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT COALESCE(m.fingerprint,''), COALESCE(m.degraded_fingerprint,''),
       f.id, COALESCE(f.task_id,0), COALESCE(t.name,''),
       COALESCE(NULLIF(f.name,''), f.vulnclass), COALESCE(f.severity,''), COALESCE(f.status,''),
       COALESCE(s.status,''), to_char(f.created_at,'YYYY-MM-DD HH24:MI')
FROM src_finding_meta m
JOIN findings f ON f.id = m.finding_id
LEFT JOIN tasks t ON t.id = f.task_id
LEFT JOIN src_submissions s ON s.finding_id = f.id AND s.platform = ''
WHERE ($1::bigint = 0 OR f.task_id = $1)
  AND (COALESCE(m.fingerprint,'') <> '' OR COALESCE(m.degraded_fingerprint,'') <> '')
ORDER BY f.id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type bucket struct {
		strong bool
		items  []SrcDuplicateGroupFinding
	}
	groups := map[string]*bucket{}
	for rows.Next() {
		var fp, dfp string
		var it SrcDuplicateGroupFinding
		if err := rows.Scan(&fp, &dfp, &it.FindingID, &it.TaskID, &it.TaskName,
			&it.Title, &it.Severity, &it.Status, &it.SubmissionSt, &it.CreatedAt); err != nil {
			return nil, err
		}
		key, strong := fp, true
		if key == "" {
			key, strong = dfp, false
		}
		b := groups[key]
		if b == nil {
			b = &bucket{strong: strong}
			groups[key] = b
		}
		b.items = append(b.items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]SrcDuplicateGroup, 0, len(groups))
	for key, b := range groups {
		if len(b.items) < 2 {
			continue
		}
		items := b.items
		if len(items) > maxPerGroup {
			items = items[:maxPerGroup]
		}
		out = append(out, SrcDuplicateGroup{
			Fingerprint: key, Strong: b.strong, Count: len(b.items), Findings: items,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Fingerprint < out[j].Fingerprint
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
