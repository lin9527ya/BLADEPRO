// Package srcmode 实现"SRC 挖掘"这一任务模式（tasks.mode='src'）。
//
// 设计取舍（见 docs/SRC挖掘agent改造方案.md）：不做 fork、不做通用插件内核，
// 而是把 SRC 当成一套**运行配置**——规划层按"补格"派意图、判定层用显式覆盖格
// 做收尾硬门、交付层出可提交件。本包只负责其中的"模式定义 + 判定"两件事，
// 刻意不 import server（避免循环依赖）：任务字段由调用方传入。
package srcmode

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ModeSrc 是 SRC 挖掘的模式标识；tasks.mode 为空表示普通渗透（行为与改造前一致）。
const ModeSrc = "src"

// 预算默认值与硬上限。上限存在的意义是防止一条 mode_doc 把任务变成无限循环。
const (
	DefaultMaxAttemptsPerCell = 2
	DefaultMaxIntentsPerAsset = 12
	maxAttemptsLimit          = 10
	maxIntentsPerAssetLimit   = 500
	maxAssetTokenBudget       = 1_000_000_000_000
	// maxAssetTimeBudgetMinutes caps the per-asset wall-clock budget (30 days).
	maxAssetTimeBudgetMinutes = 30 * 24 * 60
	DocVersion                = 1
)

