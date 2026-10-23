package srcmode

// SRC 平台/项目（P3）：一个 program = "某个平台上的某个项目"。
//
// 它把三样东西绑在一起：**范围**（给人和 agent 看的说明）、**规则**（禁用手法/写操作、
// 额度、命令级限额）、**交付口径**（等级映射）。任务创建时选一个 program，规则就被
// 编译进这个任务的 mode_doc 与探索策略——之后任务自己跑，不再回头读 program，
// 与 tasks.exploration_policy / mode_doc 的"创建时快照"语义一致。

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Program 是一个 SRC 平台项目（src_programs 的一行）。
type Program struct {
	ID          int64             `json:"id"`
	CompanyID   *int64            `json:"company_id,omitempty"`
	Name        string            `json:"name"`
	Platform    string            `json:"platform"`
	ScopeNote   string            `json:"scope_note"`
	Rules       ProgramRules      `json:"rules"`
	RateLimit   int               `json:"rate_limit"`
	TimeWindow  string            `json:"time_window"`
	SeverityMap map[string]string `json:"severity_map"`
}

// ProgramRule 是一条命令级规则（编译成探索策略里的 CustomRule）。
type ProgramRule struct {
	Label   string `json:"label"`
	Pattern string `json:"pattern"`
	Regex   bool   `json:"regex,omitempty"`
}

// ProgramRules 是 src_programs.rules 的形状（机器可读的平台规则）。
type ProgramRules struct {
	// 禁用手法 / 写操作：union 进本任务的探索策略（平台规则只能更严，不能更松）。
	ForbiddenTechniques []string `json:"forbidden_techniques,omitempty"`
	ForbiddenWrites     []string `json:"forbidden_writes,omitempty"`
	// 额度：取更小值（0 表示该项不限）。
	MaxIntentsTotal    int `json:"max_intents_total,omitempty"`
	MaxIntentsPerRound int `json:"max_intents_per_round,omitempty"`
	MaxOpenIntents     int `json:"max_open_intents,omitempty"`
	// 命令级规则：直接追加进策略的自定义禁止条目（调用方转成 policy.CustomRule）。
	CommandRules []ProgramRule `json:"command_rules,omitempty"`
	// 覆盖 SRC 模式默认值（nil = 用任务/模式默认）。
	AllowBudgetStop    *bool               `json:"allow_budget_stop,omitempty"`
	SkipReportGate     *bool               `json:"skip_report_gate,omitempty"`
	MaxAttemptsPerCell int                 `json:"max_attempts_per_cell,omitempty"`
	MaxIntentsPerAsset int                 `json:"max_intents_per_asset,omitempty"`
	RequiredAreas      map[string][]string `json:"required_areas,omitempty"`

	// ---- F2：收尾闸档位与平台红线 ----
	// CheckTier: full（默认）/ platform（平台档案档）/ off。
	CheckTier string `json:"check_tier,omitempty"`
	// CheckThresholds 按指标名覆盖内建门槛（如 {"paths":0,"manual_paths":1}）。
	CheckThresholds map[string]int `json:"check_thresholds,omitempty"`
	// NoAutomation 编译"禁扫描器/禁洪水"命令级规则（平台档案常见的硬约束）。
	NoAutomation bool `json:"no_automation,omitempty"`
	// Redlines 是平台原始红线，逐条进提示词（不自动编译：语义太细，拦不准会误伤）。
	Redlines []string `json:"redlines,omitempty"`
	// MaxRecordsRead 是越权读取上限（合规层提示用，不硬拦）。
	MaxRecordsRead int `json:"max_records_read,omitempty"`
	// RequireScreenshots 平台要求关键步骤截图时置真。
	RequireScreenshots bool `json:"require_screenshots,omitempty"`
	// StrictLayout 导出严格 8 块（备注退出正文）。
	StrictLayout bool `json:"strict_layout,omitempty"`
	// EgressIP / TestAccounts 是提交块字段。
	EgressIP     string `json:"egress_ip,omitempty"`
	TestAccounts string `json:"test_accounts,omitempty"`
}

// 平台等级（severity_map 的值域）。有些平台只有三档，有些有五档。
var platformLevels = map[string]bool{
	"info": true, "low": true, "medium": true, "high": true, "critical": true,
}

