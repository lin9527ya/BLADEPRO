package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// 公司档案（厂商维度）：复用 companies 表，用 profile_* 列承载 quiver 的
// vendor_profiles 语义。不新增 vendors 表——"厂商"就是企业本身，平台项目
// （src_programs）继续表达"这个平台上的那个项目"。
type CompanyProfile struct {
	CompanyID         int64           `json:"company_id"`
	CompanyName       string          `json:"company_name"`
	MaxDepth          int             `json:"max_depth"`
	BannedVulnClasses []string        `json:"banned_vuln_classes"`
	ReportFormatMD    string          `json:"report_format_md,omitempty"`
	TestWindow        string          `json:"test_window,omitempty"`
	RateLimitNote     string          `json:"rate_limit_note,omitempty"`
	CheckURL          string          `json:"check_url,omitempty"`
	Domains           []string        `json:"domains,omitempty"`
	ReceiptPrefs      json.RawMessage `json:"receipt_prefs,omitempty"`
}

const companyProfileCols = `id, name, profile_max_depth,
	COALESCE(array_to_json(profile_banned_vuln_classes)::text,'[]'),
	profile_report_format_md, profile_test_window, profile_rate_limit_note,
	profile_check_url, COALESCE(array_to_json(profile_domains)::text,'[]'), profile_receipt_prefs`

func scanCompanyProfile(sc interface {
	Scan(dest ...any) error
}) (*CompanyProfile, error) {
	var p CompanyProfile
	var receipt []byte
	var bannedJSON, domainsJSON string
	if err := sc.Scan(&p.CompanyID, &p.CompanyName, &p.MaxDepth, &bannedJSON,
		&p.ReportFormatMD, &p.TestWindow, &p.RateLimitNote, &p.CheckURL, &domainsJSON, &receipt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(bannedJSON), &p.BannedVulnClasses); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(domainsJSON), &p.Domains); err != nil {
		return nil, err
	}
	if len(receipt) > 0 {
		p.ReceiptPrefs = json.RawMessage(receipt)
	}
	return &p, nil
}

