package db

import (
	"fmt"
	"strconv"
	"strings"
)

// VendorContextBlock 渲染厂商上下文块：域名归属、测试窗口、不收洞型、红线、探活地址
// 与公司定级规则。空字符串表示该任务没有可归因的公司档案或公司档案为空。
//
// 这层是提示词约束（P-Q 移植自 quiver 的 vendorBlock）：与 program 规则冲突时取更严者，
// 不改变任务授权范围——授权仍由 task_scope / 冻结快照决定。
func (s *CompanyStore) VendorContextBlock(taskID int64) string {
	if s == nil || s.db == nil || taskID <= 0 {
		return ""
	}
	profs, err := s.ResolveTaskCompanies(taskID)
	if err != nil || len(profs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【厂商档案（平台口径，优先于通用经验）】：\n")
	ids := make([]int64, 0, len(profs))
	for _, p := range profs {
		ids = append(ids, p.CompanyID)
	}
	rules := s.companyRatingRules(ids)
	any := false
	for _, p := range profs {
		if len(p.Domains) == 0 && p.TestWindow == "" && p.RateLimitNote == "" &&
			len(p.BannedVulnClasses) == 0 && p.CheckURL == "" {
			continue
		}
		any = true
		fmt.Fprintf(&b, "- %s\n", p.CompanyName)
		if len(p.Domains) > 0 {
			fmt.Fprintf(&b, "  · 归属域名：%s\n", strings.Join(p.Domains, " / "))
		}
		if p.TestWindow != "" {
			fmt.Fprintf(&b, "  · 测试窗口：%s\n", p.TestWindow)
		}
		if p.RateLimitNote != "" {
			fmt.Fprintf(&b, "  · 频率说明：%s\n", p.RateLimitNote)
		}
		if len(p.BannedVulnClasses) > 0 {
			fmt.Fprintf(&b, "  · 不收洞型：%s（不要投入，也不要在报告里提交）\n", strings.Join(p.BannedVulnClasses, " / "))
		}
		if p.CheckURL != "" {
			fmt.Fprintf(&b, "  · 账号探活地址：%s\n", p.CheckURL)
		}
	}
	if len(rules) > 0 {
		any = true
		b.WriteString("- 公司定级口径（优先于通用规则）：\n")
		for _, r := range rules {
			line := fmt.Sprintf("  · %s → %s", r.VulnClass, r.Severity)
			if r.Note != "" {
				line += "（" + r.Note + "）"
			}
			b.WriteString(line + "\n")
		}
	}
	if !any {
		return ""
	}
	b.WriteString("（厂商档案只约束测试深度/不收洞型/窗口，不改变任务授权范围；与 program 规则冲突时取更严者。）\n")
	return b.String()
}

// companyRatingRules 取公司定级规则 + 通用规则（公司规则优先，最多 50 条）。
func (s *CompanyStore) companyRatingRules(companyIDs []int64) []RatingRule {
	if s == nil || s.db == nil || len(companyIDs) == 0 {
		return nil
	}
	parts := make([]string, 0, len(companyIDs))
	for _, id := range companyIDs {
		if id > 0 {
			parts = append(parts, strconv.FormatInt(id, 10))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	rows, err := s.db.Query(`SELECT id, COALESCE(company_id,0), vulnclass, severity, title, note, enabled, created_at
		FROM rating_rules
		WHERE enabled AND (company_id IS NULL OR company_id IN (` + strings.Join(parts, ",") + `))
		ORDER BY (company_id IS NOT NULL) DESC, vulnclass LIMIT 50`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []RatingRule{}
	for rows.Next() {
		var r RatingRule
		if err := rows.Scan(&r.ID, &r.CompanyID, &r.VulnClass, &r.Severity, &r.Title, &r.Note, &r.Enabled, &r.CreatedAt); err == nil {
			out = append(out, r)
		}
	}
	return out
}
