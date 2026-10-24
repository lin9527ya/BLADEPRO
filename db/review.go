package db

// 人工复核队列：把散落在覆盖格/复测/台账里的“需要人看一眼”的项聚成一张表。
// 这里只做只读聚合与人工裁决留痕；具体动作（确认/打回/重测）由 server 调既有
// 状态机执行，避免再造一套业务规则。

import (
	"context"
	"database/sql"
	"time"
)

// ReviewItem is one row of the human review queue.
type ReviewItem struct {
	Kind         string    `json:"kind"` // report_missing | retest_inconclusive | dedupe_pending | budget_stop | suspicious_na
	RefKey       string    `json:"ref_key"`
	TaskID       int64     `json:"task_id,omitempty"`
	TaskLabel    string    `json:"task_label,omitempty"`
	FindingID    int64     `json:"finding_id,omitempty"`
	SubmissionID int64     `json:"submission_id,omitempty"`
	RetestID     int64     `json:"retest_id,omitempty"`
	AssetID      int64     `json:"asset_id,omitempty"`
	AssetLabel   string    `json:"asset_label,omitempty"`
	Area         string    `json:"area,omitempty"`
	Item         string    `json:"item,omitempty"`
	State        string    `json:"state,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	Title        string    `json:"title,omitempty"`
	Severity     string    `json:"severity,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ReviewQueue aggregates pending human-review items. limit caps the whole union.
func (d *DB) ReviewQueue(ctx context.Context, limit, staleDays int) ([]ReviewItem, error) {
	if d == nil {
		return []ReviewItem{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if staleDays <= 0 {
		staleDays = 7
	}
	if staleDays > 90 {
		staleDays = 90
	}
	rows, err := d.QueryContext(ctx, `
WITH items AS (
  -- 1) medium+ 发现没有齐备可提交件（SRC 交付门）
  SELECT 'report_missing'::text AS kind,
         'report_missing:' || f.id AS ref_key,
         COALESCE(f.task_id,0) AS task_id,
         COALESCE(NULLIF(t.name,''), t.description, '') AS task_label,
         f.id AS finding_id,
         COALESCE((SELECT s.id FROM src_submissions s WHERE s.finding_id=f.id ORDER BY s.id DESC LIMIT 1),0) AS submission_id,
         0::bigint AS retest_id, 0::bigint AS asset_id,
         ''::text AS asset_label, ''::text AS area, ''::text AS item, ''::text AS state,
         COALESCE(f.summary,'') AS reason,
         COALESCE(NULLIF(f.name,''), f.vulnclass) AS title,
         f.severity AS severity,
         f.created_at AS updated_at
  FROM findings f
  LEFT JOIN src_finding_meta m ON m.finding_id=f.id
  LEFT JOIN tasks t ON t.id=f.task_id
  WHERE lower(f.severity) IN ('critical','high','medium')
    AND COALESCE(m.report_ready,false)=false
    AND (t.mode='src' OR m.finding_id IS NOT NULL)
    AND COALESCE(f.status,'pending') NOT IN ('false_positive','retracted','ruled_out','duplicate','ignored','fixed','resolved')

  UNION ALL
  -- 2) 最近一次已完成复测是 inconclusive
  SELECT 'retest_inconclusive', 'retest_inconclusive:' || r.id,
         COALESCE(f.task_id,0), COALESCE(NULLIF(t.name,''), t.description, ''),
         f.id, 0, r.id, 0, '', '', '', '',
         COALESCE(NULLIF(r.summary,''), NULLIF(r.evidence,''), '复测结论 inconclusive'),
         COALESCE(NULLIF(f.name,''), f.vulnclass), f.severity,
         COALESCE(r.finished_at, r.created_at)
  FROM finding_retests r
  JOIN findings f ON f.id=r.finding_id
  LEFT JOIN tasks t ON t.id=f.task_id
  WHERE r.status='completed' AND r.verdict='inconclusive'
    AND r.id = (SELECT max(r2.id) FROM finding_retests r2
                WHERE r2.finding_id=r.finding_id AND r2.status='completed' AND r2.verdict<>'')
    AND COALESCE(f.status,'pending') NOT IN ('false_positive','retracted','ruled_out','duplicate','ignored','fixed','resolved')

  UNION ALL
  -- 3) 疑似重复已登记 dedupe_of，但台账还没定案为 duplicate
  SELECT 'dedupe_pending', 'dedupe_pending:' || f.id || ':' || m.dedupe_of,
         COALESCE(f.task_id,0), COALESCE(NULLIF(t.name,''), t.description, ''),
         f.id,
         COALESCE((SELECT s.id FROM src_submissions s WHERE s.finding_id=f.id ORDER BY s.id DESC LIMIT 1),0),
         0, 0, '', '', '', '',
         'dedupe_of=#' || m.dedupe_of,
         COALESCE(NULLIF(f.name,''), f.vulnclass), f.severity,
         COALESCE(m.updated_at, f.created_at)
  FROM src_finding_meta m
  JOIN findings f ON f.id=m.finding_id
  LEFT JOIN tasks t ON t.id=f.task_id
  WHERE m.dedupe_of > 0
    AND COALESCE(f.status,'pending') NOT IN ('false_positive','retracted','ruled_out','duplicate','ignored','fixed','resolved')
    AND NOT EXISTS (SELECT 1 FROM src_submissions s WHERE s.finding_id=f.id AND s.status='duplicate')

  UNION ALL
  -- 4) 预算/时间用尽没测完的覆盖格
  SELECT 'budget_stop',
         'budget_stop:' || c.task_id || ':' || c.asset_id || ':' || c.area || ':' || c.item || ':' ||
         (extract(epoch FROM c.updated_at)::bigint)::text,
         c.task_id, COALESCE(NULLIF(t.name,''), t.description, ''),
         0, 0, 0, c.asset_id,
         COALESCE(a.url, a.domain, a.ip, a.app_name, a.root_domain, ''),
         c.area, c.item, c.state, c.reason, '', '', c.updated_at
  FROM src_cells c
  JOIN tasks t ON t.id=c.task_id
  LEFT JOIN assets a ON a.id=c.asset_id
  WHERE c.state='budget_stop' AND t.mode='src' AND t.deleted_at IS NULL
    AND t.status NOT IN ('done','failed','timeout')

  UNION ALL
  -- 5) 四件套全部 N/A 的资产（现有 suspicious_na 口径）
  SELECT 'suspicious_na',
         'suspicious_na:' || c.task_id || ':' || c.asset_id || ':' ||
         (extract(epoch FROM max(c.updated_at))::bigint)::text,
         c.task_id, COALESCE(NULLIF(t.name,''), t.description, ''),
         0, 0, 0, c.asset_id,
         COALESCE(a.url, a.domain, a.ip, a.app_name, a.root_domain, ''),
         '', '*', 'na', '四件套（注入/SSRF/XSS/RCE）全部 N/A', '', '', max(c.updated_at)
  FROM src_cells c
  JOIN tasks t ON t.id=c.task_id
  LEFT JOIN assets a ON a.id=c.asset_id
  WHERE t.mode='src' AND t.deleted_at IS NULL AND t.status NOT IN ('done','failed','timeout')
  GROUP BY c.task_id, c.asset_id, t.name, t.description, a.url, a.domain, a.ip, a.app_name, a.root_domain
  HAVING count(*) FILTER (WHERE c.area IN ('injection','ssrf','xss','rce') AND c.state='na' AND c.item='*') = 4

  UNION ALL
  -- 6) ready 超过 SLA 未提交
  SELECT 'ready_stale',
         'ready_stale:' || s.id || ':' || (extract(epoch FROM s.updated_at)::bigint)::text,
         s.task_id, COALESCE(NULLIF(t.name,''), t.description, ''),
         s.finding_id, s.id, 0, 0, '', '', '', '',
         'ready 超过 SLA 未提交',
         COALESCE(NULLIF(s.title,''), f.name, f.vulnclass),
         COALESCE(NULLIF(s.severity,''), f.severity),
         s.updated_at
  FROM src_submissions s
  JOIN findings f ON f.id=s.finding_id
  LEFT JOIN tasks t ON t.id=s.task_id
  WHERE s.status='ready' AND s.updated_at < now() - ($2::int * interval '1 day')

  UNION ALL
  -- 7) submitted 超过 SLA 无平台结论
  SELECT 'submitted_stale',
         'submitted_stale:' || s.id || ':' || (extract(epoch FROM COALESCE(s.submitted_at,s.updated_at))::bigint)::text,
         s.task_id, COALESCE(NULLIF(t.name,''), t.description, ''),
         s.finding_id, s.id, 0, 0, '', '', '', '',
         'submitted 超过 SLA 无平台结论',
         COALESCE(NULLIF(s.title,''), f.name, f.vulnclass),
         COALESCE(NULLIF(s.severity,''), f.severity),
         COALESCE(s.submitted_at, s.updated_at)
  FROM src_submissions s
  JOIN findings f ON f.id=s.finding_id
  LEFT JOIN tasks t ON t.id=s.task_id
  WHERE s.status='submitted' AND COALESCE(s.submitted_at,s.updated_at) < now() - ($2::int * interval '1 day')

  UNION ALL
  -- 8) accepted 超过 SLA 未到账
  SELECT 'accepted_unpaid',
         'accepted_unpaid:' || s.id || ':' || (extract(epoch FROM COALESCE(s.verdict_at,s.updated_at))::bigint)::text,
         s.task_id, COALESCE(NULLIF(t.name,''), t.description, ''),
         s.finding_id, s.id, 0, 0, '', '', '', '',
         'accepted 超过 SLA 未到账',
         COALESCE(NULLIF(s.title,''), f.name, f.vulnclass),
         COALESCE(NULLIF(s.severity,''), f.severity),
         COALESCE(s.verdict_at, s.updated_at)
  FROM src_submissions s
  JOIN findings f ON f.id=s.finding_id
  LEFT JOIN tasks t ON t.id=s.task_id
  WHERE s.status='accepted' AND COALESCE(s.verdict_at,s.updated_at) < now() - ($2::int * interval '1 day')
)
SELECT kind, ref_key, task_id, task_label, finding_id, submission_id, retest_id,
       asset_id, asset_label, area, item, state, reason, title, severity, updated_at
FROM items
ORDER BY updated_at DESC
LIMIT $1`, limit, staleDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewItem{}
	for rows.Next() {
		var it ReviewItem
		if err := rows.Scan(&it.Kind, &it.RefKey, &it.TaskID, &it.TaskLabel, &it.FindingID,
			&it.SubmissionID, &it.RetestID, &it.AssetID, &it.AssetLabel, &it.Area, &it.Item,
			&it.State, &it.Reason, &it.Title, &it.Severity, &it.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// RecordReviewDecision persists a human decision so an acknowledged item leaves the
// queue. Keyed by (kind, ref_key); a changed underlying row gets a new ref_key.
func (d *DB) RecordReviewDecision(ctx context.Context, kind, refKey, action, note string) error {
	if d == nil {
		return nil
	}
	_, err := d.ExecContext(ctx, `
INSERT INTO review_decisions(kind, ref_key, action, note)
VALUES ($1,$2,$3,$4)
ON CONFLICT (kind, ref_key) DO UPDATE SET action=EXCLUDED.action, note=EXCLUDED.note, created_at=now()`,
		kind, refKey, action, note)
	return err
}

// ReviewDecisions returns kind|ref_key → action for everything already decided.
func (d *DB) ReviewDecisions(ctx context.Context) (map[string]string, error) {
	if d == nil {
		return map[string]string{}, nil
	}
	rows, err := d.QueryContext(ctx, `SELECT kind, ref_key, action FROM review_decisions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var kind, ref, action string
		if err := rows.Scan(&kind, &ref, &action); err != nil {
			return nil, err
		}
		out[kind+"|"+ref] = action
	}
	return out, rows.Err()
}

// GetReviewDecision returns the stored action for one item ("" when undecided).
func (d *DB) GetReviewDecision(ctx context.Context, kind, refKey string) (string, error) {
	if d == nil {
		return "", nil
	}
	var action string
	err := d.QueryRowContext(ctx, `SELECT action FROM review_decisions WHERE kind=$1 AND ref_key=$2`, kind, refKey).Scan(&action)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return action, err
}
