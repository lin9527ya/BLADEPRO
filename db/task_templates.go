package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	MaxTaskTemplateNameRunes = 120
	MaxTaskTemplateTextRunes = 16000
)

var (
	ErrTaskTemplateInvalid      = errors.New("invalid task template")
	ErrTaskTemplateNameConflict = errors.New("task template name already exists")
	ErrTaskTemplateNotFound     = errors.New("task template not found")
)

// TaskTemplate is a reusable task preset (description/goal + optional category
// and task-level intercept/allow rules).
type TaskTemplate struct {
	ID             int64                    `json:"id"`
	Name           string                   `json:"name"`
	NKey           string                   `json:"-"`
	Description    string                   `json:"description"`
	Goal           string                   `json:"goal"`
	CategoryID     *int64                   `json:"category_id"`
	InterceptRules []TaskInterceptRuleInput `json:"intercept_rules"`
	// SRC 预设：mode='' 普通渗透 / 'src'；src_program_id 绑定平台项目；
	// inherit_coverage 在应用模板时默认勾选。
	Mode            string `json:"mode,omitempty"`
	SrcProgramID    *int64 `json:"src_program_id,omitempty"`
	InheritCoverage bool   `json:"inherit_coverage,omitempty"`
	// Batch 是批量扇出规格（与计划任务同一个 JSON 形状）；null/空 = 单个任务。
	Batch     json.RawMessage `json:"batch,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// TaskTemplateInput is the create/update payload after normalization.
type TaskTemplateInput struct {
	Name            string
	Description     string
	Goal            string
	CategoryID      *int64
	InterceptRules  []TaskInterceptRuleInput
	Mode            string
	SrcProgramID    *int64
	InheritCoverage bool
	Batch           json.RawMessage
}

// TaskTemplatePatch changes only fields flagged as set. Name/Description/Goal use
// non-nil pointers; CategoryID/InterceptRules use explicit Set flags (so a nil
// CategoryID can mean "clear" when SetCategoryID is true).
type TaskTemplatePatch struct {
	Name              *string
	Description       *string
	Goal              *string
	CategoryID        *int64
	SetCategoryID     bool
	InterceptRules    []TaskInterceptRuleInput
	SetInterceptRules bool
	Mode              *string
	SrcProgramID      *int64
	SetSrcProgramID   bool
	InheritCoverage   *bool
	Batch             json.RawMessage
	SetBatch          bool
}

const taskTemplateCols = `id, name, nkey, description, goal, category_id, intercept_rules, COALESCE(mode,''), src_program_id, COALESCE(inherit_coverage,false), COALESCE(batch,'null'::jsonb), created_at, updated_at`

func scanTaskTemplate(row interface{ Scan(...any) error }) (TaskTemplate, error) {
	var t TaskTemplate
	var rulesRaw, batchRaw []byte
	if err := row.Scan(&t.ID, &t.Name, &t.NKey, &t.Description, &t.Goal, &t.CategoryID, &rulesRaw,
		&t.Mode, &t.SrcProgramID, &t.InheritCoverage, &batchRaw, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return t, err
	}
	if len(batchRaw) > 0 && string(batchRaw) != "null" {
		t.Batch = json.RawMessage(batchRaw)
	}
	t.InterceptRules = []TaskInterceptRuleInput{}
	if len(rulesRaw) > 0 {
		if err := json.Unmarshal(rulesRaw, &t.InterceptRules); err != nil {
			return t, err
		}
		if t.InterceptRules == nil {
			t.InterceptRules = []TaskInterceptRuleInput{}
		}
	}
	return t, nil
}

// marshalTemplateRules serializes a template's rule snapshot to JSONB text,
// always producing a JSON array (never null).
func marshalTemplateRules(rules []TaskInterceptRuleInput) ([]byte, error) {
	if rules == nil {
		rules = []TaskInterceptRuleInput{}
	}
	return json.Marshal(rules)
}

// taskTemplateName normalizes display whitespace while preserving the user's case.
func taskTemplateName(name string) string { return strings.Join(strings.Fields(name), " ") }

// taskTemplateNKey is the case-insensitive identity used by the unique index.
func taskTemplateNKey(name string) string { return strings.ToLower(taskTemplateName(name)) }

func normalizeTaskTemplateInput(in TaskTemplateInput) (TaskTemplateInput, string, error) {
	in.Name = taskTemplateName(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.Goal = strings.TrimSpace(in.Goal)
	switch {
	case in.Name == "":
		return in, "", fmt.Errorf("%w: name is required", ErrTaskTemplateInvalid)
	case utf8.RuneCountInString(in.Name) > MaxTaskTemplateNameRunes:
		return in, "", fmt.Errorf("%w: name exceeds %d characters", ErrTaskTemplateInvalid, MaxTaskTemplateNameRunes)
	case in.Description == "":
		return in, "", fmt.Errorf("%w: description is required", ErrTaskTemplateInvalid)
	case utf8.RuneCountInString(in.Description) > MaxTaskTemplateTextRunes:
		return in, "", fmt.Errorf("%w: description exceeds %d characters", ErrTaskTemplateInvalid, MaxTaskTemplateTextRunes)
	case in.Goal == "":
		return in, "", fmt.Errorf("%w: goal is required", ErrTaskTemplateInvalid)
	case utf8.RuneCountInString(in.Goal) > MaxTaskTemplateTextRunes:
		return in, "", fmt.Errorf("%w: goal exceeds %d characters", ErrTaskTemplateInvalid, MaxTaskTemplateTextRunes)
	}
	in.Mode = strings.TrimSpace(in.Mode)
	if in.Mode != "" && in.Mode != "src" {
		return in, "", fmt.Errorf("%w: mode must be '' or 'src'", ErrTaskTemplateInvalid)
	}
	if in.Mode != "src" && (in.SrcProgramID != nil || in.InheritCoverage) {
		return in, "", fmt.Errorf("%w: src_program_id/inherit_coverage require mode=src", ErrTaskTemplateInvalid)
	}
	return in, taskTemplateNKey(in.Name), nil
}

func normalizeTaskTemplatePatch(patch TaskTemplatePatch) (TaskTemplatePatch, *string, error) {
	if patch.Name == nil && patch.Description == nil && patch.Goal == nil && !patch.SetCategoryID && !patch.SetInterceptRules &&
		patch.Mode == nil && !patch.SetSrcProgramID && patch.InheritCoverage == nil && !patch.SetBatch {
		return patch, nil, fmt.Errorf("%w: no fields supplied", ErrTaskTemplateInvalid)
	}
	if patch.Mode != nil {
		mode := strings.TrimSpace(*patch.Mode)
		if mode != "" && mode != "src" {
			return patch, nil, fmt.Errorf("%w: mode must be '' or 'src'", ErrTaskTemplateInvalid)
		}
		patch.Mode = &mode
	}
	var nkey *string
	if patch.Name != nil {
		name := taskTemplateName(*patch.Name)
		if name == "" {
			return patch, nil, fmt.Errorf("%w: name is required", ErrTaskTemplateInvalid)
		}
		if utf8.RuneCountInString(name) > MaxTaskTemplateNameRunes {
			return patch, nil, fmt.Errorf("%w: name exceeds %d characters", ErrTaskTemplateInvalid, MaxTaskTemplateNameRunes)
		}
		key := taskTemplateNKey(name)
		patch.Name = &name
		nkey = &key
	}
	if patch.Description != nil {
		description := strings.TrimSpace(*patch.Description)
		if description == "" {
			return patch, nil, fmt.Errorf("%w: description is required", ErrTaskTemplateInvalid)
		}
		if utf8.RuneCountInString(description) > MaxTaskTemplateTextRunes {
			return patch, nil, fmt.Errorf("%w: description exceeds %d characters", ErrTaskTemplateInvalid, MaxTaskTemplateTextRunes)
		}
		patch.Description = &description
	}
	if patch.Goal != nil {
		goal := strings.TrimSpace(*patch.Goal)
		if goal == "" {
			return patch, nil, fmt.Errorf("%w: goal is required", ErrTaskTemplateInvalid)
		}
		if utf8.RuneCountInString(goal) > MaxTaskTemplateTextRunes {
			return patch, nil, fmt.Errorf("%w: goal exceeds %d characters", ErrTaskTemplateInvalid, MaxTaskTemplateTextRunes)
		}
		patch.Goal = &goal
	}
	return patch, nkey, nil
}

// nullJSON turns an absent batch spec into SQL NULL instead of an empty bytea.
func nullJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func taskTemplateUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// CreateTaskTemplate inserts one globally reusable preset.
func (d *DB) CreateTaskTemplate(in TaskTemplateInput) (*TaskTemplate, error) {
	in, nkey, err := normalizeTaskTemplateInput(in)
	if err != nil {
		return nil, err
	}
	rulesJSON, err := marshalTemplateRules(in.InterceptRules)
	if err != nil {
		return nil, err
	}
	t, err := scanTaskTemplate(d.QueryRow(`
INSERT INTO task_templates(name, nkey, description, goal, category_id, intercept_rules, mode, src_program_id, inherit_coverage, batch)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (nkey) DO NOTHING
RETURNING `+taskTemplateCols, in.Name, nkey, in.Description, in.Goal, in.CategoryID, rulesJSON,
		in.Mode, in.SrcProgramID, in.InheritCoverage, nullJSON(in.Batch)))
	if err == sql.ErrNoRows {
		return nil, ErrTaskTemplateNameConflict
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTaskTemplates returns the most recently maintained templates first.
func (d *DB) ListTaskTemplates() ([]*TaskTemplate, error) {
	rows, err := d.Query(`SELECT ` + taskTemplateCols + ` FROM task_templates ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TaskTemplate{}
	for rows.Next() {
		t, err := scanTaskTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// GetTaskTemplate returns nil when id does not exist.
func (d *DB) GetTaskTemplate(id int64) (*TaskTemplate, error) {
	t, err := scanTaskTemplate(d.QueryRow(`SELECT `+taskTemplateCols+` FROM task_templates WHERE id=$1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTaskTemplate replaces the editable fields of one preset.
func (d *DB) UpdateTaskTemplate(id int64, in TaskTemplateInput) (*TaskTemplate, error) {
	in, _, err := normalizeTaskTemplateInput(in)
	if err != nil {
		return nil, err
	}
	return d.PatchTaskTemplate(id, TaskTemplatePatch{
		Name: &in.Name, Description: &in.Description, Goal: &in.Goal,
		Mode: &in.Mode, SrcProgramID: in.SrcProgramID, SetSrcProgramID: true,
		InheritCoverage: &in.InheritCoverage,
		Batch:           in.Batch, SetBatch: true,
	})
}

// PatchTaskTemplate atomically changes only the supplied fields. Keeping the
// merge in one UPDATE prevents concurrent disjoint PATCH requests from losing
// each other's changes.
func (d *DB) PatchTaskTemplate(id int64, patch TaskTemplatePatch) (*TaskTemplate, error) {
	patch, nkey, err := normalizeTaskTemplatePatch(patch)
	if err != nil {
		return nil, err
	}
	rulesJSON, err := marshalTemplateRules(patch.InterceptRules)
	if err != nil {
		return nil, err
	}
	t, err := scanTaskTemplate(d.QueryRow(`UPDATE task_templates
SET name=CASE WHEN $2 THEN $3::text ELSE name END,
    nkey=CASE WHEN $2 THEN $4::text ELSE nkey END,
    description=CASE WHEN $5 THEN $6::text ELSE description END,
    goal=CASE WHEN $7 THEN $8::text ELSE goal END,
    category_id=CASE WHEN $9 THEN $10::bigint ELSE category_id END,
    intercept_rules=CASE WHEN $11 THEN $12::jsonb ELSE intercept_rules END,
    mode=CASE WHEN $13::boolean THEN $14::text ELSE mode END,
    src_program_id=CASE WHEN $15::boolean THEN $16::bigint ELSE src_program_id END,
    inherit_coverage=CASE WHEN $17::boolean THEN $18::boolean ELSE inherit_coverage END,
    batch=CASE WHEN $19::boolean THEN $20::jsonb ELSE batch END
WHERE id=$1
RETURNING `+taskTemplateCols,
		id,
		patch.Name != nil, patch.Name, nkey,
		patch.Description != nil, patch.Description,
		patch.Goal != nil, patch.Goal,
		patch.SetCategoryID, patch.CategoryID,
		patch.SetInterceptRules, rulesJSON,
		patch.Mode != nil, patch.Mode,
		patch.SetSrcProgramID, patch.SrcProgramID,
		patch.InheritCoverage != nil, patch.InheritCoverage,
		patch.SetBatch, nullJSON(patch.Batch),
	))
	if err == sql.ErrNoRows {
		return nil, ErrTaskTemplateNotFound
	}
	if taskTemplateUniqueViolation(err) {
		return nil, ErrTaskTemplateNameConflict
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// DeleteTaskTemplate deletes one preset and reports whether it existed.
func (d *DB) DeleteTaskTemplate(id int64) (bool, error) {
	result, err := d.Exec(`DELETE FROM task_templates WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}
