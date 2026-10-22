package db

// SRC 平台项目（P3）：src_programs 的读写 + 任务绑定 + 看板聚合。
//
// Rules / SeverityMap 在这里保持 JSON 原样（db 不认识 srcmode 的类型，避免循环依赖）；
// 语义校验与编译在 srcmode / server 完成。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SrcProgram is one row of src_programs.
type SrcProgram struct {
	ID          int64           `json:"id"`
	CompanyID   *int64          `json:"company_id,omitempty"`
	Name        string          `json:"name"`
	Platform    string          `json:"platform"`
	ScopeNote   string          `json:"scope_note"`
	Rules       json.RawMessage `json:"rules"`
	RateLimit   int             `json:"rate_limit"`
	TimeWindow  string          `json:"time_window"`
	SeverityMap json.RawMessage `json:"severity_map"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// SrcProgramInput is the settable part of a program.
type SrcProgramInput struct {
	CompanyID   *int64          `json:"company_id,omitempty"`
	Name        string          `json:"name"`
	Platform    string          `json:"platform"`
	ScopeNote   string          `json:"scope_note"`
	Rules       json.RawMessage `json:"rules"`
	RateLimit   int             `json:"rate_limit"`
	TimeWindow  string          `json:"time_window"`
	SeverityMap json.RawMessage `json:"severity_map"`
}

const srcProgramCols = `id, company_id, name, platform, scope_note, COALESCE(rules,'{}'::jsonb),
rate_limit, time_window, COALESCE(severity_map,'{}'::jsonb), created_at, updated_at`

func scanSrcProgram(sc interface{ Scan(...any) error }) (*SrcProgram, error) {
	var p SrcProgram
	var rules, sev []byte
	if err := sc.Scan(&p.ID, &p.CompanyID, &p.Name, &p.Platform, &p.ScopeNote, &rules,
		&p.RateLimit, &p.TimeWindow, &sev, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.Rules = json.RawMessage(rules)
	p.SeverityMap = json.RawMessage(sev)
	return &p, nil
}

func programJSON(raw json.RawMessage, fallback string) string {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return fallback
	}
	return string(raw)
}

// CreateSrcProgram inserts one program. Name is unique (case-insensitive).
func (s *SrcStore) CreateSrcProgram(ctx context.Context, in SrcProgramInput) (*SrcProgram, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("src store unavailable")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("项目名不能为空")
	}
	row, err := scanSrcProgram(s.db.QueryRowContext(ctx, `
INSERT INTO src_programs(company_id, name, platform, scope_note, rules, rate_limit, time_window, severity_map)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING `+srcProgramCols,
		in.CompanyID, name, strings.TrimSpace(in.Platform), strings.TrimSpace(in.ScopeNote),
		programJSON(in.Rules, "{}"), in.RateLimit, strings.TrimSpace(in.TimeWindow),
		programJSON(in.SeverityMap, "{}")))
	if err != nil {
		if strings.Contains(err.Error(), "uq_src_programs_name") {
			return nil, fmt.Errorf("项目名「%s」已存在", name)
		}
		return nil, err
	}
	return row, nil
}

// UpdateSrcProgram replaces the settable fields of one program.
func (s *SrcStore) UpdateSrcProgram(ctx context.Context, id int64, in SrcProgramInput) (*SrcProgram, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("src store unavailable")
	}
	if id <= 0 {
		return nil, fmt.Errorf("项目 id 无效")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("项目名不能为空")
	}
	row, err := scanSrcProgram(s.db.QueryRowContext(ctx, `
UPDATE src_programs SET
    company_id=$2, name=$3, platform=$4, scope_note=$5, rules=$6,
    rate_limit=$7, time_window=$8, severity_map=$9
WHERE id=$1
RETURNING `+srcProgramCols,
		id, in.CompanyID, name, strings.TrimSpace(in.Platform), strings.TrimSpace(in.ScopeNote),
		programJSON(in.Rules, "{}"), in.RateLimit, strings.TrimSpace(in.TimeWindow),
		programJSON(in.SeverityMap, "{}")))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		if strings.Contains(err.Error(), "uq_src_programs_name") {
			return nil, fmt.Errorf("项目名「%s」已存在", name)
		}
		return nil, err
	}
	return row, nil
}

// GetSrcProgram returns one program (nil when missing).
func (s *SrcStore) GetSrcProgram(ctx context.Context, id int64) (*SrcProgram, error) {
	if s == nil || s.db == nil || id <= 0 {
		return nil, nil
	}
	p, err := scanSrcProgram(s.db.QueryRowContext(ctx,
		`SELECT `+srcProgramCols+` FROM src_programs WHERE id=$1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ListSrcPrograms returns all programs, platform then name.
func (s *SrcStore) ListSrcPrograms(ctx context.Context) ([]SrcProgram, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+srcProgramCols+` FROM src_programs ORDER BY platform, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcProgram{}
	for rows.Next() {
		p, err := scanSrcProgram(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// DeleteSrcProgram removes a program; tasks referencing it keep running (FK SET NULL).
func (s *SrcStore) DeleteSrcProgram(ctx context.Context, id int64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("src store unavailable")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM src_programs WHERE id=$1`, id)
	return err
}

