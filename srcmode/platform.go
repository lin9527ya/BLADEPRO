package srcmode

// 平台提交模板：平台表单字段名/顺序各不相同，导出时按模板渲染【提交信息】块，
// 避免让提交的人对着平台页面逐项翻译。正文仍是同一份 8 块报告。
//
// 模板是"内置字段映射"，不是平台规则的法律文本：RuleDate 记录模板写入日期，
// 提交前仍需按平台最新规则复核（提交块里会原样写出这句提醒）。

import "strings"

// PlatformTemplate describes one submission-form layout.
type PlatformTemplate struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	RuleURL  string `json:"rule_url,omitempty"`
	RuleDate string `json:"rule_date,omitempty"`
}

// PlatformTemplates is the built-in registry (stable order for UI).
func PlatformTemplates() []PlatformTemplate {
	return []PlatformTemplate{
		{Key: "generic", Name: "通用"},
		{Key: "butian", Name: "补天", RuleURL: "https://www.butian.net/", RuleDate: "2026-10-24"},
		{Key: "vulbox", Name: "漏洞盒子", RuleURL: "https://www.vulbox.com/", RuleDate: "2026-10-24"},
		{Key: "edu", Name: "教育 SRC", RuleDate: "2026-10-24"},
		{Key: "hackerone", Name: "HackerOne", RuleURL: "https://www.hackerone.com/policies", RuleDate: "2026-10-24"},
	}
}

// FindPlatformTemplate returns the template by key (generic when unknown key).
func FindPlatformTemplate(key string) (PlatformTemplate, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, t := range PlatformTemplates() {
		if t.Key == key {
			return t, true
		}
	}
	return PlatformTemplate{Key: "generic", Name: "通用"}, false
}

// DetectPlatformTemplate guesses a template from the free-text platform label.
func DetectPlatformTemplate(platform string) string {
	p := strings.ToLower(strings.TrimSpace(platform))
	switch {
	case strings.Contains(p, "补天") || strings.Contains(p, "butian"):
		return "butian"
	case strings.Contains(p, "漏洞盒子") || strings.Contains(p, "vulbox"):
		return "vulbox"
	case strings.Contains(p, "edu") || strings.Contains(p, "教育") || strings.Contains(p, "校园") || strings.Contains(p, "高校"):
		return "edu"
	case strings.Contains(p, "hackerone") || strings.Contains(p, "h1"):
		return "hackerone"
	}
	return "generic"
}

// ResolvePlatformTemplate picks the explicit snapshot template, else detects from
// the platform label, else generic.
func ResolvePlatformTemplate(doc ModeDoc) PlatformTemplate {
	if key := strings.TrimSpace(doc.PlatformTemplate); key != "" {
		if t, ok := FindPlatformTemplate(key); ok {
			return t
		}
	}
	if key := DetectPlatformTemplate(doc.Platform); key != "generic" {
		if t, ok := FindPlatformTemplate(key); ok {
			return t
		}
	}
	return PlatformTemplate{Key: "generic", Name: "通用"}
}

// PlatformTemplateKeys lists valid keys for error messages/UI.
func PlatformTemplateKeys() string {
	keys := make([]string, 0, len(PlatformTemplates()))
	for _, t := range PlatformTemplates() {
		keys = append(keys, t.Key)
	}
	return strings.Join(keys, " / ")
}
