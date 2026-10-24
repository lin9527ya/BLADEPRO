package db

// 台账状态历史。写入侧在 Prepare/Update 里完成，读取侧给发现详情的时间线。

import (
	"context"
	"time"
)

// SrcSubmissionEvent is one ledger transition/field change.
type SrcSubmissionEvent struct {
	ID            int64     `json:"id"`
	SubmissionID  int64     `json:"submission_id"`
	FromStatus    string    `json:"from_status,omitempty"`
	ToStatus      string    `json:"to_status"`
	ExternalID    string    `json:"external_id,omitempty"`
	PlatformLevel string    `json:"platform_level,omitempty"`
	Reward        string    `json:"reward,omitempty"`
	VerdictReason string    `json:"verdict_reason,omitempty"`
	Note          string    `json:"note,omitempty"`
	Actor         string    `json:"actor,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

const srcSubmissionEventCols = `id, submission_id, from_status, to_status, external_id,
platform_level, reward, verdict_reason, note, actor, created_at`

// ListSrcSubmissionEvents returns one submission's history, oldest first.
func (s *SrcStore) ListSrcSubmissionEvents(ctx context.Context, submissionID int64) ([]SrcSubmissionEvent, error) {
	if s == nil || s.db == nil || submissionID <= 0 {
		return []SrcSubmissionEvent{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+srcSubmissionEventCols+`
FROM src_submission_events WHERE submission_id=$1 ORDER BY id`, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcSubmissionEvent{}
	for rows.Next() {
		var e SrcSubmissionEvent
		if err := rows.Scan(&e.ID, &e.SubmissionID, &e.FromStatus, &e.ToStatus, &e.ExternalID,
			&e.PlatformLevel, &e.Reward, &e.VerdictReason, &e.Note, &e.Actor, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