// ModeDoc 是任务的模式快照（tasks.mode_doc）。创建时解析、运行中只读——
// 与 ExplorationPolicy 同一套快照语义：改全局默认不影响已建任务。
type ModeDoc struct {
	DocVersion int    `json:"doc_version"`
	Mode       string `json:"mode"`
	// Platform / Program 只是提交台账与报告备注用的标签（P2 起真正使用）。
	Platform string `json:"platform,omitempty"`
	Program  string `json:"program,omitempty"`
	// PlatformTemplate 是提交块模板（generic/butian/vulbox/edu/hackerone）。
	// 空 = 按 Platform 标签自动识别，识别不出用 generic。
	PlatformTemplate string `json:"platform_template,omitempty"`
	// ProgramNote 是创建任务时按平台项目生成的一行规则说明（P3）：平台、限额、
	// 时间窗、范围说明。它进提示词，让 agent 从一开始就知道这套边界。
	ProgramNote string `json:"program_note,omitempty"`
	// RequiredAreas 覆盖"资产类型 → 必做风险面"的默认映射；只允许收窄/新增已知类型。
	RequiredAreas map[string][]string `json:"required_areas,omitempty"`
	// AllowBudgetStop=true 时 budget_stop 也算终态（放行收尾），但报告备注必须写明缺口。
	AllowBudgetStop bool `json:"allow_budget_stop"`
	// MaxAttemptsPerCell 是同一格的尝试上限；超过必须 budget_stop 并写原因。
	MaxAttemptsPerCell int `json:"max_attempts_per_cell"`
	// MaxIntentsPerAsset 防止单个资产吃掉整任务的意图预算。
	MaxIntentsPerAsset int `json:"max_intents_per_asset"`
	// AssetTokenBudget 是单个资产的 token 预算（输入+输出，0 = 不限）。达到上限后：
	// 新意图会被 add_intent 拒绝，引擎把该资产未完成的格自动标成 budget_stop，
	// 并向 planner 投递一次提示，让预算转向其他资产。
	AssetTokenBudget int64 `json:"asset_token_budget,omitempty"`
	// AssetTimeBudgetMinutes 是单个资产的墙钟预算（分钟，0 = 不限）。从该资产第一次
	// 产生 LLM 用量起算；到点后与 token 预算同样收口：未完成格转 budget_stop，
	// 新意图被拒，预算转向其他资产。时间预算不因暂停/等待人工而暂停——它约束的是
	// 「这个资产占用了任务多久」，不是模型实际思考时长。
	AssetTimeBudgetMinutes int `json:"asset_time_budget_minutes,omitempty"`
	// AssetCostBudgetUSD 是单个资产的美元成本上限（0 = 不限），按 llm_usage.cost_usd
	// 累计（调用时 profile 单价折算）。与 token/时间预算同样收口并转向其他资产。
	AssetCostBudgetUSD float64 `json:"asset_cost_budget_usd,omitempty"`
	// HardItems 是"必须逐个点亮"的细分项（如 paths/enum-200）；不写则每个面只需 '*'。
	HardItems []string `json:"hard_items,omitempty"`
	// SkipReportGate 关掉"medium+ 发现必须齐备可提交件"这条收尾门（默认**要求**）。
	// 只给"先跑覆盖、交付另开任务"的用法留出口：关掉后任务可以在报告没写好时收尾。
	SkipReportGate bool `json:"skip_report_gate,omitempty"`
	// CheckTier 是收尾闸档位：full（默认，全量门槛）/ platform（平台档案档，
	// 技能 site-close-checklist §0.1：只豁免批量枚举，不豁免测试深度）/ off（关闸）。
	CheckTier string `json:"check_tier,omitempty"`
	// CheckThresholds 按指标名覆盖内建门槛（例如 {"paths":0,"manual_paths":1}）。
	CheckThresholds map[string]int `json:"check_thresholds,omitempty"`
	// SkipCheckGate 一刀关掉收尾闸（应急出口；正常项目应当用 check_tier 表达档位）。
	SkipCheckGate bool `json:"skip_check_gate,omitempty"`
	// RequireScreenshots 平台要求关键步骤截图时置真：收尾项 screenshots 不再允许 na。
	RequireScreenshots bool `json:"require_screenshots,omitempty"`
	// StrictLayout：导出严格 8 块（备注退出正文，挪进台账 note）。
	StrictLayout bool `json:"strict_layout,omitempty"`
	// EgressIP / TestAccounts 是提交块字段（vuln-report-format §〇）。
	EgressIP     string `json:"egress_ip,omitempty"`
	TestAccounts string `json:"test_accounts,omitempty"`
	// MaxRecordsRead 是越权读取上限（合规提示用；0 = 用内建默认 5）。
	MaxRecordsRead int `json:"max_records_read,omitempty"`
	// TimeWindow 是平台项目的作业时间窗快照（"HH:MM-HH:MM"，逗号分段，允许跨夜）。
	// 空 = 不限；guard 会在窗口外直接拦下动作（不只是提示词约束）。
	TimeWindow string `json:"time_window,omitempty"`
	// AllowReasonOnlyClear 允许 tested_clear 只写说明、不挂证据节点。默认 false：
	// "没证据的 clear 等于没测"；只有明确接受人工复核的档位才应打开。
	AllowReasonOnlyClear bool `json:"allow_reason_only_clear,omitempty"`
	// RequireRetest 要求 medium+ 发现在准备交付件前必须有一次独立复验
	// （finding_retests.verdict=reproduced）。默认 true；program 可显式关闭。
	RequireRetest bool `json:"require_retest"`
	// RateLimit 是确定性探针的出站 QPS 上限（0 = 用内建保守值 5）。
	RateLimit int `json:"rate_limit,omitempty"`
	// AreaROI 是按风险面的历史产出率乘子（0.5–1.5，创建任务时按台账快照）。
	// 用于补格排序：高产出面优先，长期无产出的面降权。缺省 = 1。
	AreaROI map[string]float64 `json:"area_roi,omitempty"`
	// AreaTiers 是按风险面的模型分层（cheap/standard/strong）。空 = 用内建默认
	// （有输出价值的面 strong，清点/枚举类 cheap）。只有在 llm_auto_tier 打开、
	// 且任务没有显式 worker 绑定时才生效；显式配置始终优先。
	AreaTiers map[string]string `json:"area_tiers,omitempty"`
	// PlatformExemptAreas 是 program 显式声明的平台豁免面：只有列在这里的面才允许
	// na_code=platform_exempt，避免把"平台不测"当成万能 N/A。
	PlatformExemptAreas []string `json:"platform_exempt_areas,omitempty"`
	// PlatformRedlines 是平台原始红线（逐条进提示词，不自动编译成规则）。
	PlatformRedlines []string `json:"platform_redlines,omitempty"`
}

// DefaultDoc 返回 SRC 模式的默认快照。
func DefaultDoc() ModeDoc {
	return ModeDoc{
		DocVersion:         DocVersion,
		Mode:               ModeSrc,
		AllowBudgetStop:    false,
		MaxAttemptsPerCell: DefaultMaxAttemptsPerCell,
		MaxIntentsPerAsset: DefaultMaxIntentsPerAsset,
		RequireRetest:      true,
	}
}

