package srcmode

import "testing"

// TestLogicAreaItems 锁定 logic 面的细分项：业务流程与幂等是 SRC 高价值面，
// 不允许被合并回一个粗格子。
func TestLogicAreaItems(t *testing.T) {
	for _, item := range []string{"state-skip", "race", "privilege", "money", "workflow", "idempotent"} {
		if !ItemAllowed("logic", item) {
			t.Fatalf("logic 面缺少细分项 %q（现有：%v）", item, AreaItems("logic"))
		}
	}
}

// TestLogicFlowsCheck 校验新增的收尾项：门槛、适用资产类型与 N/A 理由码。
func TestLogicFlowsCheck(t *testing.T) {
	def, ok := FindCheck("logic_flows")
	if !ok {
		t.Fatal("缺少收尾项 logic_flows")
	}
	if def.MinFull["flows"] < 1 || def.MinFull["evidenced"] < 1 {
		t.Fatalf("full 档门槛不合理: %v", def.MinFull)
	}
	if def.MinPlatform["flows"] < 1 {
		t.Fatalf("platform 档门槛不合理: %v", def.MinPlatform)
	}
	if !def.RequireEvidence {
		t.Fatal("logic_flows 必须要求证据节点")
	}
	for _, typ := range []string{"app", "service"} {
		if !checkAppliesTo(def, typ) {
			t.Fatalf("logic_flows 应适用于 %s", typ)
		}
	}
	if checkAppliesTo(def, "endpoint") {
		t.Fatal("logic_flows 不应适用于单个 endpoint")
	}
	for _, code := range []string{"no_state_machine", "no_business_object"} {
		found := false
		for _, c := range def.NaCodes {
			if c == code {
				found = true
			}
		}
		if !found {
			t.Fatalf("logic_flows 缺少 N/A 码 %q", code)
		}
		if NACodeTitle(code) == code {
			t.Fatalf("N/A 码 %q 缺少中文说明", code)
		}
	}
}

// TestLogicNACodesScoped 新理由码只能用在 logic（及 authz 的业务对象码）上，
// 不能变成万能 N/A。
func TestLogicNACodesScoped(t *testing.T) {
	if !NACodeAllowed("logic", "no_state_machine") {
		t.Fatal("logic 面应允许 no_state_machine")
	}
	if NACodeAllowed("injection", "no_state_machine") {
		t.Fatal("no_state_machine 不应适用于 injection")
	}
	if !NACodeAllowed("authz", "no_business_object") {
		t.Fatal("authz 面应允许 no_business_object")
	}
}