// Validate 校验一份 program（创建/更新时调用）。返回的错误直接回给创建页。
func (p *Program) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	p.Platform = strings.TrimSpace(p.Platform)
	p.TimeWindow = strings.TrimSpace(p.TimeWindow)
	if p.Name == "" {
		return fmt.Errorf("项目名不能为空")
	}
	if p.RateLimit < 0 {
		return fmt.Errorf("rate_limit 不能为负")
	}
	if p.RateLimit > 1000 {
		return fmt.Errorf("rate_limit 不能超过 1000（平台不会允许这么高）")
	}
	if err := validateTimeWindow(p.TimeWindow); err != nil {
		return err
	}
	for _, tech := range p.Rules.ForbiddenTechniques {
		if strings.TrimSpace(tech) == "" {
			return fmt.Errorf("forbidden_techniques 里有空项")
		}
	}
	for i := range p.Rules.CommandRules {
		r := &p.Rules.CommandRules[i]
		r.Pattern = strings.TrimSpace(r.Pattern)
		if r.Pattern == "" {
			return fmt.Errorf("command_rules[%d] 缺 pattern", i)
		}
		if strings.TrimSpace(r.Label) == "" {
			r.Label = r.Pattern
		}
	}
	if p.Rules.MaxAttemptsPerCell < 0 || p.Rules.MaxIntentsPerAsset < 0 ||
		p.Rules.MaxIntentsTotal < 0 || p.Rules.MaxIntentsPerRound < 0 || p.Rules.MaxOpenIntents < 0 {
		return fmt.Errorf("额度不能为负")
	}
	if areasByType := p.Rules.RequiredAreas; areasByType != nil {
		for assetType, areas := range areasByType {
			if !IsAssetType(assetType) {
				return fmt.Errorf("未知资产类型：%s", assetType)
			}
			for _, area := range areas {
				if !IsArea(area) {
					return fmt.Errorf("未知风险面：%s（资产类型 %s）", area, assetType)
				}
			}
		}
	}
	for internal, level := range p.SeverityMap {
		if !isInternalSeverity(internal) {
			return fmt.Errorf("severity_map 的键必须是 critical/high/medium/low：%s", internal)
		}
		lv := strings.ToLower(strings.TrimSpace(level))
		if !platformLevels[lv] {
			return fmt.Errorf("severity_map[%s] 的平台等级不合法：%s", internal, level)
		}
		p.SeverityMap[internal] = lv
	}
	switch strings.TrimSpace(p.Rules.CheckTier) {
	case "", CheckTierFull, CheckTierPlatform, CheckTierOff:
	default:
		return fmt.Errorf("check_tier 只能是 full / platform / off：%s", p.Rules.CheckTier)
	}
	for metric, v := range p.Rules.CheckThresholds {
		if !IsCheckMetric(metric) {
			return fmt.Errorf("check_thresholds 里有未知指标：%s（%s）", metric, strings.Join(CheckMetricKeys(), " / "))
		}
		if v < 0 {
			return fmt.Errorf("check_thresholds[%s] 不能为负", metric)
		}
	}
	if p.Rules.MaxRecordsRead < 0 {
		return fmt.Errorf("max_records_read 不能为负")
	}
	if len(p.Rules.Redlines) > 30 {
		return fmt.Errorf("redlines 最多 30 条（当前 %d 条）", len(p.Rules.Redlines))
	}
	kept := make([]string, 0, len(p.Rules.Redlines))
	for _, r := range p.Rules.Redlines {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		kept = append(kept, r)
	}
	p.Rules.Redlines = kept
	p.Rules.EgressIP = strings.TrimSpace(p.Rules.EgressIP)
	p.Rules.TestAccounts = strings.TrimSpace(p.Rules.TestAccounts)
	if len(p.Rules.EgressIP) > 64 || strings.ContainsAny(p.Rules.EgressIP, " \t\n") {
		return fmt.Errorf("egress_ip 不合法：%q", p.Rules.EgressIP)
	}
	return nil
}

