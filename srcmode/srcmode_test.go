package srcmode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

// TestResolveDefaults pins the创建时快照 semantics: a bare mode="src" must produce a
// complete, self-describing doc (budgets filled, mode stamped) so运行中的任务 never
// has to fall back to "whatever the globals say now".
func TestResolveDefaults(t *testing.T) {
	raw, doc, err := Resolve("src", nil)
	if err != nil {
		t.Fatalf("Resolve(src, nil): %v", err)
	}
	if doc.Mode != ModeSrc || doc.DocVersion != DocVersion {
		t.Fatalf("doc not stamped: %+v", doc)
	}
	if doc.MaxAttemptsPerCell != DefaultMaxAttemptsPerCell || doc.MaxIntentsPerAsset != DefaultMaxIntentsPerAsset {
		t.Fatalf("budgets not defaulted: %+v", doc)
	}
	if doc.AllowBudgetStop {
		t.Fatal("allow_budget_stop 默认必须是 false：缺口不能默认被放行")
	}
	var round ModeDoc
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("snapshot not valid JSON: %v", err)
	}
	if round.Mode != ModeSrc {
		t.Fatalf("snapshot lost mode: %+v", round)
	}
}

func TestResolvePlainPentest(t *testing.T) {
	raw, doc, err := Resolve("", json.RawMessage(`{"mode":"src"}`))
	if err != nil {
		t.Fatalf("Resolve(\"\"): %v", err)
	}
	if raw != nil || doc.IsSrc() {
		t.Fatalf("mode=\"\" 必须产出空快照，got raw=%s doc=%+v", raw, doc)
	}
}

