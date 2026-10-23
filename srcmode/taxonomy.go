package srcmode

import "sort"

// Area 是一个「风险面」：SRC 挖掘里"这个站点的这一类问题测过没有"的单位。
// 它比 db.RiskAreas 的意图标签粗，但**必须可判定**——一个面要么有终态，要么是空格。
type Area struct {
	Name   string   `json:"name"`
	Title  string   `json:"title"`
	Items  []string `json:"items"`  // 可细分的子项；空 = 只有 '*'
	Weight int      `json:"weight"` // 补格优先级权重
}

// Areas 是全部合法风险面。顺序即 UI 与提示词里的展示顺序（高风险在前）。
//
// 细项对齐 src-dig 的类型矩阵（rules/src-value-hunting.md §3、工具/站点覆盖矩阵模板.md）：
// 注入按栈分探针（SQL/NoSQL/SSTI/命令/HQL·SpEL/ES DSL/XPath/LDAP），越权分对象图与批量赋值，
// 认证口分发送会话/重置/改绑/换票/2FA，逻辑分跳步/竞态/提权/金额。
// CSRF 是"有入口就测、不当主业"的可选面：不在默认必做里，但允许点格留痕。
// CORS 永久不挖（rules/cors-vuln-report-priority.md），因此**不进矩阵**，只在提示词里写明。
var Areas = []Area{
	{Name: "rce", Title: "反序列化 / 上传 getshell / 命令执行 / 模板", Weight: 10,
		Items: []string{"deser", "upload-chain", "template", "expression", "ws-exec", "debug-endpoint"}},
	{Name: "injection", Title: "SQL / NoSQL / SSTI / 命令 / 表达式注入", Weight: 9,
		Items: []string{"sql", "nosql", "ssti", "cmdi", "hql-spel", "es-dsl", "xpath", "ldap"}},
	{Name: "authz", Title: "越权 / IDOR / 批量赋值", Weight: 8,
		Items: []string{"idor", "vertical", "mass-assign", "object-graph"}},
	{Name: "upload", Title: "上传 / 路径穿越 / 解析差异", Weight: 8,
		Items: []string{"bypass", "lfi"}},
	{Name: "session", Title: "登录 / 重置 / 改绑 / JWT / OAuth / 默认口令", Weight: 7,
		Items: []string{"login-bypass", "jwt", "reset", "rebind", "2fa", "default-cred"}},
	{Name: "logic", Title: "状态机 / 金额 / 竞态 / 提权", Weight: 7,
		Items: []string{"state-skip", "race", "privilege", "money"}},
	{Name: "ssrf", Title: "SSRF", Weight: 6,
		Items: []string{"url-param", "webhook", "cloud-meta", "oob"}},
	{Name: "unauth", Title: "未授权 / 未认证可达", Weight: 6,
		Items: []string{"no-auth-read", "param-completion", "swagger", "actuator", "graphql"}},
	{Name: "cred", Title: "凭证 / 密钥 / 会话泄露", Weight: 6,
		Items: []string{"js-hardcoded", "repo-leak", "aksk"}},
	{Name: "xss", Title: "XSS", Weight: 5,
		Items: []string{"reflect", "stored", "dom"}},
	{Name: "takeover", Title: "子域接管", Weight: 5},
	{Name: "host", Title: "端口 / 服务暴露 / 已知 CVE", Weight: 4,
		Items: []string{"exposed", "cve"}},
	{Name: "paths", Title: "敏感路径 / 备份 / 配置", Weight: 3,
		Items: []string{"enum-200", "non404-retest", "scm", "actuator"}},
	// 可选面：技能说"有入口仍测，不当主业"，所以不进默认必做，但允许留痕。
	{Name: "csrf", Title: "CSRF / 鉴权参数", Weight: 3,
		Items: []string{"no-token", "weak-cookie", "state-change"}},
}

// AreaAll 是"通配"细分项：表示整组终态。
const AreaAll = "*"