// ApplyToModeDoc 把 program 的规则编译进任务模式快照（调用方在创建任务时用）。
// 规则只会让任务更严：额度取更小、必做面以 program 为准（平台要求什么就测什么）。
func (p *Program) ApplyToModeDoc(doc ModeDoc) ModeDoc {
	out := doc
	if p.Platform != "" {
		out.Platform = p.Platform
	}
	if p.Name != "" {
		out.Program = p.Name
	}
	if r := p.Rules.MaxAttemptsPerCell; r > 0 {
		out.MaxAttemptsPerCell = r
	}
	if r := p.Rules.MaxIntentsPerAsset; r > 0 {
		out.MaxIntentsPerAsset = r
	}
	if len(p.Rules.RequiredAreas) > 0 {
		merged := make(map[string][]string, len(out.RequiredAreas)+len(p.Rules.RequiredAreas))
		for k, v := range out.RequiredAreas {
			merged[k] = v
		}
		for k, v := range p.Rules.RequiredAreas {
			merged[k] = v
		}
		out.RequiredAreas = merged
	}
	if p.Rules.AllowBudgetStop != nil {
		out.AllowBudgetStop = *p.Rules.AllowBudgetStop
	}
	if p.Rules.SkipReportGate != nil {
		out.SkipReportGate = *p.Rules.SkipReportGate
	}
	// F2：收尾闸档位 / 阈值 / 截图 / 严格版式 / 提交块字段。
	if t := strings.TrimSpace(p.Rules.CheckTier); t != "" {
		out.CheckTier = t
	}
	if len(p.Rules.CheckThresholds) > 0 {
		merged := make(map[string]int, len(out.CheckThresholds)+len(p.Rules.CheckThresholds))
		for k, v := range out.CheckThresholds {
			merged[k] = v
		}
		for k, v := range p.Rules.CheckThresholds {
			merged[k] = v
		}
		out.CheckThresholds = merged
	}
	if p.Rules.RequireScreenshots {
		out.RequireScreenshots = true
	}
	if p.Rules.StrictLayout {
		out.StrictLayout = true
	}
	if p.Rules.MaxRecordsRead > 0 {
		out.MaxRecordsRead = p.Rules.MaxRecordsRead
	}
	if p.Rules.EgressIP != "" {
		out.EgressIP = p.Rules.EgressIP
	}
	if p.Rules.TestAccounts != "" {
		out.TestAccounts = p.Rules.TestAccounts
	}
	if len(p.Rules.Redlines) > 0 {
		out.PlatformRedlines = append(append([]string(nil), out.PlatformRedlines...), p.Rules.Redlines...)
	}
	return out
}

// NoAutomationRules 把"平台禁止自动化扫描"编译成命令级规则。
// 只在项目显式开启时生效：非平台系目标仍按全量档测（技能 §0.1 明确"非平台系不打折"）。
func (p *Program) NoAutomationRules() []ProgramRule {
	if !p.Rules.NoAutomation {
		return nil
	}
	return []ProgramRule{
		{Label: "平台禁止自动化扫描器（只允许手工测试）",
			Pattern: `(?i)(^|[;&|]\s*)(\S*[/\\])?(nuclei|acunetix|awvs|nessus|openvas|masscan|zmap|sqlmap)(\s|$)`, Regex: true},
		{Label: "平台禁止全端口/高强度扫描",
			Pattern: `(?i)nmap\s+[^\n]*(-p\s*-|--top-ports|--min-rate|--max-rate|--script)`, Regex: true},
		{Label: "平台禁止字典爆破",
			Pattern: `(?i)(ffuf|dirsearch|gobuster|dirb|feroxbuster|wfuzz)\b[^\n]*(-w|--wordlist|--wordlists)`, Regex: true},
	}
}

// PlatformLevel 把内部等级映射成平台等级（没配映射时原样返回）。
func (p *Program) PlatformLevel(internal string) string {
	if len(p.SeverityMap) == 0 {
		return ""
	}
	if v, ok := p.SeverityMap[strings.ToLower(strings.TrimSpace(internal))]; ok {
		return v
	}
	return ""
}

