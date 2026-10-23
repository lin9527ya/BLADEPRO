package srcmode

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/lin9527ya/bladepro/db"
)

// 收尾闸（closure checks）——来自 src-dig 的 rules/site-close-checklist.md。
//
// 为什么面和收尾闸要分开：`src_cells` 回答"这个风险面测过没有"，
// 收尾闸回答"测够没有"——JS 抽全没有、路径枚举够不够 200 条、非 404 响应有没有
// 慢速复测、方法矩阵做没做、参数变体做没做、声称的防护有没有对照实验。
// 技能里这两层都要过；缺一层就会出现"每个面都点了，但只读了首页内联 JS"这种假完成。
//
// 每一项都必须带**数字**（metrics）与**证据节点**：技能的原话是"缺项 = 没测，不是
// 测了没有"，所以 done 不接受一句"做过了"。

// MetricDef 是一个需要在 metrics 里填的数字（UI 据此渲染输入框）。
type MetricDef struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Min   int    `json:"min"` // 仅用于 UI 提示；真实门槛见 CheckDef.MinFull/MinPlatform
}

// CheckDef 是一项收尾闸的定义。
type CheckDef struct {
	Key        string      `json:"key"`
	Title      string      `json:"title"`
	Hint       string      `json:"hint"`
	AssetTypes []string    `json:"asset_types"`
	Weight     int         `json:"weight"`
	Metrics    []MetricDef `json:"metrics"`
	// MinFull / MinPlatform：同一项在不同档位下的数字门槛（技能 §0.1 平台档豁免）。
	MinFull     map[string]int `json:"-"`
	MinPlatform map[string]int `json:"-"`
	// NaCodes 是允许的 N/A 理由码（空 = 不接受 N/A）。
	NaCodes []string `json:"na_codes,omitempty"`
	// RequireEvidence：done 必须挂证据节点（fact）。
	RequireEvidence bool `json:"require_evidence"`
}

// 收尾项在报告/矩阵里的伪风险面名。
const ClosureArea = "closure"

// 档位。
const (
	CheckTierFull     = "full"
	CheckTierPlatform = "platform"
	CheckTierOff      = "off"
)

// CheckDefs 是内建收尾闸目录。顺序即展示顺序。
var CheckDefs = []CheckDef{
	{
		Key: "js_enum", Title: "JS 抽全",
		Hint:       "内联 <script> + 外链 .js + 异步 chunk（*.async.js / __webpack_require__.e）+ sourcemap。CDN 拦死可降级：填 degraded=1 并写清降级原因。",
		AssetTypes: []string{"app", "service", "endpoint"}, Weight: 30,
		Metrics: []MetricDef{
			{Key: "js_inline", Label: "内联脚本数"},
			{Key: "js_external", Label: "外链 JS 数"},
			{Key: "js_chunks", Label: "异步 chunk 数"},
			{Key: "sourcemap", Label: "sourcemap 数"},
			{Key: "degraded", Label: "降级(1=是)"},
		},
		MinFull:         map[string]int{"js_inline": 0},
		MinPlatform:     map[string]int{"js_inline": 0},
		RequireEvidence: true,
	},
	{
		Key: "paths_enum", Title: "路径枚举",
		Hint:       "全量档 ≥200 条（通用字典 + 按应用命名扩展）；平台档改手工等价物：从 JS/网关/回包手工列出真实引用过的 path（manual_paths）。",
		AssetTypes: []string{"app", "service"}, Weight: 25,
		Metrics: []MetricDef{
			{Key: "paths", Label: "枚举路径数"},
			{Key: "manual_paths", Label: "手工整理路径数"},
		},
		MinFull:         map[string]int{"paths": 200},
		MinPlatform:     map[string]int{"manual_paths": 1},
		RequireEvidence: true,
	},
	{
		Key: "non404_retest", Title: "非 404 复测",
		Hint:       "扫出的 502/500/403/EOF 必须慢速复测 ≥2 次（快扫的 502/EOF 常是限速假象）。没有非 404 响应时填 non404=0。",
		AssetTypes: []string{"app", "service"}, Weight: 25,
		Metrics: []MetricDef{
			{Key: "non404", Label: "非 404 响应数"},
			{Key: "retested", Label: "已复测条数"},
		},
		MinFull:         map[string]int{"retested": 2},
		MinPlatform:     map[string]int{"retested": 2},
		RequireEvidence: true,
	},
	{
		Key: "method_matrix", Title: "方法矩阵",
		Hint:       "每个端点测 GET/POST/PUT/DELETE/PATCH/OPTIONS/HEAD（至少状态变更类全测）；记录 Allow 与 405 行为。平台档只对状态变更端点手工换方法。",
		AssetTypes: []string{"app", "service", "endpoint"}, Weight: 20,
		Metrics: []MetricDef{
			{Key: "methods", Label: "已测方法数"},
			{Key: "state_methods", Label: "状态变更端点已测方法数"},
		},
		MinFull:         map[string]int{"methods": 7},
		MinPlatform:     map[string]int{"state_methods": 1},
		RequireEvidence: true,
	},
	{
		Key: "param_variants", Title: "参数变体",
		Hint:       "身份/租户/角色/环境类参数逐个换值、置空、删参；补参推进（报错点名缺哪个字段→补上）；同名重复/类型/大小写/编码变体。",
		AssetTypes: []string{"app", "service", "endpoint"}, Weight: 25,
		Metrics: []MetricDef{
			{Key: "variants", Label: "做过的变体类型数"},
		},
		MinFull:         map[string]int{"variants": 4},
		MinPlatform:     map[string]int{"variants": 4},
		NaCodes:         []string{"no_business_param", "no_object"},
		RequireEvidence: true,
	},
	{
		Key: "defense_contrast", Title: "防护对照",
		Hint:       "声称的每一项防护都要有对照实验数据：验证码触发阈值、锁定条件、限频响应变化、白名单拒绝原文、枚举有无差异、签名空/假/乱三种输入。",
		AssetTypes: []string{"app", "service"}, Weight: 20,
		Metrics: []MetricDef{
			{Key: "contrasts", Label: "对照实验数"},
		},
		MinFull:         map[string]int{"contrasts": 1},
		MinPlatform:     map[string]int{"contrasts": 1},
		NaCodes:         []string{"no_defense_claim"},
		RequireEvidence: true,
	},
	{
		Key: "unreachable_proof", Title: "判死三条件",
		Hint:       "只有不可达资产需要：本地不可达 + ≥2 个外部出口复测 + ≥2 个公共 DNS 解析器核对。可达资产填 na(reachable)。",
		AssetTypes: []string{"ip", "root_domain", "subdomain"}, Weight: 15,
		Metrics: []MetricDef{
			{Key: "local_unreachable", Label: "本地不可达(1=是)"},
			{Key: "external_exits", Label: "外部出口数"},
			{Key: "dns_resolvers", Label: "DNS 解析器数"},
		},
		MinFull:         map[string]int{"external_exits": 2, "dns_resolvers": 2},
		MinPlatform:     map[string]int{"external_exits": 2, "dns_resolvers": 2},
		NaCodes:         []string{"reachable"},
		RequireEvidence: true,
	},
	{
		Key: "screenshots", Title: "证据截图",
		Hint:       "平台要求截图时（program.require_screenshots），把截图产物登记为证据节点；不要求时填 na。",
		AssetTypes: []string{"app", "service"}, Weight: 10,
		MinFull:         map[string]int{},
		MinPlatform:     map[string]int{},
		NaCodes:         []string{"platform_not_required", "not_applicable"},
		RequireEvidence: true,
	},
}

