package srcmode

import "strings"

// N/A 理由码：把"这个面不存在（有依据）"和"没测"分开。
//
// 技能的口径很硬（dig-scope §4.2.1）：只回"请登录"且没有业务字段 → 整段 N/A，不要挨个丢
// 引号；有入口就必须打完。所以 N/A 不是自由文本，而是**限定的几种情形**，每种绑定到
// 允许出现的风险面。写错面的理由码（比如给 authz 填 no_exec_sink）直接拒绝。

// NACode 是一个 N/A 理由码。
type NACode struct {
	Code  string   `json:"code"`
	Title string   `json:"title"`
	Areas []string `json:"areas"` // 空 = 适用于全部风险面
}

// NACodes 是全部合法理由码（顺序即 UI 展示顺序）。
var NACodes = []NACode{
	{Code: "no_input", Title: "无业务参数（无 query/body 业务字段）", Areas: []string{"injection", "logic", "xss", "ssrf"}},
	{Code: "no_url_param", Title: "无 URL 拉取/代理类参数", Areas: []string{"ssrf"}},
	{Code: "no_reflect", Title: "无回显点", Areas: []string{"xss"}},
	{Code: "no_writer", Title: "无用户可控写入点", Areas: []string{"xss"}},
	{Code: "no_exec_sink", Title: "清单+抓包+回包都无执行 sink", Areas: []string{"rce"}},
	{Code: "no_upload_entry", Title: "无上传/下载入口", Areas: []string{"upload"}},
	{Code: "no_object", Title: "纯登录墙，抽不出对象", Areas: []string{"authz", "logic"}},
	{Code: "no_login_entry", Title: "清单里无发会话/重置/改绑/换票口", Areas: []string{"session"}},
	// 纯静态页只对"输入/执行/对象"类面成立；paths/cred 仍可能命中（备份、JS 密钥），
	// host/takeover 更与页面静态与否无关，所以不给这两类开口子。
	{Code: "static_page", Title: "纯静态页面", Areas: []string{
		"injection", "xss", "ssrf", "upload", "rce", "authz", "logic", "session", "csrf",
	}},
	{Code: "platform_exempt", Title: "平台档案明确不测", Areas: nil},
	{Code: "out_of_scope", Title: "越界资产，不在授权范围", Areas: nil},
}

// 收尾项的 N/A 理由码（与 @CheckDef.NaCodes 对应）。
var checkNACodes = map[string]string{
	"no_business_param":     "没有可换的业务参数",
	"no_object":             "抽不出业务对象",
	"no_defense_claim":      "没有任何「防护到位」的结论要证",
	"reachable":             "资产可达，不需要判死",
	"platform_not_required": "平台不要求截图",
	"not_applicable":        "不适用（需在 reason 写清）",
}

var naCodeByCode = func() map[string]NACode {
	m := make(map[string]NACode, len(NACodes))
	for _, c := range NACodes {
		m[c.Code] = c
	}
	return m
}()

// IsNACode reports whether code is a known cell-level N/A code.
func IsNACode(code string) bool {
	_, ok := naCodeByCode[strings.TrimSpace(code)]
	return ok
}

// NACodeAllowed reports whether code may be used for that risk area.
func NACodeAllowed(area, code string) bool {
	c, ok := naCodeByCode[strings.TrimSpace(code)]
	if !ok {
		return false
	}
	if len(c.Areas) == 0 {
		return true
	}
	for _, a := range c.Areas {
		if a == area {
			return true
		}
	}
	return false
}

// AreaNACodes 返回某风险面可用的理由码（UI 与工具描述用）。
func AreaNACodes(area string) []string {
	out := make([]string, 0, 4)
	for _, c := range NACodes {
		if len(c.Areas) == 0 || containsStr(c.Areas, area) {
			out = append(out, c.Code)
		}
	}
	return out
}

// NACodeTitle 返回理由码的中文说明（未知码原样返回）。
func NACodeTitle(code string) string {
	if c, ok := naCodeByCode[strings.TrimSpace(code)]; ok {
		return c.Title
	}
	if t, ok := checkNACodes[strings.TrimSpace(code)]; ok {
		return t
	}
	return code
}

// CheckNACodes 返回收尾项可用的理由码及说明（空 = 该项不接受 N/A）。
func (def CheckDef) CheckNACodes() map[string]string {
	if len(def.NaCodes) == 0 {
		return nil
	}
	out := make(map[string]string, len(def.NaCodes))
	for _, code := range def.NaCodes {
		out[code] = NACodeTitle(code)
	}
	return out
}
