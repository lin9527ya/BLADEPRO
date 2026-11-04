package db

import (
	"database/sql"
	"strings"
	"time"
)

// 定级规则库（移植自 quiver rating_rules）：company_id 为空 = 通用规则；
// 公司级规则优先于通用规则。由 ops/reviewer agent 与审批流使用。
type RatingRule struct {
	ID        int64     `json:"id"`
	CompanyID int64     `json:"company_id,omitempty"`
	VulnClass string    `json:"vulnclass"`
	Severity  string    `json:"severity"`
	Title     string    `json:"title,omitempty"`
	Note      string    `json:"note,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

var RatingSeverities = []string{"critical", "high", "medium", "low", "info"}

func normalizeSeverity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, v := range RatingSeverities {
		if s == v {
			return v
		}
	}
	return ""
}

// UpsertRatingRule 按 (company_id, vulnclass) 写入或更新一条定级规则。
func (s *AssetStore) UpsertRatingRule(companyID int64, vulnclass, severity, title, note string) (*RatingRule, error) {
	if s == nil || s.db == nil {
		return nil, errDBClosed
	}
	vulnclass = strings.ToLower(strings.TrimSpace(vulnclass))
	if vulnclass == "" {
		return nil, errBadInput("vulnclass 不能为空")
	}
	sev := normalizeSeverity(severity)
	if sev == "" {
		return nil, errBadInput("severity 只能是 critical/high/medium/low/info")
	}
	row := s.db.QueryRow(`INSERT INTO rating_rules(company_id, vulnclass, severity, title, note)
		VALUES (NULLIF($1,0),$2,$3,$4,$5)
		ON CONFLICT (COALESCE(company_id,0), vulnclass)
		DO UPDATE SET severity=EXCLUDED.severity, title=EXCLUDED.title, note=EXCLUDED.note, enabled=true
		RETURNING id, COALESCE(company_id,0), vulnclass, severity, title, note, enabled, created_at`,
		companyID, vulnclass, sev, strings.TrimSpace(title), strings.TrimSpace(note))
	var r RatingRule
	if err := row.Scan(&r.ID, &r.CompanyID, &r.VulnClass, &r.Severity, &r.Title, &r.Note, &r.Enabled, &r.CreatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRatingRules 列出公司规则 + 通用规则（公司规则优先）。
func (s *AssetStore) ListRatingRules(companyID int64) ([]RatingRule, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT id, COALESCE(company_id,0), vulnclass, severity, title, note, enabled, created_at
		FROM rating_rules
		WHERE enabled AND (COALESCE(company_id,0)=0 OR company_id=COALESCE(NULLIF($1,0),-1))
		ORDER BY (company_id=$1) DESC NULLS LAST, vulnclass`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RatingRule{}
	for rows.Next() {
		var r RatingRule
		if err := rows.Scan(&r.ID, &r.CompanyID, &r.VulnClass, &r.Severity, &r.Title, &r.Note, &r.Enabled, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRatingRule 删除一条（审批/UI 用）。
func (s *AssetStore) DeleteRatingRule(id int64) error {
	if s == nil || s.db == nil {
		return errDBClosed
	}
	_, err := s.db.Exec(`DELETE FROM rating_rules WHERE id=$1`, id)
	return err
}

// 复用 db 包已有的错误风格：这里只定义本文件需要的两个小构造器，避免引入新依赖。
var errDBClosed = sql.ErrConnDone

type errBadInput string

func (e errBadInput) Error() string { return string(e) }
