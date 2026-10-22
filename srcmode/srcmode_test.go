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
	if got := doc.RequiredFor("app"); len(got) != 8 {
		t.Fatalf("app 必做面应有 8 个，got %v", got)
	}
	if got := doc.RequiredFor("endpoint"); len(got) != 5 {
		t.Fatalf("endpoint 必做面应有 5 个，got %v", got)
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
	fresh := cellPriority("app", "rce", 0)
	tried := cellPriority("app", "rce", 3)
	if fresh <= tried {
		t.Fatalf("没试过的高价值格应优先：fresh=%d tried=%d", fresh, tried)
	}
	low := cellPriority("subdomain", "paths", 0)
	if fresh <= low {
		t.Fatalf("app×rce 应高于 subdomain×paths：%d vs %d", fresh, low)
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
		{"clear with reason", withState(base, "authz", "*", db.SrcStateClear, "测过，无越权"), "", true},
		{"na without reason", withState(base, "authz", "*", db.SrcStateNA, ""), "reason", false},
		{"budget_stop without reason", withState(base, "authz", "*", db.SrcStateBudgetStop, ""), "reason", false},
		{"fine with item", withState(base, "injection", "sql", db.SrcStateClear, "参数化查询"), "", true},
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
		{db.SrcCell{Area: "authz", Item: "*", State: db.SrcStateFound}, GapMissingFinding, true},
		{db.SrcCell{Area: "authz", Item: "*", State: db.SrcStateFound, FindingID: 3}, "", false},
	}
	for i, tc := range cases {
		gap, isGap := cellGap(asset, "authz", tc.cell)
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