// Resolve 校验并解析一份模式文档，返回要落库的快照（JSON）。mode="" 表示普通
// 渗透，返回 nil 快照且不报错；未知 mode、未知 area、越界预算一律拒绝——模式是
// 创建时定死的，宁可在创建页报错，也不要在运行到一半才发现配置写错。
func Resolve(mode string, raw json.RawMessage) (json.RawMessage, ModeDoc, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return nil, ModeDoc{}, nil
	}
	if mode != ModeSrc {
		return nil, ModeDoc{}, fmt.Errorf("未知任务模式：%s", mode)
	}
	doc := DefaultDoc()
	if len(strings.TrimSpace(string(raw))) > 0 && string(raw) != "null" {
		// 未知字段忽略：模式文档会随版本增加字段，旧客户端传来的多余键不该让创建失败。
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, ModeDoc{}, fmt.Errorf("模式配置解析失败：%w", err)
		}
	}
	doc.Mode = ModeSrc
	doc.DocVersion = DocVersion
	if doc.MaxAttemptsPerCell <= 0 {
		doc.MaxAttemptsPerCell = DefaultMaxAttemptsPerCell
	}
	if doc.MaxIntentsPerAsset <= 0 {
		doc.MaxIntentsPerAsset = DefaultMaxIntentsPerAsset
	}
	if doc.MaxAttemptsPerCell > maxAttemptsLimit {
		return nil, ModeDoc{}, fmt.Errorf("max_attempts_per_cell 不能超过 %d", maxAttemptsLimit)
	}
	if doc.MaxIntentsPerAsset > maxIntentsPerAssetLimit {
		return nil, ModeDoc{}, fmt.Errorf("max_intents_per_asset 不能超过 %d", maxIntentsPerAssetLimit)
	}
	if doc.AssetTokenBudget < 0 {
		return nil, ModeDoc{}, fmt.Errorf("asset_token_budget 不能为负数（0 = 不限）")
	}
	if doc.AssetTokenBudget > maxAssetTokenBudget {
		return nil, ModeDoc{}, fmt.Errorf("asset_token_budget 不能超过 %d", int64(maxAssetTokenBudget))
	}
	if doc.AssetTimeBudgetMinutes < 0 {
		return nil, ModeDoc{}, fmt.Errorf("asset_time_budget_minutes 不能为负数（0 = 不限）")
	}
	if doc.AssetTimeBudgetMinutes > maxAssetTimeBudgetMinutes {
		return nil, ModeDoc{}, fmt.Errorf("asset_time_budget_minutes 不能超过 %d（30 天）", maxAssetTimeBudgetMinutes)
	}
	if doc.AssetCostBudgetUSD < 0 {
		return nil, ModeDoc{}, fmt.Errorf("asset_cost_budget_usd 不能为负数（0 = 不限）")
	}
	if doc.AssetCostBudgetUSD > 1_000_000 {
		return nil, ModeDoc{}, fmt.Errorf("asset_cost_budget_usd 不能超过 1000000")
	}
	for assetType, areas := range doc.RequiredAreas {
		if !IsAssetType(assetType) {
			return nil, ModeDoc{}, fmt.Errorf("未知资产类型：%s", assetType)
		}
		for _, area := range areas {
			if !IsArea(area) {
				return nil, ModeDoc{}, fmt.Errorf("未知风险面：%s（资产类型 %s）", area, assetType)
			}
		}
		if len(areas) == 0 {
			return nil, ModeDoc{}, fmt.Errorf("资产类型 %s 的必做面不能为空", assetType)
		}
	}
	for _, item := range doc.HardItems {
		if _, _, err := SplitItem(item); err != nil {
			return nil, ModeDoc{}, err
		}
	}
	if t := strings.TrimSpace(doc.PlatformTemplate); t != "" {
		// custom: 前缀留给用户自定义模板；存在性由 server 在创建任务时查库校验。
		if _, ok := FindPlatformTemplate(t); !ok && !strings.HasPrefix(strings.ToLower(t), "custom:") {
			return nil, ModeDoc{}, fmt.Errorf("未知平台模板：%s（可选：%s，或 custom:<key>）", t, PlatformTemplateKeys())
		}
		doc.PlatformTemplate = strings.ToLower(t)
	}
	for _, area := range doc.PlatformExemptAreas {
		if !IsArea(strings.TrimSpace(area)) {
			return nil, ModeDoc{}, fmt.Errorf("未知的 platform_exempt_areas 风险面：%s", area)
		}
	}
	for area, tier := range doc.AreaTiers {
		if !IsArea(strings.TrimSpace(area)) {
			return nil, ModeDoc{}, fmt.Errorf("未知 area_tiers 风险面：%s", area)
		}
		switch strings.TrimSpace(tier) {
		case "cheap", "standard", "strong":
		default:
			return nil, ModeDoc{}, fmt.Errorf("area_tiers[%s] 只能是 cheap / standard / strong", area)
		}
	}
	for area, mult := range doc.AreaROI {
		if !IsArea(strings.TrimSpace(area)) {
			return nil, ModeDoc{}, fmt.Errorf("未知的 area_roi 风险面：%s", area)
		}
		if mult < 0.1 || mult > 5 {
			return nil, ModeDoc{}, fmt.Errorf("area_roi[%s] 超出 0.1–5：%v", area, mult)
		}
	}
	switch strings.TrimSpace(doc.CheckTier) {
	case "", CheckTierFull, CheckTierPlatform, CheckTierOff:
	default:
		return nil, ModeDoc{}, fmt.Errorf("未知收尾闸档位：%s（full / platform / off）", doc.CheckTier)
	}
	for metric, v := range doc.CheckThresholds {
		if !IsCheckMetric(metric) {
			return nil, ModeDoc{}, fmt.Errorf("未知收尾闸指标：%s（%s）", metric, strings.Join(CheckMetricKeys(), " / "))
		}
		if v < 0 {
			return nil, ModeDoc{}, fmt.Errorf("收尾闸指标 %s 不能为负", metric)
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, ModeDoc{}, err
	}
	return out, doc, nil
}