// RateLimitRules 把"每秒请求上限"编译成命令级硬规则：平台限速在提示词里只是提醒，
// 到命令层才是拦得住的。只覆盖最常见的洪水开关，不做穷举（穷举等于假装能拦全）。
func (p *Program) RateLimitRules() []ProgramRule {
	if p.RateLimit <= 0 {
		return nil
	}
	limit := strconv.Itoa(p.RateLimit)
	return []ProgramRule{
		{Label: "平台限速 " + limit + " qps：禁止洪水模式", Pattern: "--flood"},
		{Label: "平台限速 " + limit + " qps：禁止高并发（-t/--threads ≥ 50）", Pattern: `(^|\s)(-t|--threads)[= ]([5-9][0-9]|[0-9]{3,})(\s|$)`, Regex: true},
		{Label: "平台限速 " + limit + " qps：禁止高频率/不限速开关", Pattern: `(--rate|--min-rate|--max-rate)[= ]([0-9]{3,}|\d+\.\d+[kK])`, Regex: true},
	}
}

// Describe 生成给提示词用的一行说明（写进 SRC 覆盖段的末尾）。
func (p *Program) Describe() string {
	parts := []string{}
	if p.Platform != "" {
		parts = append(parts, "平台："+p.Platform)
	}
	if p.Name != "" {
		parts = append(parts, "项目："+p.Name)
	}
	if p.RateLimit > 0 {
		parts = append(parts, fmt.Sprintf("限速 ≤ %d 请求/秒", p.RateLimit))
	}
	if p.TimeWindow != "" {
		parts = append(parts, "作业时间窗 "+p.TimeWindow)
	}
	if s := strings.TrimSpace(p.ScopeNote); s != "" {
		parts = append(parts, "范围说明："+oneLineText(s, 200))
	}
	if len(parts) == 0 {
		return ""
	}
	return "【平台规则】" + strings.Join(parts, "；") + "。超出这些限制的动作一律不做——被平台封号/拒收的代价远大于多测一个面。"
}

// validateTimeWindow 校验 "HH:MM-HH:MM[,HH:MM-HH:MM]"（允许跨夜，例如 22:00-02:00）。
func validateTimeWindow(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	for _, seg := range strings.Split(raw, ",") {
		seg = strings.TrimSpace(seg)
		from, to, ok := strings.Cut(seg, "-")
		if !ok {
			return fmt.Errorf("时间窗格式应为 HH:MM-HH:MM：%q", seg)
		}
		if _, _, err := parseHHMM(from); err != nil {
			return fmt.Errorf("时间窗起点不合法：%q", from)
		}
		if _, _, err := parseHHMM(to); err != nil {
			return fmt.Errorf("时间窗终点不合法：%q", to)
		}
	}
	return nil
}

func parseHHMM(raw string) (hour, minute int, err error) {
	raw = strings.TrimSpace(raw)
	h, m, ok := strings.Cut(raw, ":")
	if !ok {
		return 0, 0, fmt.Errorf("缺冒号")
	}
	hour, err = strconv.Atoi(h)
	if err != nil {
		return 0, 0, err
	}
	minute, err = strconv.Atoi(m)
	if err != nil {
		return 0, 0, err
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("越界")
	}
	return hour, minute, nil
}

// InWindow 判断一个时刻是否落在作业时间窗内（空窗口 = 不限）。跨夜窗口按"或"处理。
func (p *Program) InWindow(at time.Time) bool {
	raw := strings.TrimSpace(p.TimeWindow)
	if raw == "" {
		return true
	}
	minutes := at.Hour()*60 + at.Minute()
	for _, seg := range strings.Split(raw, ",") {
		from, to, ok := strings.Cut(strings.TrimSpace(seg), "-")
		if !ok {
			continue
		}
		fh, fm, err1 := parseHHMM(from)
		th, tm, err2 := parseHHMM(to)
		if err1 != nil || err2 != nil {
			continue
		}
		start, end := fh*60+fm, th*60+tm
		if start <= end {
			if minutes >= start && minutes <= end {
				return true
			}
			continue
		}
		// 跨夜：22:00-02:00 = [22:00,24:00) ∪ [00:00,02:00]
		if minutes >= start || minutes <= end {
			return true
		}
	}
	return false
}

func isInternalSeverity(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical", "high", "medium", "low":
		return true
	}
	return false
}

func oneLineText(s string, max int) string {
	out := strings.Join(strings.Fields(s), " ")
	if r := []rune(out); len(r) > max {
		return string(r[:max]) + "…"
	}
	return out
}
