package db

// 用户自定义平台提交模板。内置模板仍在 srcmode（代码即事实），这里只存用户新增；
// key 必须带 custom: 前缀，保证不会覆盖内置 key。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SrcSubmitField is one row of a custom submission form.
type SrcSubmitField struct {
	Label    string `json:"label"`
	Source   string `json:"source"`
	Required bool   `json:"required,omitempty"`
}

// SrcSubmitTemplate is one user-defined platform template.
type SrcSubmitTemplate struct {
	Key       string           `json:"key"`
	Name      string           `json:"name"`
	Header    string           `json:"header,omitempty"`
	RuleURL   string           `json:"rule_url,omitempty"`
	RuleDate  string           `json:"rule_date,omitempty"`
	Fields    []SrcSubmitField `json:"fields"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

const srcSubmitTemplateCols = `key, name, header, rule_url, rule_date, CAST(fields AS TEXT), created_at, updated_at`

func scanSrcSubmitTemplate(sc interface{ Scan(...any) error }) (*SrcSubmitTemplate, error) {
	var t SrcSubmitTemplate
	var fieldsRaw string
	if err := sc.Scan(&t.Key, &t.Name, &t.Header, &t.RuleURL, &t.RuleDate, &fieldsRaw,
		&t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(fieldsRaw), &t.Fields); err != nil {
		return nil, fmt.Errorf("模板 %s 的 fields 损坏: %w", t.Key, err)
	}
	if t.Fields == nil {
		t.Fields = []SrcSubmitField{}
	}
	return &t, nil
}

// ListSrcSubmitTemplates returns user templates, newest first.
func (s *SrcStore) ListSrcSubmitTemplates(ctx context.Context) ([]SrcSubmitTemplate, error) {
	if s == nil || s.db == nil {
		return []SrcSubmitTemplate{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+srcSubmitTemplateCols+` FROM src_platform_templates ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcSubmitTemplate{}
	for rows.Next() {
		t, err := scanSrcSubmitTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// GetSrcSubmitTemplate returns nil when the key does not exist.
func (s *SrcStore) GetSrcSubmitTemplate(ctx context.Context, key string) (*SrcSubmitTemplate, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	t, err := scanSrcSubmitTemplate(s.db.QueryRowContext(ctx,
		`SELECT `+srcSubmitTemplateCols+` FROM src_platform_templates WHERE key=$1`, strings.TrimSpace(key)))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

// UpsertSrcSubmitTemplate creates or replaces one custom template.
func (s *SrcStore) UpsertSrcSubmitTemplate(ctx context.Context, t SrcSubmitTemplate) (*SrcSubmitTemplate, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("src store unavailable")
	}
	key := strings.ToLower(strings.TrimSpace(t.Key))
	if !strings.HasPrefix(key, "custom:") {
		return nil, fmt.Errorf("自定义模板 key 必须以 custom: 开头")
	}
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return nil, fmt.Errorf("模板名称不能为空")
	}
	fields, err := json.Marshal(t.Fields)
	if err != nil {
		return nil, err
	}
	row, err := scanSrcSubmitTemplate(s.db.QueryRowContext(ctx, `
INSERT INTO src_platform_templates(key, name, header, rule_url, rule_date, fields)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (key) DO UPDATE SET
  name=EXCLUDED.name, header=EXCLUDED.header, rule_url=EXCLUDED.rule_url,
  rule_date=EXCLUDED.rule_date, fields=EXCLUDED.fields, updated_at=now()
RETURNING `+srcSubmitTemplateCols, key, name, strings.TrimSpace(t.Header),
		strings.TrimSpace(t.RuleURL), strings.TrimSpace(t.RuleDate), fields))
	if err != nil {
		return nil, err
	}
	return row, nil
}

// DeleteSrcSubmitTemplate removes one custom template.
func (s *SrcStore) DeleteSrcSubmitTemplate(ctx context.Context, key string) (bool, error) {
	if s == nil || s.db == nil {
		return false, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM src_platform_templates WHERE key=$1`, strings.TrimSpace(key))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
