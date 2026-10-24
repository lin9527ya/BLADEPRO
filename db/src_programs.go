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
	// RetentionDays 是该 program 的任务产物/日志保留天数（0=跟随全局）。
	RetentionDays int       `json:"retention_days"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// SrcProgramInput is the settable part of a program.
type SrcProgramInput struct {
	CompanyID     *int64          `json:"company_id,omitempty"`
	Name          string          `json:"name"`
	Platform      string          `json:"platform"`
	ScopeNote     string          `json:"scope_note"`
	Rules         json.RawMessage `json:"rules"`
	RateLimit     int             `json:"rate_limit"`
	TimeWindow    string          `json:"time_window"`
	SeverityMap   json.RawMessage `json:"severity_map"`
	RetentionDays int             `json:"retention_days"`
}

const srcProgramCols = `id, company_id, name, platform, scope_note, COALESCE(rules,'{}'::jsonb),
rate_limit, time_window, COALESCE(severity_map,'{}'::jsonb), retention_days, created_at, updated_at`

func scanSrcProgram(sc interface{ Scan(...any) error }) (*SrcProgram, error) {
	var p SrcProgram
	var rules, sev []byte
	if err := sc.Scan(&p.ID, &p.CompanyID, &p.Name, &p.Platform, &p.ScopeNote, &rules,
		&p.RateLimit, &p.TimeWindow, &sev, &p.RetentionDays, &p.CreatedAt, &p.UpdatedAt); err != nil {
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
	if in.RetentionDays < 0 {
		return nil, fmt.Errorf("保留天数不能为负（0 = 跟随全局）")
	}
	row, err := scanSrcProgram(s.db.QueryRowContext(ctx, `
INSERT INTO src_programs(company_id, name, platform, scope_note, rules, rate_limit, time_window, severity_map, retention_days)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
RETURNING `+srcProgramCols,
		in.CompanyID, name, strings.TrimSpace(in.Platform), strings.TrimSpace(in.ScopeNote),
		programJSON(in.Rules, "{}"), in.RateLimit, strings.TrimSpace(in.TimeWindow),
		programJSON(in.SeverityMap, "{}"), in.RetentionDays))
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
	if in.RetentionDays < 0 {
		return nil, fmt.Errorf("保留天数不能为负（0 = 跟随全局）")
	}
	row, err := scanSrcProgram(s.db.QueryRowContext(ctx, `
UPDATE src_programs SET
    company_id=$2, name=$3, platform=$4, scope_note=$5, rules=$6,
    rate_limit=$7, time_window=$8, severity_map=$9, retention_days=$10
WHERE id=$1
RETURNING `+srcProgramCols,
		id, in.CompanyID, name, strings.TrimSpace(in.Platform), strings.TrimSpace(in.ScopeNote),
		programJSON(in.Rules, "{}"), in.RateLimit, strings.TrimSpace(in.TimeWindow),
		programJSON(in.SeverityMap, "{}"), in.RetentionDays))
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
	Tasks            int            `json:"tasks"`       // mode='src' 的任务数
	Programs         int            `json:"programs"`    // 平台项目数
	Submissions      map[string]int `json:"submissions"` // 台账按状态
	BySeverity       map[string]int `json:"by_severity"` // 台账按内部等级
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

// SrcProgramFeedback 是一个平台项目的历史结果分布（台账 → 提示词/打法库回流用）。
// 只统计已经交出去的台账（draft/ready 还没经过平台，不能当反馈）。
type SrcProgramFeedback struct {
	ProgramID   int64          `json:"program_id"`
	Program     string         `json:"program"`
	Submissions int            `json:"submissions"` // 全部台账（含草稿）
	Submitted   int            `json:"submitted"`   // 已交给平台（含终态）
	Accepted    int            `json:"accepted"`    // accepted + paid
	Rejected    int            `json:"rejected"`
	Duplicate   int            `json:"duplicate"`
	Paid        int            `json:"paid"`
	Reasons     map[string]int `json:"reasons"` // 驳回原因码 → 次数
}

// SrcProgramFeedback 聚合某项目的台账结果；programID<=0 返回空统计。
func (s *SrcStore) SrcProgramFeedback(ctx context.Context, programID int64) (SrcProgramFeedback, error) {
	out := SrcProgramFeedback{ProgramID: programID, Reasons: map[string]int{}}
	if s == nil || s.db == nil || programID <= 0 {
		return out, nil
	}
	if err := s.db.QueryRowContext(ctx, `
SELECT p.name,
       count(*) FILTER (WHERE s.status IN ('submitted','accepted','rejected','duplicate','paid')),
       count(*) FILTER (WHERE s.status IN ('accepted','paid')),
       count(*) FILTER (WHERE s.status='rejected'),
       count(*) FILTER (WHERE s.status='duplicate'),
       count(*) FILTER (WHERE s.status='paid'),
       count(*)
FROM src_submissions s
JOIN src_programs p ON p.id = s.program_id
WHERE s.program_id=$1
GROUP BY p.name`, programID).Scan(
		&out.Program, &out.Submitted, &out.Accepted, &out.Rejected, &out.Duplicate, &out.Paid, &out.Submissions); err != nil {
		if err == sql.ErrNoRows {
			return out, nil
		}
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT COALESCE(NULLIF(verdict_reason,''),'other'), count(*)
FROM src_submissions
WHERE program_id=$1 AND status='rejected'
GROUP BY 1 ORDER BY 2 DESC`, programID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var n int
		if err := rows.Scan(&code, &n); err != nil {
			return out, err
		}
		out.Reasons[code] = n
	}
	return out, rows.Err()
}

// TaskProgramRetention returns task_id -> program retention_days for every task
// bound to a program. Tasks not in the map fall back to the global setting.
func (d *DB) TaskProgramRetention(ctx context.Context) (map[string]int, error) {
	if d == nil {
		return map[string]int{}, nil
	}
	rows, err := d.QueryContext(ctx, `SELECT t.id::text, p.retention_days
FROM tasks t JOIN src_programs p ON p.id=t.src_program_id
WHERE t.deleted_at IS NULL AND p.retention_days > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var days int
		if err := rows.Scan(&id, &days); err != nil {
			return nil, err
		}
		out[id] = days
	}
	return out, rows.Err()
}

// ProgramRetentionGroups returns program_id -> retention_days and the task ids
// belonging to each program (used by the cleanup sweeper to honour overrides).
type ProgramRetentionGroup struct {
	ProgramID int64
	Days      int
	TaskIDs   []string
}

// ProgramRetentionGroups lists programs with a retention override and their tasks.
func (d *DB) ProgramRetentionGroups(ctx context.Context) ([]ProgramRetentionGroup, error) {
	if d == nil {
		return nil, nil
	}
	rows, err := d.QueryContext(ctx, `SELECT p.id, p.retention_days,
  COALESCE(array_to_json(array_agg(t.id::text) FILTER (WHERE t.id IS NOT NULL))::text, '[]')
FROM src_programs p LEFT JOIN tasks t ON t.src_program_id=p.id AND t.deleted_at IS NULL
WHERE p.retention_days > 0
GROUP BY p.id, p.retention_days ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProgramRetentionGroup{}
	for rows.Next() {
		var g ProgramRetentionGroup
		var idsRaw string
		if err := rows.Scan(&g.ProgramID, &g.Days, &idsRaw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(idsRaw), &g.TaskIDs); err != nil {
			return nil, fmt.Errorf("解析 program #%d 的任务列表失败: %w", g.ProgramID, err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