// Parse 读取一份已落库的模式快照。解析失败或为空时返回零值 ModeDoc——调用方用
// doc.IsSrc() 判断，坏快照只会退化成"普通渗透"，不会让任务跑不起来。
func Parse(raw json.RawMessage) ModeDoc {
	doc := DefaultDoc()
	if len(strings.TrimSpace(string(raw))) == 0 || string(raw) == "null" {
		return doc
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return DefaultDoc()
	}
	if doc.MaxAttemptsPerCell <= 0 {
		doc.MaxAttemptsPerCell = DefaultMaxAttemptsPerCell
	}
	if doc.MaxIntentsPerAsset <= 0 {
		doc.MaxIntentsPerAsset = DefaultMaxIntentsPerAsset
	}
	return doc
}

// IsSrc reports whether this snapshot describes the SRC mode.
func (d ModeDoc) IsSrc() bool { return d.Mode == ModeSrc }

// PlatformExemptAllowed reports whether the program snapshot explicitly exempts
// this risk area from testing (the only basis for na_code=platform_exempt).
func (d ModeDoc) PlatformExemptAllowed(area string) bool {
	area = strings.TrimSpace(area)
	for _, a := range d.PlatformExemptAreas {
		if strings.TrimSpace(a) == area {
			return true
		}
	}
	return false
}

// RequiredFor 返回某资产类型的必做风险面（mode_doc 覆盖优先，否则用 taxonomy 默认）。
func (d ModeDoc) RequiredFor(assetType string) []string {
	if areas, ok := d.RequiredAreas[assetType]; ok && len(areas) > 0 {
		return areas
	}
	return DefaultRequiredAreas(assetType)
}

// HardItemsFor 返回某风险面下必须逐个点亮的细分项（可能为空）。
func (d ModeDoc) HardItemsFor(area string) []string {
	var out []string
	for _, item := range d.HardItems {
		a, i, err := SplitItem(item)
		if err != nil || a != area {
			continue
		}
		out = append(out, i)
	}
	sort.Strings(out)
	return out
}

// SplitItem 拆分 "area/item" 形式的硬性细分项。
func SplitItem(raw string) (area, item string, err error) {
	raw = strings.TrimSpace(raw)
	area, item, ok := strings.Cut(raw, "/")
	area, item = strings.TrimSpace(area), strings.TrimSpace(item)
	if !ok || area == "" || item == "" {
		return "", "", fmt.Errorf("hard_items 项必须写成 area/item：%q", raw)
	}
	if !IsArea(area) {
		return "", "", fmt.Errorf("未知风险面：%s", area)
	}
	if !ItemAllowed(area, item) {
		return "", "", fmt.Errorf("风险面 %s 没有 %s 这个细分项", area, item)
	}
	return area, item, nil
}
