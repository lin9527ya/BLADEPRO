package srcmode

import (
	"net/url"
	"strings"
)

// 提交块（src-dig vuln-report-format.md §〇）：平台表单要的那几项，直接抄。
//
// 为什么由机器生成：测试源 IP、平台名、映射后的等级这几项都是"抄错就白干"的字段
// （技能里记录过"换网络后抄旧报告的 IP 必错"），而它们的来源本来就在任务快照里。
// 平台模板只改字段名/顺序，不改正文；未知平台回落到通用 5 字段。

// SubmitContext is the finding-level data templates may use. Empty optional fields
// are omitted; Required labels get a "（待补）" placeholder so a human cannot
// silently submit an empty form.
type SubmitContext struct {
	Title         string
	SeverityLabel string
	CWE           string
	CVSS          string
	Asset         string
	Unit          string
	ReproEnv      string
	Description   string
	Impact        string
	Fix           string
	Steps         string
	Endpoints     string
	Attachments   []string
}

// SubmitInfo renders the block with no finding-level extras (legacy signature).
func SubmitInfo(doc ModeDoc, severityLabel string) string {
	return SubmitInfoFor(doc, SubmitContext{SeverityLabel: severityLabel})
}

// SubmitInfoFor renders 【提交信息】 for the resolved platform template. Returns ""
// only when there is nothing platform-specific to say (generic template + no
// submission fields).
func SubmitInfoFor(doc ModeDoc, in SubmitContext) string {
	tpl := ResolvePlatformTemplate(doc)
	platform := strings.TrimSpace(doc.Platform)
	program := strings.TrimSpace(doc.Program)
	accounts := strings.TrimSpace(doc.TestAccounts)
	egress := strings.TrimSpace(doc.EgressIP)
	attachments := strings.Join(in.Attachments, "；")
	generic := tpl.Key == "generic"
	if generic && platform == "" && program == "" && egress == "" && accounts == "" &&
		strings.TrimSpace(in.Title) == "" && strings.TrimSpace(in.Asset) == "" &&
		strings.TrimSpace(in.CWE) == "" && strings.TrimSpace(in.CVSS) == "" && attachments == "" {
		return ""
	}

	var b strings.Builder
	if generic {
		b.WriteString("## 【提交信息】\n")
	} else {
		b.WriteString("## 【提交信息 · " + tpl.Name + "】\n")
	}
	line := func(label, value string) {
		if strings.TrimSpace(value) != "" {
			b.WriteString("- " + label + "：" + strings.TrimSpace(value) + "\n")
		}
	}
	required := func(label, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			value = "（待补）"
		}
		b.WriteString("- " + label + "：" + value + "\n")
	}
	ruleLine := func() {
		if tpl.RuleURL == "" {
			return
		}
		date := tpl.RuleDate
		if date == "" {
			date = "未知"
		}
		b.WriteString("- 规则页（模板日期 " + date + "；提交前请按平台最新规则复核）：" + tpl.RuleURL + "\n")
	}

	switch tpl.Key {
	case "butian":
		required("标题", in.Title)
		required("所属单位/厂商", in.Unit)
		line("所属应用/项目", program)
		required("漏洞类型", in.CWE)
		required("危害等级", in.SeverityLabel)
		required("资产/URL", in.Asset)
		line("CVSS", in.CVSS)
		line("测试账号", accounts)
		line("测试源IP", egress)
		line("复现与附件", attachments)
		ruleLine()
	case "vulbox":
		required("标题", in.Title)
		required("所属单位/厂商", in.Unit)
		line("所属项目", program)
		required("漏洞类型", in.CWE)
		required("危害等级", in.SeverityLabel)
		required("资产/URL", in.Asset)
		line("CVSS", in.CVSS)
		line("测试账号", accounts)
		line("测试源IP", egress)
		line("复现步骤/附件", attachments)
		ruleLine()
	case "edu":
		required("漏洞标题", in.Title)
		required("资产/单位", in.Asset)
		line("所属项目", program)
		required("漏洞类型", in.CWE)
		required("危害等级", in.SeverityLabel)
		line("复现环境", in.ReproEnv)
		line("测试账号", accounts)
		line("复现与附件", attachments)
		ruleLine()
	case "hackerone":
		required("Title", in.Title)
		required("Weakness", in.CWE)
		required("Severity", in.SeverityLabel)
		required("Asset", in.Asset)
		line("CVSS", in.CVSS)
		b.WriteString("- Steps to Reproduce: see the report body below\n")
		b.WriteString("- Impact: see the report body below\n")
		line("Attachments", attachments)
		ruleLine()
	default: // generic
		line("平台", platform)
		line("所属应用/项目", program)
		line("漏洞标题", in.Title)
		line("危害等级", in.SeverityLabel)
		line("CWE", in.CWE)
		line("CVSS", in.CVSS)
		line("资产/URL", in.Asset)
		line("测试源IP", egress)
		if accounts != "" {
			b.WriteString("- 测试账号：" + accounts + "\n")
		} else {
			b.WriteString("- 测试账号：无需账号（未授权类）；越权类需自备，缺账号会在审核阶段被挂起\n")
		}
		line("附件", attachments)
	}
	return b.String()
}

// SubmitBlockPlaceholders returns the labels whose rendered value is still the
// "（待补）" placeholder. Export checks use it so a complete-looking block cannot
// silently carry empty required fields.
func SubmitBlockPlaceholders(block string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		label, value, ok := strings.Cut(strings.TrimPrefix(line, "- "), "：")
		if !ok {
			label, value, ok = strings.Cut(strings.TrimPrefix(line, "- "), ":")
		}
		if !ok || strings.TrimSpace(value) != "（待补）" {
			continue
		}
		label = strings.TrimSpace(label)
		if label != "" && !seen[label] {
			seen[label] = true
			out = append(out, label)
		}
	}
	return out
}