var checkByKey = func() map[string]CheckDef {
	m := make(map[string]CheckDef, len(CheckDefs))
	for _, c := range CheckDefs {
		m[c.Key] = c
	}
	return m
}()

// FindCheck 按 key 取收尾项定义。
func FindCheck(key string) (CheckDef, bool) {
	c, ok := checkByKey[strings.TrimSpace(key)]
	return c, ok
}

// CheckKeys 返回全部收尾项 key（顺序即展示顺序）。
func CheckKeys() []string {
	out := make([]string, 0, len(CheckDefs))
	for _, c := range CheckDefs {
		out = append(out, c.Key)
	}
	return out
}

// ChecksFor 返回某资产类型适用的收尾项（按目录顺序）。
func ChecksFor(assetType string) []CheckDef {
	out := make([]CheckDef, 0, 4)
	for _, c := range CheckDefs {
		for _, t := range c.AssetTypes {
			if t == assetType {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// Tier 返回本次任务采用的档位（缺省 full）。
func (d ModeDoc) Tier() string {
	switch strings.TrimSpace(d.CheckTier) {
	case CheckTierPlatform:
		return CheckTierPlatform
	case CheckTierOff:
		return CheckTierOff
	default:
		return CheckTierFull
	}
}

// Threshold 返回某指标在本次档位下的门槛（mode_doc 覆盖优先）。
func (d ModeDoc) Threshold(metric string) int {
	if v, ok := d.CheckThresholds[strings.TrimSpace(metric)]; ok {
		return v
	}
	tier := d.Tier()
	for _, c := range CheckDefs {
		if tier == CheckTierPlatform {
			if v, ok := c.MinPlatform[metric]; ok {
				return v
			}
			continue
		}
		if v, ok := c.MinFull[metric]; ok {
			return v
		}
	}
	return 0
}

// ValidateCheck 校验一条收尾项终态：结构 + 数字门槛 + 证据要求 + 资产范围。
func ValidateCheck(ctx context.Context, store *db.SrcStore, taskID int64, doc ModeDoc, check db.SrcCheck) error {
	def, ok := FindCheck(check.CheckKey)
	if !ok {
		return fmt.Errorf("未知收尾项：%s（合法值：%s）", check.CheckKey, strings.Join(CheckKeys(), " / "))
	}
	check.CheckKey = def.Key
	if !checkAppliesTo(def, check.AssetType) {
		return fmt.Errorf("收尾项 %s 不适用于资产类型 %s", def.Key, check.AssetType)
	}
	switch check.State {
	case db.SrcCheckDone:
		if ok, why := checkSatisfied(def, doc, check.Metrics); !ok {
			return fmt.Errorf("%s 还不算做完：%s", def.Title, why)
		}
		if def.RequireEvidence && check.EvidenceNodeID <= 0 {
			return fmt.Errorf("%s 标 done 必须带 evidence_node_id（先 record_fact 记下数字与命令，再把节点 id 填进来）", def.Title)
		}
	case db.SrcCheckNA:
		if strings.TrimSpace(check.Reason) == "" {
			return fmt.Errorf("%s 标 na 必须写 reason", def.Title)
		}
		if def.Key == "screenshots" && doc.RequireScreenshots {
			return fmt.Errorf("该项目要求关键步骤截图：%s 不能标 na，请把截图登记成证据节点", def.Title)
		}
		if len(def.NaCodes) == 0 {
			return fmt.Errorf("%s 不接受 na：它没有「无入口」这种情形，必须实际做完", def.Title)
		}
		if !containsStr(def.NaCodes, check.NACode) && !containsStr(def.NaCodes, "not_applicable") {
			return fmt.Errorf("%s 的 na 理由码不合法：%q（可选：%s）", def.Title, check.NACode, strings.Join(def.NaCodes, " / "))
		}
	case db.SrcCheckBudgetStop:
		if strings.TrimSpace(check.Reason) == "" {
			return fmt.Errorf("%s 标 budget_stop 必须写 reason", def.Title)
		}
	default:
		return fmt.Errorf("未知收尾项状态：%s（done / na / budget_stop）", check.State)
	}
	if store == nil {
		return nil
	}
	asset, err := store.SrcAssetInScope(ctx, taskID, check.AssetID)
	if err != nil {
		return err
	}
	if asset == nil {
		return fmt.Errorf("资产 %d 不在本任务测试范围内，不能标收尾项", check.AssetID)
	}
	if check.EvidenceNodeID > 0 {
		ok, err := store.NodeBelongsToTask(ctx, taskID, check.EvidenceNodeID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("evidence_node_id=%d 不是本任务的节点", check.EvidenceNodeID)
		}
	}
	return nil
}

// checkSatisfied 判断数字门槛是否达标，返回"差在哪"的人话。
func checkSatisfied(def CheckDef, doc ModeDoc, metrics map[string]int) (bool, string) {
	tier := doc.Tier()
	if tier == CheckTierOff {
		return true, ""
	}
	need := def.MinFull
	if tier == CheckTierPlatform {
		need = def.MinPlatform
	}
	// 特殊形状：JS 抽全接受"降级"通道；非 404 接受"没有非 404"通道。
	switch def.Key {
	case "js_enum":
		sum := metrics["js_inline"] + metrics["js_external"] + metrics["js_chunks"] + metrics["sourcemap"]
		if sum > 0 {
			return true, ""
		}
		if metrics["degraded"] > 0 {
			return true, ""
		}
		return false, "js_inline/js_external/js_chunks/sourcemap 全为 0；CDN 拦死请在 reason 写清并用 degraded=1"
	case "non404_retest":
		if metrics["non404"] == 0 {
			return true, ""
		}
	}
	var missing []string
	for key, min := range need {
		if v := doc.Threshold(key); v > min {
			min = v
		}
		if metrics[key] < min {
			missing = append(missing, fmt.Sprintf("%s=%d < %d", key, metrics[key], min))
		}
	}
	if len(missing) == 0 {
		return true, ""
	}
	sort.Strings(missing)
	return false, strings.Join(missing, "，")
}

func checkAppliesTo(def CheckDef, assetType string) bool {
	for _, t := range def.AssetTypes {
		if t == assetType {
			return true
		}
	}
	return false
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// CheckMetricKeys 返回全部可覆盖的指标名（去重、排序）。
func CheckMetricKeys() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range CheckDefs {
		for _, m := range c.Metrics {
			if !seen[m.Key] {
				seen[m.Key] = true
				out = append(out, m.Key)
			}
		}
		for k := range c.MinFull {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// IsCheckMetric reports whether name is a known closure metric.
func IsCheckMetric(name string) bool {
	for _, c := range CheckDefs {
		for _, m := range c.Metrics {
			if m.Key == name {
				return true
			}
		}
		for k := range c.MinFull {
			if k == name {
				return true
			}
		}
	}
	return false
}

// checkPriority 是收尾缺口的排序权重：资产价值 × 项权重 ÷ 2（比面缺口略低，先补面）。
func checkPriority(assetType string, def CheckDef, attempts int) int {
	if attempts < 0 {
		attempts = 0
	}
	return AssetValue(assetType) * def.Weight / (2 * (attempts + 1))
}
