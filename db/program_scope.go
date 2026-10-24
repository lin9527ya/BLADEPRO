package db

// 平台项目的显式 in/out 范围。company_id 是便捷的粗范围；这里补的是"同名公司里
// 还要排除某域名"和"只打指定 host"这类边界。

import (
	"context"
	"fmt"
	"net"
	"strings"
)

// ProgramScopeRow is one in/out row of a program.
type ProgramScopeRow struct {
	ID        int64  `json:"id"`
	ProgramID int64  `json:"program_id"`
	Direction string `json:"direction"` // in | out
	Kind      string `json:"kind"`      // root_domain | subdomain | ip | cidr
	Domain    string `json:"domain,omitempty"`
	Net       string `json:"net,omitempty"`
	Note      string `json:"note,omitempty"`
}

// ProgramScopeInput is one row accepted by the scope editor.
type ProgramScopeInput struct {
	Direction string `json:"direction"`
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	Note      string `json:"note,omitempty"`
}

const programScopeCols = `id, program_id, direction, kind, COALESCE(domain,''), COALESCE(net::text,''), note`

func scanProgramScope(sc interface{ Scan(...any) error }) (*ProgramScopeRow, error) {
	var r ProgramScopeRow
	if err := sc.Scan(&r.ID, &r.ProgramID, &r.Direction, &r.Kind, &r.Domain, &r.Net, &r.Note); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListProgramScope returns a program's in/out rows (in first, then out).
func (s *SrcStore) ListProgramScope(ctx context.Context, programID int64) ([]ProgramScopeRow, error) {
	if s == nil || s.db == nil || programID <= 0 {
		return []ProgramScopeRow{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+programScopeCols+`
FROM src_program_scope WHERE program_id=$1
ORDER BY CASE direction WHEN 'in' THEN 0 ELSE 1 END, kind, COALESCE(domain,''), COALESCE(net::text,'')`, programID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProgramScopeRow{}
	for rows.Next() {
		r, err := scanProgramScope(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// NormalizeProgramScope validates one (kind,value) and returns the stored columns.
// Bare IP becomes /32 or /128; domains are normalized via DomainKey.
func NormalizeProgramScope(kind, value string) (domain, netCIDR string, err error) {
	kind = strings.TrimSpace(strings.ToLower(kind))
	value = strings.TrimSpace(value)
	switch kind {
	case "root_domain", "subdomain":
		d := DomainKey(stripHostPort(value))
		if d == "" {
			return "", "", fmt.Errorf("域名不能为空")
		}
		return d, "", nil
	case "ip", "cidr":
		if strings.Contains(value, "/") {
			_, n, err := net.ParseCIDR(value)
			if err != nil {
				return "", "", fmt.Errorf("CIDR 不合法：%s", value)
			}
			return "", n.String(), nil
		}
		ip := net.ParseIP(stripHostPort(value))
		if ip == nil {
			return "", "", fmt.Errorf("IP 不合法：%s", value)
		}
		if ip.To4() != nil {
			return "", ip.String() + "/32", nil
		}
		return "", ip.String() + "/128", nil
	}
	return "", "", fmt.Errorf("kind 只能是 root_domain / subdomain / ip / cidr")
}

// ReplaceProgramScope atomically replaces a program's scope rows.
func (s *SrcStore) ReplaceProgramScope(ctx context.Context, programID int64, rows []ProgramScopeInput) ([]ProgramScopeRow, error) {
	if s == nil || s.db == nil || programID <= 0 {
		return nil, fmt.Errorf("invalid program")
	}
	if len(rows) > 500 {
		return nil, fmt.Errorf("项目范围最多 500 条")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `DELETE FROM src_program_scope WHERE program_id=$1`, programID); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i, r := range rows {
		direction := strings.TrimSpace(strings.ToLower(r.Direction))
		if direction != "in" && direction != "out" {
			return nil, fmt.Errorf("第 %d 行 direction 只能是 in / out", i+1)
		}
		domain, netCIDR, err := NormalizeProgramScope(r.Kind, r.Value)
		if err != nil {
			return nil, fmt.Errorf("第 %d 行：%w", i+1, err)
		}
		key := direction + "|" + r.Kind + "|" + domain + "|" + netCIDR
		if seen[key] {
			continue // 重复行静默去重：编辑器里复制粘贴很常见
		}
		seen[key] = true
		var domainArg, netArg any
		if domain != "" {
			domainArg = domain
		}
		if netCIDR != "" {
			netArg = netCIDR
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO src_program_scope(program_id, direction, kind, domain, net, note)
VALUES ($1,$2,$3,$4,$5::cidr,COALESCE(NULLIF($6,''),''))`,
			programID, direction, strings.TrimSpace(strings.ToLower(r.Kind)), domainArg, netArg, strings.TrimSpace(r.Note)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListProgramScope(ctx, programID)
}

// ProgramScopeToTaskScope maps a program row to the task_scope input shape.
func ProgramScopeToTaskScope(r ProgramScopeRow, reason string) TaskScopeInput {
	return TaskScopeInput{Kind: r.Kind, Domain: r.Domain, Net: r.Net, Reason: reason}
}

// CountProgramScope counts in/out rows for prompts/notes.
func (s *SrcStore) CountProgramScope(ctx context.Context, programID int64) (in, out int, err error) {
	if s == nil || s.db == nil || programID <= 0 {
		return 0, 0, nil
	}
	err = s.db.QueryRowContext(ctx, `
SELECT count(*) FILTER (WHERE direction='in'), count(*) FILTER (WHERE direction='out')
FROM src_program_scope WHERE program_id=$1`, programID).Scan(&in, &out)
	return in, out, err
}
