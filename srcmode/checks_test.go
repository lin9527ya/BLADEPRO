package srcmode

import (
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestCheckCatalogIntegrity(t *testing.T) {
	seen := map[string]bool{}
	for _, def := range CheckDefs {
		if def.Key == "" || def.Title == "" {
			t.Fatalf("收尾项缺 key/title：%+v", def)
		}
		if seen[def.Key] {
			t.Fatalf("收尾项 key 重复：%s", def.Key)
		}
		seen[def.Key] = true
		if len(def.AssetTypes) == 0 {
			t.Fatalf("%s 没有适用资产类型（永远不生效）", def.Key)
		}
		if def.Weight <= 0 || def.Weight > 40 {
			t.Fatalf("%s 权重不合理：%d", def.Key, def.Weight)
		}
		for _, m := range def.Metrics {
			if m.Key == "" || m.Label == "" {
				t.Fatalf("%s 的指标缺 key/label：%+v", def.Key, m)
			}
		}
		for _, code := range def.NaCodes {
			if NACodeTitle(code) == code {
				t.Fatalf("%s 的 na 码 %q 没有中文说明", def.Key, code)
			}
		}
	}
	for _, key := range []string{"js_enum", "paths_enum", "non404_retest", "method_matrix", "param_variants", "defense_contrast", "unreachable_proof", "screenshots"} {
		if !seen[key] {
			t.Fatalf("缺收尾项 %s（技能收尾闸 8 项）", key)
		}
	}
}

func TestCheckSatisfiedThresholds(t *testing.T) {
	doc := DefaultDoc()
	paths := mustCheck(t, "paths_enum")
	if ok, why := checkSatisfied(paths, doc, map[string]int{"paths": 37}); ok {
		t.Fatalf("paths=37 不该通过 full 档：%s", why)
	} else if !strings.Contains(why, "37") || !strings.Contains(why, "200") {
		t.Fatalf("拒绝理由要写清差多少：%s", why)
	}
	if ok, _ := checkSatisfied(paths, doc, map[string]int{"paths": 200}); !ok {
		t.Fatal("paths=200 应通过 full 档")
	}

	// 平台档：手工清单替代 200 条字典。
	doc.CheckTier = CheckTierPlatform
	if ok, why := checkSatisfied(paths, doc, map[string]int{"manual_paths": 12}); !ok {
		t.Fatalf("平台档 manual_paths=12 应通过：%s", why)
	}
	if ok, _ := checkSatisfied(paths, doc, map[string]int{"paths": 300}); ok {
		t.Fatal("平台档不该用字典条数通过（那是另一种门槛）")
	}

	// 阈值覆盖优先于档位默认。
	doc.CheckThresholds = map[string]int{"manual_paths": 30}
	if ok, why := checkSatisfied(paths, doc, map[string]int{"manual_paths": 12}); ok {
		t.Fatalf("覆盖后的门槛 30 应拒绝 12：%s", why)
	}
}

func TestCheckSatisfiedSpecialShapes(t *testing.T) {
	doc := DefaultDoc()
	js := mustCheck(t, "js_enum")
	if ok, _ := checkSatisfied(js, doc, map[string]int{}); ok {
		t.Fatal("全 0 的 JS 指标不该通过")
	}
	if ok, _ := checkSatisfied(js, doc, map[string]int{"js_inline": 1}); !ok {
		t.Fatal("抽到 1 个内联脚本应通过")
	}
	if ok, _ := checkSatisfied(js, doc, map[string]int{"degraded": 1}); !ok {
		t.Fatal("降级通道（CDN 拦死）应通过")
	}

	retest := mustCheck(t, "non404_retest")
	if ok, _ := checkSatisfied(retest, doc, map[string]int{"non404": 0}); !ok {
		t.Fatal("没有非 404 响应时应允许以 non404=0 收口")
	}
	if ok, _ := checkSatisfied(retest, doc, map[string]int{"non404": 6, "retested": 1}); ok {
		t.Fatal("有 6 条非 404 但只复测 1 条，不该通过")
	}
}

func TestCheckSatisfiedOff(t *testing.T) {
	doc := DefaultDoc()
	doc.CheckTier = CheckTierOff
	paths := mustCheck(t, "paths_enum")
	if ok, _ := checkSatisfied(paths, doc, nil); !ok {
		t.Fatal("off 档不应再卡数字")
	}
}

func TestNACodeWhitelist(t *testing.T) {
	if !NACodeAllowed("rce", "no_exec_sink") {
		t.Fatal("rce 应允许 no_exec_sink")
	}
	if NACodeAllowed("rce", "no_input") {
		t.Fatal("rce 不该允许 no_input（那是注入/逻辑的理由码）")
	}
	if !NACodeAllowed("authz", "static_page") {
		t.Fatal("static_page 应对全部面适用")
	}
	if NACodeAllowed("injection", "made_up") {
		t.Fatal("未知理由码不该通过")
	}
	if got := AreaNACodes("ssrf"); len(got) < 3 {
		t.Fatalf("ssrf 可选理由码太少：%v", got)
	}
}

func TestValidateCheckRequiresEvidence(t *testing.T) {
	doc := DefaultDoc()
	chk := db.SrcCheck{
		AssetID: 1, AssetType: "app", CheckKey: "paths_enum", State: db.SrcCheckDone,
		Metrics: map[string]int{"paths": 210},
	}
	if err := ValidateCheck(t.Context(), nil, 1, doc, chk); err == nil ||
		!strings.Contains(err.Error(), "evidence_node_id") {
		t.Fatalf("done 缺证据节点应被拒：%v", err)
	}
	chk.EvidenceNodeID = 7
	if err := ValidateCheck(t.Context(), nil, 1, doc, chk); err != nil {
		t.Fatalf("达标且带证据应通过：%v", err)
	}
}

func TestValidateCheckNA(t *testing.T) {
	doc := DefaultDoc()
	base := db.SrcCheck{AssetID: 1, AssetType: "app", State: db.SrcCheckNA}

	// 不接受 N/A 的项。
	js := base
	js.CheckKey = "js_enum"
	js.Reason = "懒得抽"
	if err := ValidateCheck(t.Context(), nil, 1, doc, js); err == nil {
		t.Fatal("js_enum 不接受 na")
	}

	// 接受 N/A 但没有理由码。
	pv := base
	pv.CheckKey = "param_variants"
	pv.Reason = "没有参数"
	if err := ValidateCheck(t.Context(), nil, 1, doc, pv); err == nil {
		t.Fatal("na 必须给 na_code")
	}

	pv.NACode = "no_exec_sink"
	if err := ValidateCheck(t.Context(), nil, 1, doc, pv); err == nil {
		t.Fatal("理由码不属于该项时应拒绝")
	}

	pv.NACode = "no_business_param"
	if err := ValidateCheck(t.Context(), nil, 1, doc, pv); err != nil {
		t.Fatalf("合法理由码应通过：%v", err)
	}
}

func TestChecksForAssetType(t *testing.T) {
	app := ChecksFor("app")
	if len(app) != len(CheckDefs) {
		t.Fatalf("app 应适用全部收尾项，got %d", len(app))
	}
	ip := ChecksFor("ip")
	if len(ip) != 1 || ip[0].Key != "unreachable_proof" {
		t.Fatalf("ip 只适用判死三条件，got %+v", ip)
	}
}

func TestModeDocTierAndThresholds(t *testing.T) {
	doc := DefaultDoc()
	if doc.Tier() != CheckTierFull {
		t.Fatalf("默认档位应是 full，got %s", doc.Tier())
	}
	doc.CheckTier = "platform"
	if doc.Tier() != CheckTierPlatform {
		t.Fatal("platform 档未生效")
	}
	if got := doc.Threshold("paths"); got != 0 {
		t.Fatalf("platform 档不应有字典条数门槛，got %d", got)
	}
	if got := doc.Threshold("manual_paths"); got != 1 {
		t.Fatalf("platform 档 manual_paths 门槛应为 1，got %d", got)
	}

	// Resolve 拒绝未知档位与未知指标。
	if _, _, err := Resolve(ModeSrc, []byte(`{"check_tier":"lenient"}`)); err == nil {
		t.Fatal("未知档位应被 Resolve 拒绝")
	}
	if _, _, err := Resolve(ModeSrc, []byte(`{"check_thresholds":{"paths":-1}}`)); err == nil {
		t.Fatal("负门槛应被拒绝")
	}
	if _, _, err := Resolve(ModeSrc, []byte(`{"check_thresholds":{"nope":1}}`)); err == nil {
		t.Fatal("未知指标应被拒绝")
	}
}

func TestRedlinesAndBuiltinRules(t *testing.T) {
	block := RedlineBlock()
	for _, want := range []string{"禁止登出", "别人账号只探不发", "CORS"} {
		if !strings.Contains(block, want) {
			t.Fatalf("红线段缺 %q：%s", want, block)
		}
	}
	rules := BuiltinRules()
	if len(rules) != 1 || !rules[0].Regex {
		t.Fatalf("内建规则应只有一条正则：%+v", rules)
	}
	for _, sample := range []string{"curl https://x/logout", "GET /api/revoke", "注销账号"} {
		if !regexpMatch(rules[0].Pattern, sample) {
			t.Fatalf("会话红线未命中 %q", sample)
		}
	}
	if regexpMatch(rules[0].Pattern, "https://x/logout-history") {
		t.Log("注意：/logout 前缀会命中 logout-history，属可接受的保守拦截")
	}
}

func TestSubmitInfo(t *testing.T) {
	doc := DefaultDoc()
	if got := SubmitInfo(doc, "高危"); got != "" {
		t.Fatalf("没有任何提交字段时不该生成提交块：%q", got)
	}
	doc.Platform = "ByteSRC"
	doc.Program = "字节-电商"
	doc.EgressIP = "1.2.3.4"
	doc.TestAccounts = "账号A"
	got := SubmitInfo(doc, "高危")
	for _, want := range []string{"【提交信息】", "ByteSRC", "字节-电商", "高危", "1.2.3.4", "账号A"} {
		if !strings.Contains(got, want) {
			t.Fatalf("提交块缺 %q：%s", want, got)
		}
	}
}

func mustCheck(t *testing.T, key string) CheckDef {
	t.Helper()
	def, ok := FindCheck(key)
	if !ok {
		t.Fatalf("缺收尾项 %s", key)
	}
	return def
}
