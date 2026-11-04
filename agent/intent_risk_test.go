package agent

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lin9527ya/bladepro/db"
)

// risk_area 漏填不再整批退回：按 summary 推断并在回执里说明；显式填错仍然报错。
func TestAddIntentInfersMissingRiskArea(t *testing.T) {
	tools, ts, _ := newToolWorker(t)
	out := callTool(t, tools.PlannerTools(), "add_intent", `{"summary":"对 /search?q= 做 SQL 注入验证"}`)
	if !strings.Contains(out, "intent created") {
		t.Fatalf("漏填 risk_area 应放行: %s", out)
	}
	if !strings.Contains(out, "推断为 sqli") {
		t.Fatalf("回执应说明推断结果: %s", out)
	}
	nodes, err := ts.ListByKind(db.KindIntent, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := ""
	for _, n := range nodes {
		var p struct {
			Summary  string `json:"summary"`
			RiskArea string `json:"risk_area"`
		}
		_ = json.Unmarshal(n.Payload, &p)
		if strings.Contains(p.Summary, "SQL 注入验证") {
			found = p.RiskArea
		}
	}
	if found != "sqli" {
		t.Fatalf("推断结果应落库为 sqli，实际 %q", found)
	}
	out = callTool(t, tools.PlannerTools(), "add_intent", `{"summary":"对 /x 做验证","risk_area":"nope"}`)
	if !strings.Contains(out, "不在词表内") {
		t.Fatalf("显式填错仍应报错: %s", out)
	}
}

// blocked 语义：平台/外部阻断必须能如实落 blocked（覆盖矩阵记「未测成」），
// 且必须写清原因——否则就是拿它给"没干活"开脱。
func TestConcludeIntentBlockedRequiresNote(t *testing.T) {
	tools, ts, intent := newToolWorker(t)
	if ok, err := ts.ClaimIntent(intent, "work#1", db.IntentLeaseTTL); err != nil || !ok {
		t.Fatalf("claim intent: ok=%v err=%v", ok, err)
	}
	tools.SetOwnerNode(intent)
	conclude := func(input string) string { return callTool(t, tools.WorkerTools(), "conclude_intent", input) }

	out := conclude(`{"outcome":"blocked"}`)
	if !strings.Contains(out, "note") {
		t.Fatalf("blocked 缺原因必须被拒: %s", out)
	}
	out = conclude(`{"outcome":"blocked","note":"平台出口代理不可用：curl 全部 exit 7"}`)
	if !strings.Contains(out, "已记录意图") {
		t.Fatalf("blocked 应被接受: %s", out)
	}
	if got, err := ts.GetIntentSemanticOutcome(intent); err != nil || got != "blocked" {
		t.Fatalf("blocked 语义应落库: %q err=%v", got, err)
	}
}

func TestCoarseIntentSummary(t *testing.T) {
	cases := []struct {
		summary string
		recon   bool
		assets  bool
		coarse  bool
	}{
		{"对目标做认证面测试", false, false, true},
		{"测试注入", false, false, true},
		{"对 https://example.test/admin/login 测账号枚举", false, false, false},
		{"对 /search?q= 做三通道注入验证", false, false, false},
		{"核实 nopCommerce 4.1 的 CVE", false, false, false},
		{"从 finding-4156 继续", false, false, false},
		{"发现 uradi-sam.rs 的同根子域", true, false, false},
		{"侦察目标", true, true, false},
		{"侦察目标", true, false, true},
	}
	for _, c := range cases {
		got, why := coarseIntentSummary(c.summary, c.recon, c.assets)
		if got != c.coarse {
			t.Fatalf("%q recon=%v assets=%v: 期望 coarse=%v 实际 %v（%s）", c.summary, c.recon, c.assets, c.coarse, got, why)
		}
	}
}

// 开局轮：零事实时写「做认证面测试」这类风险类目录必须被拒收；点名具体入口后放行。
func TestAddIntentRejectsCoarseSummaryAtOpening(t *testing.T) {
	tools, _, _ := newToolWorker(t)
	tools.SetOpeningStrict(true)
	out := callTool(t, tools.PlannerTools(), "add_intent", `{"risk_area":"auth","summary":"对目标做认证面测试"}`)
	if !strings.Contains(out, "没有点名任何具体入口") {
		t.Fatalf("开局轮的目录型意图应被拒收：%s", out)
	}
	out = callTool(t, tools.PlannerTools(), "add_intent",
		`{"risk_area":"auth","summary":"对 /admin/login（302→/login?ReturnUrl=/admin）测账号枚举与无锁定下的低速口令喷洒"}`)
	if !strings.Contains(out, "intent created") {
		t.Fatalf("点名了具体入口的意图应通过：%s", out)
	}
}

// 中后段：已有事实可依，粗粒度只留痕不拦，避免白烧一轮。
func TestAddIntentWarnsOnCoarseSummaryAfterOpening(t *testing.T) {
	tools, _, _ := newToolWorker(t)
	out := callTool(t, tools.PlannerTools(), "add_intent", `{"risk_area":"auth","summary":"测试注入"}`)
	if !strings.Contains(out, "intent created") {
		t.Fatalf("中后段的粗粒度意图应放行：%s", out)
	}
	if !strings.Contains(out, "coarse_warning") {
		t.Fatalf("放行时应在回执里留 coarse_warning：%s", out)
	}
}

// 还有候选停在 open_proof_gap 时宣告穷尽必须被拒——那是「没测完」，不是「没得测」。
func TestStrategyExhaustedBlockedByOpenProofGap(t *testing.T) {
	tools, ts, intent := newToolWorker(t)
	gapID, err := ts.AddNode(db.KindFinding, map[string]any{
		"summary":   "/login 的账号枚举差异仍缺后台侧读回面",
		"vulnclass": "auth",
	}, 0, db.FindingOpenProofGap, "work#1", nil)
	if err != nil {
		t.Fatal(err)
	}
	ev := strconv.FormatInt(intent, 10)
	out := callTool(t, tools.PlannerTools(), "mark_strategy_exhausted",
		`{"reason":"各方向都试过了","evidence_ids":[`+ev+`]}`)
	if !strings.Contains(out, "open_proof_gap") {
		t.Fatalf("未闭合候选未在 reason 里点名时应拒绝穷尽：%s", out)
	}
	out = callTool(t, tools.PlannerTools(), "mark_strategy_exhausted",
		`{"reason":"各方向都试过了；#`+strconv.FormatInt(gapID, 10)+` 需要后台会话，属外部阻断","evidence_ids":[`+ev+`]}`)
	if !strings.Contains(out, "strategy exhausted recorded") {
		t.Fatalf("点名后的穷尽声明应放行：%s", out)
	}
}

// intentGateFixture 建一个带范围资产的任务，返回可调用 mark_strategy_exhausted 的
// ToolSet、任务 id 与资产 id。
func intentGateFixture(t *testing.T) (*ToolSet, *db.ExplorationStore, int64, int64) {
	t.Helper()
	d := testDB(t)
	t.Cleanup(func() { d.Close() })
	task, err := d.CreateTaskWithOptions("intent-gate", "穷尽门", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	root := fmt.Sprintf("gate-%d.example", time.Now().UnixNano())
	domain := "www." + root
	assetID, err := d.Assets().UpsertSubdomain(db.UpsertSubdomainReq{Domain: domain, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE domain=$1`, domain) })
	if _, err := d.Assets().AddAgentScope(task.ID, "root_domain", root, "测试范围", "agent"); err != nil {
		t.Fatal(err)
	}
	ts := d.Exploration(task.ExplorationID)
	tools := NewToolSet(ts, "planner")
	tools.SetTaskID(task.ID)
	tools.SetAssetStore(d.Assets(), nil)
	return tools, ts, task.ID, assetID
}

func TestStrategyExhaustedBlockedByUntouchedAssets(t *testing.T) {
	tools, _, _, _ := intentGateFixture(t)
	out := callTool(t, tools.PlannerTools(), "mark_strategy_exhausted",
		`{"reason":"各方向都试过了","evidence_ids":[1]}`)
	if !strings.Contains(out, "一条意图都没派过") {
		t.Fatalf("还有未触达资产时应拒绝穷尽：%s", out)
	}
}

func TestStrategyExhaustedBlockedByShallowAssets(t *testing.T) {
	tools, ts, _, assetID := intentGateFixture(t)
	doneID, err := ts.AddNode(db.KindIntent, map[string]any{
		"summary":   "对 www 做注入验证",
		"risk_area": "sqli",
	}, 5, "done", "planner", []int64{assetID})
	if err != nil {
		t.Fatal(err)
	}
	out := callTool(t, tools.PlannerTools(), "mark_strategy_exhausted",
		`{"reason":"各方向都试过了","evidence_ids":[`+strconv.FormatInt(doneID, 10)+`]}`)
	if !strings.Contains(out, "只测了很少几类风险") {
		t.Fatalf("资产深度不足时应拒绝穷尽：%s", out)
	}
}

// 每个资产终结 3 类风险后，穷尽声明才放行。
func TestStrategyExhaustedPassesAfterDepthReached(t *testing.T) {
	tools, ts, _, assetID := intentGateFixture(t)
	var last int64
	for _, area := range []string{"sqli", "xss", "authz"} {
		id, err := ts.AddNode(db.KindIntent, map[string]any{
			"summary":   "对 www 测 " + area,
			"risk_area": area,
		}, 5, "done", "planner", []int64{assetID})
		if err != nil {
			t.Fatal(err)
		}
		last = id
	}
	out := callTool(t, tools.PlannerTools(), "mark_strategy_exhausted",
		`{"reason":"各方向都试过了","evidence_ids":[`+strconv.FormatInt(last, 10)+`]}`)
	if !strings.Contains(out, "strategy exhausted recorded") {
		t.Fatalf("深度达标后应放行穷尽：%s", out)
	}
}