// GetCompanyProfile 取单个公司档案；公司不存在返回 nil。
func (s *CompanyStore) GetCompanyProfile(companyID int64) (*CompanyProfile, error) {
	if s == nil || s.db == nil || companyID <= 0 {
		return nil, nil
	}
	row := s.db.QueryRow(`SELECT `+companyProfileCols+` FROM companies WHERE id=$1`, companyID)
	p, err := scanCompanyProfile(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// UpsertCompanyProfile 更新公司档案（公司必须已存在）。
func (s *CompanyStore) UpsertCompanyProfile(p CompanyProfile) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if p.CompanyID <= 0 {
		return fmt.Errorf("company_id 必填")
	}
	if p.MaxDepth < 0 || p.MaxDepth > 3 {
		return fmt.Errorf("max_depth 需在 0–3（0=不限制/跟随默认）")
	}
	if p.MaxDepth == 0 {
		p.MaxDepth = 2
	}
	var receipt any
	if len(p.ReceiptPrefs) > 0 {
		receipt = []byte(p.ReceiptPrefs)
	}
	bannedJSON, _ := json.Marshal(p.BannedVulnClasses)
	if p.BannedVulnClasses == nil {
		bannedJSON = []byte("[]")
	}
	domainsJSON, _ := json.Marshal(p.Domains)
	if p.Domains == nil {
		domainsJSON = []byte("[]")
	}
	res, err := s.db.Exec(`UPDATE companies SET profile_max_depth=$2,
		profile_banned_vuln_classes=ARRAY(SELECT jsonb_array_elements_text($3::jsonb)),
		profile_report_format_md=$4, profile_test_window=$5,
		profile_rate_limit_note=$6, profile_check_url=$7,
		profile_domains=ARRAY(SELECT jsonb_array_elements_text($8::jsonb)),
		profile_receipt_prefs=COALESCE($9,'{}'::jsonb), updated_at=now() WHERE id=$1`,
		p.CompanyID, p.MaxDepth, string(bannedJSON), strings.TrimSpace(p.ReportFormatMD),
		strings.TrimSpace(p.TestWindow), strings.TrimSpace(p.RateLimitNote),
		strings.TrimSpace(p.CheckURL), string(domainsJSON), receipt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("公司 %d 不存在", p.CompanyID)
	}
	return nil
}

// ListCompanyProfiles 列出全部公司档案（按名称排序）。
func (s *CompanyStore) ListCompanyProfiles() ([]CompanyProfile, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT ` + companyProfileCols + ` FROM companies ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CompanyProfile{}
	for rows.Next() {
		p, err := scanCompanyProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// ResolveTaskCompanies 按三段式归因任务所属公司：
//  1. task_scope 里显式的 company 行；
//  2. 任务范围内域名后缀命中 companies.profile_domains；
//  3. 任务描述/目标里出现公司名（精确包含，取最长名优先）。
//
// 去重后返回；没有任何命中时返回空切片。
func (s *CompanyStore) ResolveTaskCompanies(taskID int64) ([]CompanyProfile, error) {
	if s == nil || s.db == nil || taskID <= 0 {
		return nil, nil
	}
	seen := map[int64]bool{}
	out := []CompanyProfile{}

	// 1) 显式 company scope（可能多行）。
	// 用子查询而不是 JOIN：companyProfileCols 是未限定列名，JOIN 时与
	// task_scope.id / name 等列歧义（曾因此让显式归属静默落空）。
	rows, err := s.db.Query(`SELECT `+companyProfileCols+` FROM companies WHERE id IN (
		SELECT company_id FROM task_scope
		WHERE task_id=$1 AND kind='company' AND company_id IS NOT NULL)`, taskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		p, err := scanCompanyProfile(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if !seen[p.CompanyID] {
			seen[p.CompanyID] = true
			out = append(out, *p)
		}
	}
	rows.Close()

	// 2) 域名后缀归因。
	if len(out) == 0 {
		hosts := []string{}
		hrows, err := s.db.Query(`SELECT lower(COALESCE(domain,'')) FROM task_scope
			WHERE task_id=$1 AND kind IN ('root_domain','subdomain') AND COALESCE(domain,'') <> ''`, taskID)
		if err != nil {
			return nil, err
		}
		for hrows.Next() {
			var h string
			if err := hrows.Scan(&h); err != nil {
				hrows.Close()
				return nil, err
			}
			hosts = append(hosts, strings.TrimPrefix(h, "."))
		}
		hrows.Close()
		if len(hosts) > 0 {
			prows, err := s.db.Query(`SELECT ` + companyProfileCols + ` FROM companies
				WHERE cardinality(profile_domains) > 0`)
			if err != nil {
				return nil, err
			}
			for prows.Next() {
				p, err := scanCompanyProfile(prows)
				if err != nil {
					prows.Close()
					return nil, err
				}
				for _, d := range p.Domains {
					d = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "."))
					if d == "" || seen[p.CompanyID] {
						continue
					}
					for _, h := range hosts {
						if h == d || strings.HasSuffix(h, "."+d) {
							seen[p.CompanyID] = true
							out = append(out, *p)
							break
						}
					}
					if seen[p.CompanyID] {
						break
					}
				}
			}
			prows.Close()
		}
	}

	// 3) 任务文本包含公司名（取最长名优先，避免"腾讯"命中"腾讯云"以外的误配）。
	if len(out) == 0 {
		var text string
		if err := s.db.QueryRow(`SELECT description || ' ' || goal FROM tasks WHERE id=$1`, taskID).Scan(&text); err == nil {
			text = strings.ToLower(text)
			crows, err := s.db.Query(`SELECT ` + companyProfileCols + ` FROM companies ORDER BY length(name) DESC`)
			if err != nil {
				return nil, err
			}
			for crows.Next() {
				p, err := scanCompanyProfile(crows)
				if err != nil {
					crows.Close()
					return nil, err
				}
				name := strings.ToLower(strings.TrimSpace(p.CompanyName))
				if name == "" || len([]rune(name)) < 2 || seen[p.CompanyID] {
					continue
				}
				if strings.Contains(text, name) {
					seen[p.CompanyID] = true
					out = append(out, *p)
				}
			}
			crows.Close()
		}
	}
	return out, nil
}

// EffectiveDepth 解析任务生效深度：tasks.depth_override > 关联公司档案最大值 > 2。
// program 级 max_depth 在 P3 编译进 mode_doc 后再叠加。
func (s *CompanyStore) EffectiveDepth(taskID int64) int {
	if s == nil || s.db == nil || taskID <= 0 {
		return 2
	}
	var override sql.NullInt64
	var modeDoc sql.NullString
	if err := s.db.QueryRow(`SELECT depth_override, mode_doc::text FROM tasks WHERE id=$1`, taskID).
		Scan(&override, &modeDoc); err == nil {
		if override.Valid && override.Int64 >= 1 && override.Int64 <= 3 {
			return int(override.Int64)
		}
		// program 编译进 mode_doc 的 max_depth 优先于公司档案（只能更严）。
		if modeDoc.Valid && strings.TrimSpace(modeDoc.String) != "" {
			var md struct {
				MaxDepth int `json:"max_depth"`
			}
			if json.Unmarshal([]byte(modeDoc.String), &md) == nil && md.MaxDepth >= 1 && md.MaxDepth <= 3 {
				return md.MaxDepth
			}
		}
	}
	profiles, err := s.ResolveTaskCompanies(taskID)
	if err != nil || len(profiles) == 0 {
		return 2
	}
	best := 0
	for _, p := range profiles {
		if p.MaxDepth > best && p.MaxDepth <= 3 {
			best = p.MaxDepth
		}
	}
	if best < 1 {
		return 2
	}
	return best
}