// SubmitField is one rendered form row. It is returned to the preview UI so a
// submitter can copy each platform field independently.
type SubmitField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ParseSubmitFields parses the rendered "- label：value" submit block. Rule-page
// reminders are included as normal fields so the preview never hides context.
func ParseSubmitFields(block string) []SubmitField {
	var out []SubmitField
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		body := strings.TrimPrefix(line, "- ")
		label, value, ok := strings.Cut(body, "：")
		if !ok {
			label, value, ok = strings.Cut(body, ":")
		}
		if !ok || strings.TrimSpace(label) == "" {
			continue
		}
		out = append(out, SubmitField{Label: strings.TrimSpace(label), Value: strings.TrimSpace(value)})
	}
	return out
}

// SubmitIssues is the platform-facing readiness check. Unlike the generic
// evidence gate it validates the exact fields a submission form will contain:
// title, vulnerability type/CWE, severity and a real http(s) asset URL. Butian
// and Vulbox additionally need the project/unit and test-source IP because those
// fields are otherwise omitted from the rendered block.
func SubmitIssues(doc ModeDoc, in SubmitContext) []string {
	// Custom templates declare their own required labels; the rendered block's
	// （待补） markers are validated by the caller. Do not impose built-in fields.
	if strings.HasPrefix(strings.TrimSpace(doc.PlatformTemplate), "custom:") {
		return nil
	}
	var issues []string
	require := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			issues = append(issues, "提交信息缺"+label)
		}
	}
	require("标题", in.Title)
	require("漏洞类型（CWE/平台分类）", in.CWE)
	require("危害等级", in.SeverityLabel)
	require("资产/URL", in.Asset)
	if asset := strings.TrimSpace(in.Asset); asset != "" {
		u, err := url.Parse(asset)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			issues = append(issues, "提交信息资产/URL不是合法的 http(s) 链接："+asset)
		}
	}
	tpl := ResolvePlatformTemplate(doc)
	switch tpl.Key {
	case "butian", "vulbox":
		require("所属单位/厂商", in.Unit)
		require("所属应用/项目", doc.Program)
		require("测试源IP", doc.EgressIP)
	case "edu":
		require("所属项目", doc.Program)
	}
	return issues
}

// CustomSubmitField / CustomSubmitTemplate are the user-defined form layout.
// The server loads them from src_platform_templates and passes them in; this
// package stays database-free and only renders.
type CustomSubmitField struct {
	Label    string `json:"label"`
	Source   string `json:"source"`
	Required bool   `json:"required,omitempty"`
}

type CustomSubmitTemplate struct {
	Key      string              `json:"key"`
	Name     string              `json:"name"`
	Header   string              `json:"header,omitempty"`
	RuleURL  string              `json:"rule_url,omitempty"`
	RuleDate string              `json:"rule_date,omitempty"`
	Fields   []CustomSubmitField `json:"fields"`
}

// SubmitInfoForCustom renders a user-defined template. Unknown field sources are
// skipped (a template edited by hand must not break export).
func SubmitInfoForCustom(doc ModeDoc, in SubmitContext, tpl *CustomSubmitTemplate) string {
	if tpl == nil || len(tpl.Fields) == 0 {
		return SubmitInfoFor(doc, in)
	}
	program := strings.TrimSpace(doc.Program)
	accounts := strings.TrimSpace(doc.TestAccounts)
	egress := strings.TrimSpace(doc.EgressIP)
	attachments := strings.Join(in.Attachments, "；")
	value := func(source string) string {
		switch strings.ToLower(strings.TrimSpace(source)) {
		case "title":
			return in.Title
		case "severity":
			return in.SeverityLabel
		case "cwe":
			return in.CWE
		case "cvss":
			return in.CVSS
		case "asset":
			return in.Asset
		case "repro_env":
			return in.ReproEnv
		case "attachments":
			return attachments
		case "platform":
			return strings.TrimSpace(doc.Platform)
		case "program":
			return program
		case "egress_ip":
			return egress
		case "test_accounts":
			return accounts
		case "description":
			return in.Description
		case "impact":
			return in.Impact
		case "fix":
			return in.Fix
		case "steps":
			return in.Steps
		case "endpoints":
			return in.Endpoints
		case "unit":
			return in.Unit
		}
		return ""
	}
	var b strings.Builder
	name := strings.TrimSpace(tpl.Name)
	if name == "" {
		name = tpl.Key
	}
	b.WriteString("## 【提交信息 · " + name + "】\n")
	if h := strings.TrimSpace(tpl.Header); h != "" {
		b.WriteString(h + "\n")
	}
	for _, f := range tpl.Fields {
		v := strings.TrimSpace(value(f.Source))
		if v == "" && !f.Required {
			continue
		}
		label := strings.TrimSpace(f.Label)
		if label == "" {
			label = f.Source
		}
		if v == "" {
			v = "（待补）"
		}
		b.WriteString("- " + label + "：" + v + "\n")
	}
	if tpl.RuleURL != "" {
		date := tpl.RuleDate
		if date == "" {
			date = "未知"
		}
		b.WriteString("- 规则页（模板日期 " + date + "；提交前请按平台最新规则复核）：" + tpl.RuleURL + "\n")
	}
	return b.String()
}