// SrcProgramForTask returns the program bound to a task (nil when unbound).
func (s *SrcStore) SrcProgramForTask(ctx context.Context, taskID int64) (*SrcProgram, error) {
	if s == nil || s.db == nil || taskID <= 0 {
		return nil, nil
	}
	p, err := scanSrcProgram(s.db.QueryRowContext(ctx, `
SELECT `+srcProgramCols+` FROM src_programs
WHERE id = (SELECT src_program_id FROM tasks WHERE id=$1)`, taskID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// SrcStats 是 SRC 看板的聚合数字（一次查询一组，避免 N 次往返）。
type SrcStats struct {
	Tasks            int            `json:"tasks"`         // mode='src' 的任务数
	Programs         int            `json:"programs"`      // 平台项目数
	Submissions      map[string]int `json:"submissions"`   // 台账按状态
	BySeverity       map[string]int `json:"by_severity"`   // 台账按内部等级
	FindingsMedium   int            `json:"findings_medium_plus"`
	FindingsReady    int            `json:"findings_report_ready"`
	SubmissionsTotal int            `json:"submissions_total"`
}

// SrcDashboardStats aggregates the numbers the SRC board shows.
func (s *SrcStore) SrcDashboardStats(ctx context.Context) (SrcStats, error) {
	out := SrcStats{Submissions: map[string]int{}, BySeverity: map[string]int{}}
	if s == nil || s.db == nil {
		return out, nil
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM tasks WHERE mode='src' AND deleted_at IS NULL`).Scan(&out.Tasks); err != nil {
		return out, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM src_programs`).Scan(&out.Programs); err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT status, count(*) FROM src_submissions GROUP BY status`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			return out, err
		}
		out.Submissions[status] = n
		out.SubmissionsTotal += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	srows, err := s.db.QueryContext(ctx,
		`SELECT severity, count(*) FROM src_submissions GROUP BY severity`)
	if err != nil {
		return out, err
	}
	for srows.Next() {
		var sev string
		var n int
		if err := srows.Scan(&sev, &n); err != nil {
			srows.Close()
			return out, err
		}
		out.BySeverity[sev] = n
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return out, err
	}
	if err := s.db.QueryRowContext(ctx, `
SELECT
  count(*) FILTER (WHERE f.severity IN ('critical','high','medium')),
  count(*) FILTER (WHERE f.severity IN ('critical','high','medium') AND COALESCE(m.report_ready,false))
FROM findings f
JOIN tasks t ON t.id = f.task_id AND t.mode='src' AND t.deleted_at IS NULL
LEFT JOIN src_finding_meta m ON m.finding_id = f.id`).Scan(&out.FindingsMedium, &out.FindingsReady); err != nil {
		return out, err
	}
	return out, nil
}