func TestResolveRejects(t *testing.T) {
	cases := []struct {
		name string
		mode string
		raw  string
		want string
	}{
		{"unknown mode", "ctf", "", "未知任务模式"},
		{"unknown area", "src", `{"required_areas":{"app":["nope"]}}`, "未知风险面"},
		{"unknown asset type", "src", `{"required_areas":{"toaster":["authz"]}}`, "未知资产类型"},
		{"empty areas", "src", `{"required_areas":{"app":[]}}`, "不能为空"},
		{"bad hard item", "src", `{"hard_items":["paths"]}`, "area/item"},
		{"unknown hard area", "src", `{"hard_items":["nope/enum-200"]}`, "未知风险面"},
		{"unknown hard item", "src", `{"hard_items":["paths/nope"]}`, "没有"},
		{"too many attempts", "src", `{"max_attempts_per_cell":99}`, "max_attempts_per_cell"},
		{"too many intents", "src", `{"max_intents_per_asset":100000}`, "max_intents_per_asset"},
		{"negative asset budget", "src", `{"asset_token_budget":-1}`, "asset_token_budget"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Resolve(tc.mode, json.RawMessage(tc.raw))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestParseToleratesBadSnapshot(t *testing.T) {
	doc := Parse(json.RawMessage(`{not json`))
	if !doc.IsSrc() {
		t.Fatal("坏快照应退化成默认 SRC 文档（mode 保持），否则任务会静默变回普通渗透")
	}
	if doc.MaxAttemptsPerCell != DefaultMaxAttemptsPerCell {
		t.Fatalf("坏快照的预算没有回默认：%+v", doc)
	}
}

func TestRequiredFor(t *testing.T) {
	doc := DefaultDoc()
	if got := doc.RequiredFor("app"); len(got) != 15 {
		t.Fatalf("app 必做面应有 15 个（含四件套 + cache/oauth/graphql/websocket/supply），got %v", got)
	}
	if got := doc.RequiredFor("endpoint"); len(got) != 8 {
		t.Fatalf("endpoint 必做面应有 8 个（含四件套 + graphql），got %v", got)
	}
	if got := doc.RequiredFor("ip"); len(got) != 2 {
		t.Fatalf("ip 必做面应有 2 个，got %v", got)
	}
	if got := doc.RequiredFor("unknown-type"); got != nil {
		t.Fatalf("未知类型必须返回 nil（由收尾门报 unknown_asset_type），got %v", got)
	}
	doc.RequiredAreas = map[string][]string{"app": {"authz"}}
	if got := doc.RequiredFor("app"); len(got) != 1 || got[0] != "authz" {
		t.Fatalf("mode_doc 覆盖没生效：%v", got)
	}
}

func TestHardItemsFor(t *testing.T) {
	doc := DefaultDoc()
	doc.HardItems = []string{"paths/enum-200", "paths/non404-retest", "authz/idor"}
	got := doc.HardItemsFor("paths")
	if len(got) != 2 || got[0] != "enum-200" || got[1] != "non404-retest" {
		t.Fatalf("hard items 分组/排序错误：%v", got)
	}
}

func TestCellPriorityFavorsUntriedHighValue(t *testing.T) {
	doc := DefaultDoc()
	fresh := cellPriority("app", "rce", 0, doc, 1)
	tried := cellPriority("app", "rce", 3, doc, 1)
	if fresh <= tried {
		t.Fatalf("没试过的高价值格应优先：fresh=%d tried=%d", fresh, tried)
	}
	low := cellPriority("subdomain", "paths", 0, doc, 1)
	if fresh <= low {
		t.Fatalf("app×rce 应高于 subdomain×paths：%d vs %d", fresh, low)
	}
	// ROI 乘子：历史产出高的面加分，冷资产降权。
	hot := DefaultDoc()
	hot.AreaROI = map[string]float64{"rce": 2}
	if got := cellPriority("app", "rce", 0, hot, 1); got <= fresh {
		t.Fatalf("ROI>1 应抬高优先级：%d vs %d", got, fresh)
	}
	if got := cellPriority("app", "rce", 0, doc, 0.3); got >= fresh {
		t.Fatalf("冷资产应降权：%d vs %d", got, fresh)
	}
}

func TestValidateCellStructuralRules(t *testing.T) {
	doc := DefaultDoc()
	ctx := context.Background()
	// store=nil：跳过范围/证据归属检查，只验结构（单测边界）。
	base := db.SrcCell{TaskID: 1, AssetID: 2, Area: "authz", Item: "*"}

	cases := []struct {
		name  string
		cell  db.SrcCell
		want  string
		valid bool
	}{
		{"unknown area", withState(base, "nope", "", db.SrcStateClear, "ok"), "未知风险面", false},
		{"bad item", withState(base, "authz", "nope", db.SrcStateClear, "ok"), "细分项", false},
		{"bad state", withState(base, "authz", "*", "done", "ok"), "未知终态", false},
		{"found without finding", withState(base, "authz", "*", db.SrcStateFound, ""), "finding_id", false},
		{"clear without evidence", withState(base, "authz", "*", db.SrcStateClear, ""), "evidence_node_id", false},
		{"clear with reason only", withState(base, "authz", "*", db.SrcStateClear, "测过，无越权"), "evidence_node_id", false},
		{"na without reason", withState(base, "authz", "*", db.SrcStateNA, ""), "reason", false},
		{"budget_stop without reason", withState(base, "authz", "*", db.SrcStateBudgetStop, ""), "reason", false},
		{"fine with evidence", db.SrcCell{
			TaskID: 1, AssetID: 2, Area: "injection", Item: "sql",
			State: db.SrcStateClear, Reason: "参数化查询", EvidenceNodeID: 5,
		}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCell(ctx, nil, 1, doc, tc.cell)
			if tc.valid && err != nil {
				t.Fatalf("expected valid, got %v", err)
			}
			if !tc.valid {
				if err == nil {
					t.Fatalf("expected error mentioning %q, got nil", tc.want)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error %q does not mention %q", err, tc.want)
				}
			}
		})
	}

	// 项目显式允许时，说明型 clear 才可以通过。
	allow := DefaultDoc()
	allow.AllowReasonOnlyClear = true
	if err := ValidateCell(ctx, nil, 1, allow, withState(base, "authz", "*", db.SrcStateClear, "测过，无越权")); err != nil {
		t.Fatalf("allow_reason_only_clear 应放行说明型 clear：%v", err)
	}
	// platform_exempt 必须有 program 的 exempt_areas 依据。
	exempt := withState(base, "authz", "*", db.SrcStateNA, "平台不测")
	exempt.NACode = "platform_exempt"
	if err := ValidateCell(ctx, nil, 1, DefaultDoc(), exempt); err == nil {
		t.Fatal("没有豁免声明时 platform_exempt 必须被拒")
	}
	allowExempt := DefaultDoc()
	allowExempt.PlatformExemptAreas = []string{"authz"}
	if err := ValidateCell(ctx, nil, 1, allowExempt, exempt); err != nil {
		t.Fatalf("program 声明豁免后 platform_exempt 应放行：%v", err)
	}
}

func withState(base db.SrcCell, area, item, state, reason string) db.SrcCell {
	base.Area = area
	base.Item = item
	base.State = state
	base.Reason = reason
	return base
}

func TestCellGapShapes(t *testing.T) {
	asset := db.SrcAsset{ID: 7, Type: "app", Label: "api.example.com"}
	cases := []struct {
		cell     db.SrcCell
		wantKind string
		wantGap  bool
	}{
		{db.SrcCell{Area: "authz", Item: "*", State: db.SrcStateNA, Reason: "不适用"}, "", false},
		{db.SrcCell{Area: "authz", Item: "*", State: db.SrcStateNA}, GapMissingReason, true},
		{db.SrcCell{Area: "authz", Item: "*", State: db.SrcStateClear}, GapMissingEvidence, true},
		{db.SrcCell{Area: "authz", Item: "*", State: db.SrcStateClear, Reason: "发过请求，无异常"}, GapMissingEvidence, true},
		{db.SrcCell{Area: "authz", Item: "*", State: db.SrcStateClear, EvidenceNodeID: 9}, "", false},
		// 继承格：证据在源任务里，带继承来源 + 说明即可通过；没有说明仍然缺证据。
		{db.SrcCell{Area: "authz", Item: "*", State: db.SrcStateClear, Reason: "继承自任务 #3", InheritedFromTaskID: 3}, "", false},
		{db.SrcCell{Area: "authz", Item: "*", State: db.SrcStateClear, InheritedFromTaskID: 3}, GapMissingEvidence, true},
		{db.SrcCell{Area: "authz", Item: "*", State: db.SrcStateFound}, GapMissingFinding, true},
		{db.SrcCell{Area: "authz", Item: "*", State: db.SrcStateFound, FindingID: 3}, "", false},
	}
	doc := DefaultDoc()
	for i, tc := range cases {
		gap, isGap := cellGap(asset, "authz", tc.cell, doc, 1)
		if isGap != tc.wantGap {
			t.Fatalf("case %d: want gap=%v got %v (%+v)", i, tc.wantGap, isGap, gap)
		}
		if tc.wantGap && gap.Kind != tc.wantKind {
			t.Fatalf("case %d: want kind %q got %q", i, tc.wantKind, gap.Kind)
		}
		if gap.AssetID != asset.ID || gap.Asset != asset.Label {
			t.Fatalf("case %d: gap lost asset identity: %+v", i, gap)
		}
	}
	// 项目显式允许时，说明型 clear 可以不留证据节点。
	allow := DefaultDoc()
	allow.AllowReasonOnlyClear = true
	if g, bad := cellGap(asset, "authz", db.SrcCell{
		Area: "authz", Item: "*", State: db.SrcStateClear, Reason: "发过请求，无异常",
	}, allow, 1); bad {
		t.Fatalf("allow_reason_only_clear 应放行说明型 clear：%+v", g)
	}
}

// TestCheckFinishWithoutStoreIsFinishable pins the degradation rule: no SRC store
// means the gate must not invent gaps (unit tests /未接线 callers stay green).
func TestCheckFinishWithoutStoreIsFinishable(t *testing.T) {
	rep, err := CheckFinish(context.Background(), nil, 1, DefaultDoc())
	if err != nil {
		t.Fatalf("CheckFinish(nil store): %v", err)
	}
	if !rep.Finishable || rep.GapsTotal != 0 {
		t.Fatalf("nil store 不应制造缺口：%+v", rep)
	}
}

func TestAreaTaxonomyConsistency(t *testing.T) {
	for _, area := range AreaNames() {
		if !IsArea(area) {
			t.Fatalf("AreaNames 里的 %q 不是合法面", area)
		}
		if !ItemAllowed(area, AreaAll) {
			t.Fatalf("面 %q 不允许通配项", area)
		}
	}
	for assetType, areas := range defaultRequired {
		if !IsAssetType(assetType) {
			t.Fatalf("默认必做面引用了未知资产类型 %q", assetType)
		}
		for _, area := range areas {
			if !IsArea(area) {
				t.Fatalf("资产类型 %q 引用了未知面 %q", assetType, area)
			}
		}
	}
	if len(Areas) != len(AreaNames()) {
		t.Fatal("AreaNames 与 Areas 长度不一致")
	}
}

// TestAssetYieldCold 钉住 ROI 止损画像：投入 ≥4 个终态格仍零发现 → 冷资产。
func TestAssetYieldCold(t *testing.T) {
	asset := db.SrcAsset{ID: 9, Type: "app", Label: "api.example"}
	cells := []db.SrcCell{
		{State: db.SrcStateClear}, {State: db.SrcStateClear},
		{State: db.SrcStateNA}, {State: db.SrcStateBudgetStop},
	}
	st := assetYield(asset, cells)
	if !st.Cold || st.Terminal != 4 || st.Found != 0 || st.AssetID != 9 {
		t.Fatalf("应判为冷资产：%+v", st)
	}
	cells[0].State = db.SrcStateFound
	if st := assetYield(asset, cells); st.Cold || st.Found != 1 {
		t.Fatalf("有发现就不该判冷：%+v", st)
	}
	if st := assetYield(asset, cells[:2]); st.Cold {
		t.Fatalf("终态不足 4 个不该判冷：%+v", st)
	}
}

// TestResolveValidatesAreaROI 钉住 ROI 快照的合法性：未知面/离谱乘子直接拒绝。
func TestResolveValidatesAreaROI(t *testing.T) {
	if _, _, err := Resolve(ModeSrc, []byte(`{"area_roi":{"nope":1.2}}`)); err == nil {
		t.Fatal("未知风险面的 ROI 应拒绝")
	}
	if _, _, err := Resolve(ModeSrc, []byte(`{"area_roi":{"injection":99}}`)); err == nil {
		t.Fatal("超范围 ROI 应拒绝")
	}
	_, doc, err := Resolve(ModeSrc, []byte(`{"area_roi":{"injection":1.2}}`))
	if err != nil || doc.AreaROI["injection"] != 1.2 {
		t.Fatalf("合法 ROI 未解析：%v %+v", err, doc.AreaROI)
	}
}

func TestSubmitInfoPlatformTemplates(t *testing.T) {
	doc := DefaultDoc()
	doc.Platform = "补天"
	block := SubmitInfoFor(doc, SubmitContext{
		Title: "越权读取订单", SeverityLabel: "高危", CWE: "CWE-639",
		Asset: "https://api.example.com/orders/1", Attachments: []string{"attachments/1/poc.sh"},
	})
	for _, want := range []string{"提交信息 · 补天", "标题：越权读取订单", "漏洞类型：CWE-639", "资产/URL：https://api.example.com/orders/1", "规则页（模板日期", "https://www.butian.net/"} {
		if !strings.Contains(block, want) {
			t.Fatalf("butian block missing %q:\n%s", want, block)
		}
	}

	doc = DefaultDoc()
	doc.PlatformTemplate = "hackerone"
	block = SubmitInfoFor(doc, SubmitContext{Title: "IDOR", SeverityLabel: "high", CWE: "CWE-639", Asset: "api.example.com"})
	for _, want := range []string{"HackerOne", "Title：IDOR", "Weakness：CWE-639", "Steps to Reproduce"} {
		if !strings.Contains(block, want) {
			t.Fatalf("hackerone block missing %q:\n%s", want, block)
		}
	}

	// 未识别出平台时保持通用块，且没有提交字段就不硬塞空块。
	doc = DefaultDoc()
	if got := SubmitInfoFor(doc, SubmitContext{}); got != "" {
		t.Fatalf("empty generic block=%q want empty", got)
	}
	if tpl := ResolvePlatformTemplate(doc); tpl.Key != "generic" {
		t.Fatalf("template=%+v want generic", tpl)
	}

	// 未知模板 key 在 Resolve 阶段拒绝。
	if _, _, err := Resolve("src", json.RawMessage(`{"platform_template":"nope"}`)); err == nil {
		t.Fatal("unknown platform template should be rejected")
	}
}

func TestProgramAppliesPlatformTemplate(t *testing.T) {
	p := &Program{Name: "prog", Rules: ProgramRules{PlatformTemplate: "vulbox"}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	doc := p.ApplyToModeDoc(DefaultDoc())
	if doc.PlatformTemplate != "vulbox" {
		t.Fatalf("template=%q want vulbox", doc.PlatformTemplate)
	}
}

func TestNewRiskAreas(t *testing.T) {
	for _, area := range []string{"cache", "desync", "oauth", "graphql", "websocket", "supply"} {
		if !IsArea(area) {
			t.Fatalf("missing area %s", area)
		}
	}
	app := DefaultRequiredAreas("app")
	for _, want := range []string{"cache", "oauth"} {
		if !containsStr(app, want) {
			t.Fatalf("app required areas=%v missing %s", app, want)
		}
	}
	if sub := DefaultRequiredAreas("subdomain"); !containsStr(sub, "desync") {
		t.Fatalf("subdomain required areas=%v missing desync", sub)
	}
	if !NACodeAllowed("cache", "no_cache") || !NACodeAllowed("desync", "no_reverse_proxy") || !NACodeAllowed("oauth", "no_oauth") {
		t.Fatal("new area N/A codes should be allowed")
	}
	if !NACodeAllowed("graphql", "no_graphql") || !NACodeAllowed("websocket", "no_websocket") || !NACodeAllowed("supply", "no_third_party") {
		t.Fatal("graphql/websocket/supply N/A codes should be allowed")
	}
	if NACodeAllowed("graphql", "no_cache") || NACodeAllowed("websocket", "no_input") {
		t.Fatal("N/A codes must stay scoped to their area")
	}
	for _, want := range []string{"graphql", "websocket", "supply"} {
		if !containsStr(app, want) {
			t.Fatalf("app required areas=%v missing %s", app, want)
		}
	}
	if !containsStr(DefaultRequiredAreas("endpoint"), "graphql") {
		t.Fatal("endpoint should require graphql (GraphQL endpoints are endpoint assets)")
	}
	if _, ok := FindCheck("cache_headers"); !ok {
		t.Fatal("cache_headers check missing")
	}
	for _, item := range []string{"cache/poison", "desync/cl-te", "oauth/redirect-uri",
		"graphql/introspection", "websocket/message-injection", "supply/sdk-key"} {
		area, sub, err := SplitItem(item)
		if err != nil || !ItemAllowed(area, sub) {
			t.Fatalf("item %s invalid: %v", item, err)
		}
	}
	// endpoint 是单个路由，不该被页面级的 JS 清点收尾项拖住。
	for _, c := range ChecksFor("endpoint") {
		if c.Key == "js_enum" {
			t.Fatal("endpoint assets must not require js_enum")
		}
	}
	if len(ChecksFor("app")) == 0 {
		t.Fatal("app assets should still have closure checks")
	}
}

// TestReconMissing pins the "inventory before payload" ordering signal: only
// app/service assets get it, and a done-but-below-threshold check still counts
// as missing (the gate's own threshold is the source of truth).
func TestReconMissing(t *testing.T) {
	doc := DefaultDoc()
	app := db.SrcAsset{ID: 1, Type: "app", Label: "api.example"}
	if got := reconMissing(app, nil, doc); len(got) != 2 || got[0] != "js_enum" || got[1] != "paths_enum" {
		t.Fatalf("empty checks should list both recon items in order: %v", got)
	}
	ok := map[string]db.SrcCheck{
		"js_enum":    {State: db.SrcCheckDone, Metrics: map[string]int{"js_inline": 1}},
		"paths_enum": {State: db.SrcCheckDone, Metrics: map[string]int{"paths": 200}},
	}
	if got := reconMissing(app, ok, doc); len(got) != 0 {
		t.Fatalf("satisfied checks should not be pending: %v", got)
	}
	// 数字不达标仍是缺口（200 条是全量档门槛）。
	ok["paths_enum"] = db.SrcCheck{State: db.SrcCheckDone, Metrics: map[string]int{"paths": 3}}
	if got := reconMissing(app, ok, doc); len(got) != 1 || got[0] != "paths_enum" {
		t.Fatalf("below-threshold paths should stay pending: %v", got)
	}
	if got := reconMissing(db.SrcAsset{ID: 2, Type: "endpoint"}, nil, doc); got != nil {
		t.Fatalf("endpoint assets are not page surfaces: %v", got)
	}
	off := doc
	off.SkipCheckGate = true
	if got := reconMissing(app, nil, off); got != nil {
		t.Fatalf("check gate off should not report recon: %v", got)
	}
}

// TestAssetTierMultiplier pins the explicit asset-tier influence on补格 priority:
// unclassified assets keep the old behavior (1.0), high-value surfaces rank up,
// third-party/CDN surfaces rank down.
func TestAssetTierMultiplier(t *testing.T) {
	cases := map[string]float64{"": 1, "primary": 1.4, "api": 1.25, "admin": 1.2, "mobile": 1.1, "third_party": 0.6, "other": 0.8, "nope": 1}
	for tier, want := range cases {
		if got := AssetTierMultiplier(tier); got != want {
			t.Fatalf("AssetTierMultiplier(%q)=%v want %v", tier, got, want)
		}
	}
	for _, tier := range append([]string{""}, AssetTiers...) {
		if !AssetTierAllowed(tier) {
			t.Fatalf("tier %q should be allowed", tier)
		}
	}
	if AssetTierAllowed("bogus") {
		t.Fatal("unknown tier must be rejected")
	}
}

// TestWorkerTierForRiskArea pins the per-area model routing table: high-value
// faces go strong, inventory faces go cheap, explicit mode_doc overrides win,
// and unknown db risk areas stay unrouted (fall back to the default worker).
func TestWorkerTierForRiskArea(t *testing.T) {
	doc := DefaultDoc()
	cases := map[string]string{
		"sqli": "strong", "authz": "strong", "cache": "strong", "graphql": "strong",
		"traversal": "strong", "disclosure": "cheap", "supply": "cheap", "other": "",
	}
	for risk, want := range cases {
		if got := doc.WorkerTierForRiskArea(risk); got != want {
			t.Fatalf("WorkerTierForRiskArea(%q)=%q want %q", risk, got, want)
		}
	}
	doc.AreaTiers = map[string]string{"injection": "cheap", "paths": "strong"}
	if got := doc.WorkerTierForRiskArea("sqli"); got != "cheap" {
		t.Fatalf("explicit area_tiers should win, got %q", got)
	}
	if got := doc.WorkerTierForRiskArea("disclosure"); got != "strong" {
		t.Fatalf("paths override not applied: %q", got)
	}
	if _, _, err := Resolve(ModeSrc, []byte(`{"area_tiers":{"nope":"strong"}}`)); err == nil {
		t.Fatal("unknown area should be rejected")
	}
	if _, _, err := Resolve(ModeSrc, []byte(`{"area_tiers":{"authz":"turbo"}}`)); err == nil {
		t.Fatal("unknown tier should be rejected")
	}
}

// TestCustomSubmitTemplateRenders pins user templates: field order/labels,
// required placeholders, unknown sources skipped, and rule-page reminder.
func TestCustomSubmitTemplateRenders(t *testing.T) {
	doc := DefaultDoc()
	doc.Platform = "Acme SRC"
	doc.Program = "Acme Q4"
	doc.EgressIP = "203.0.113.9"
	tpl := &CustomSubmitTemplate{
		Key: "custom:acme", Name: "Acme 表单", Header: "请按模板填写",
		RuleURL: "https://acme.example/rules", RuleDate: "2026-10-24",
		Fields: []CustomSubmitField{
			{Label: "标题", Source: "title", Required: true},
			{Label: "等级", Source: "severity", Required: true},
			{Label: "说明", Source: "unknown"},
			{Label: "出口", Source: "egress_ip"},
		},
	}
	out := SubmitInfoForCustom(doc, SubmitContext{Title: "越权", SeverityLabel: "高危"}, tpl)
	for _, want := range []string{"【提交信息 · Acme 表单】", "请按模板填写", "标题：越权", "等级：高危", "出口：203.0.113.9", "规则页（模板日期 2026-10-24"} {
		if !strings.Contains(out, want) {
			t.Fatalf("custom block missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "说明") {
		t.Fatalf("unknown source should be skipped:\n%s", out)
	}
	// 必填缺失 -> 占位符，而不是静默空行。
	out = SubmitInfoForCustom(doc, SubmitContext{}, tpl)
	if !strings.Contains(out, "标题：（待补）") {
		t.Fatalf("required placeholder missing:\n%s", out)
	}
	// custom: 前缀在 Resolve 中合法（存在性由 server 查库）。
	if _, _, err := Resolve(ModeSrc, []byte(`{"platform_template":"custom:acme"}`)); err != nil {
		t.Fatalf("custom template key rejected: %v", err)
	}
}