var areaByName = func() map[string]Area {
	m := make(map[string]Area, len(Areas))
	for _, a := range Areas {
		m[a.Name] = a
	}
	return m
}()

// defaultRequired 是"资产类型 → 必做面"的默认映射。必做是**下限**不是上限：
// 矩阵外的面照样能报，只是不占格。CSRF 不在其中（技能口径：有入口仍测，不当主业）。
var defaultRequired = map[string][]string{
	"app":         {"unauth", "authz", "injection", "ssrf", "xss", "rce", "paths", "session", "upload", "logic"},
	"service":     {"unauth", "authz", "injection", "ssrf", "xss", "rce", "paths", "session", "upload", "logic"},
	"endpoint":    {"unauth", "authz", "injection", "ssrf", "xss", "rce", "logic"},
	"ip":          {"host", "cred"},
	"root_domain": {"takeover", "host", "paths"},
	"subdomain":   {"takeover", "host", "paths"},
}

// assetValues 是补格优先级里的"资产价值"因子：主域/站点/API 面 > 普通子域 > 纯 IP。
var assetValues = map[string]int{
	"app":         5,
	"service":     5,
	"endpoint":    4,
	"root_domain": 4,
	"subdomain":   3,
	"ip":          3,
}

// AssetTypes 是全部合法资产类型（与 assets.type 的 CHECK 一致）。
var AssetTypes = []string{"root_domain", "ip", "subdomain", "app", "service", "endpoint"}

// IsAssetType reports whether t is a known asset type.
func IsAssetType(t string) bool {
	_, ok := assetValues[t]
	return ok
}

// IsArea reports whether name is a known risk area.
func IsArea(name string) bool {
	_, ok := areaByName[name]
	return ok
}

// AreaTitle 返回风险面的中文说明（未知面回退为原始值）。
func AreaTitle(name string) string {
	if a, ok := areaByName[name]; ok {
		return a.Title
	}
	return name
}

// AreaItems 返回某风险面的细分项（可能为空）。
func AreaItems(area string) []string {
	if a, ok := areaByName[area]; ok {
		return append([]string(nil), a.Items...)
	}
	return nil
}

// ItemAllowed reports whether item is a legal sub-item of area ('*' 永远合法)。
func ItemAllowed(area, item string) bool {
	if item == AreaAll {
		return true
	}
	a, ok := areaByName[area]
	if !ok {
		return false
	}
	for _, it := range a.Items {
		if it == item {
			return true
		}
	}
	return false
}

// DefaultRequiredAreas 返回某资产类型的默认必做面。
func DefaultRequiredAreas(assetType string) []string {
	areas, ok := defaultRequired[assetType]
	if !ok {
		return nil
	}
	return append([]string(nil), areas...)
}

// AssetValue 是补格优先级里的资产价值因子。
func AssetValue(assetType string) int {
	if v, ok := assetValues[assetType]; ok {
		return v
	}
	return 1
}

// AreaWeight 是补格优先级里的风险面权重。
func AreaWeight(area string) int {
	if a, ok := areaByName[area]; ok {
		return a.Weight
	}
	return 1
}

// AreaNames 返回全部风险面名（含顺序），供 UI 与提示词列举。
func AreaNames() []string {
	out := make([]string, 0, len(Areas))
	for _, a := range Areas {
		out = append(out, a.Name)
	}
	return out
}

// AreaLegend 生成 "authz=越权/IDOR, injection=…" 的一段说明，注入提示词。
func AreaLegend() string {
	out := ""
	for i, a := range Areas {
		if i > 0 {
			out += "；"
		}
		out += a.Name + "=" + a.Title
	}
	return out
}

// SortedAreas 返回按权重降序的面名（同权重按名字稳定排序）。
func SortedAreas() []string {
	names := AreaNames()
	sort.SliceStable(names, func(i, j int) bool {
		wi, wj := AreaWeight(names[i]), AreaWeight(names[j])
		if wi != wj {
			return wi > wj
		}
		return names[i] < names[j]
	})
	return names
}
