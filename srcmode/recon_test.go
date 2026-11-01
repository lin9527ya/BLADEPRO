package srcmode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestResolveReconOnly 确认 recon_only 能被解析，并强制关掉交付门与收尾闸——
// 侦察任务没有覆盖格/交付件，不关这两道门任务永远收不了尾。
func TestResolveReconOnly(t *testing.T) {
	raw := json.RawMessage(`{"recon_only":true}`)
	_, doc, err := Resolve(ModeSrc, raw)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !doc.ReconOnly || !doc.ReconOnlyMode() {
		t.Fatalf("recon_only 未生效：%+v", doc)
	}
	if !doc.SkipReportGate || !doc.SkipCheckGate || doc.CheckTier != "off" {
		t.Fatalf("recon_only 应强制关闭交付门/收尾闸：skip_report=%v skip_check=%v tier=%q",
			doc.SkipReportGate, doc.SkipCheckGate, doc.CheckTier)
	}
}

// TestResolveReconOnlyOff 确保普通 SRC 任务不受影响。
func TestResolveReconOnlyOff(t *testing.T) {
	_, doc, err := Resolve(ModeSrc, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if doc.ReconOnly {
		t.Fatal("默认不应是侦察任务")
	}
}

// TestCheckFinishReconOnlyWithoutStore 与 CheckFinish 同口径：没有存储时不臆造缺口。
func TestCheckFinishReconOnlyWithoutStore(t *testing.T) {
	rep, err := CheckFinish(context.Background(), nil, 42, ModeDoc{Mode: ModeSrc, ReconOnly: true})
	if err != nil {
		t.Fatalf("CheckFinish: %v", err)
	}
	if !rep.Finishable || rep.GapsTotal != 0 {
		t.Fatalf("无存储时应放行且无缺口：%+v", rep)
	}
}

// TestReconOnlyOverlays 确认侦察任务的 planner/worker 提示走专用段落，
// 且不再要求覆盖格/交付件。
func TestReconOnlyOverlays(t *testing.T) {
	doc := ModeDoc{Mode: ModeSrc, ReconOnly: true}
	p := PlannerOverlay(doc)
	if !strings.Contains(p, "只做信息收集") {
		t.Fatalf("planner overlay 未切换为侦察段落：%s", p)
	}
	if strings.Contains(p, "覆盖矩阵（站点资产 × 风险面）") {
		t.Fatal("侦察任务不应再注入覆盖矩阵要求")
	}
	w := WorkerOverlay(doc)
	if !strings.Contains(w, "信息收集") || !strings.Contains(w, "src-recon") {
		t.Fatalf("worker overlay 未切换为侦察段落：%s", w)
	}
}

// TestReconGateOverlayMentionsEnscan 任务内侦察（recon_gate）要在 planner 里
// 明确"开案先派资产测绘意图"，否则规划层不会主动去摸企业面。
func TestReconGateOverlayMentionsEnscan(t *testing.T) {
	gate := true
	p := PlannerOverlay(ModeDoc{Mode: ModeSrc, ReconGate: &gate})
	if !strings.Contains(p, "资产测绘") || !strings.Contains(p, "ENScan") {
		t.Fatalf("recon_gate 提示缺少开案侦察说明")
	}
	w := WorkerOverlay(ModeDoc{Mode: ModeSrc, ReconGate: &gate})
	if !strings.Contains(w, "src-recon") {
		t.Fatalf("worker 提示缺少 src-recon 加载说明")
	}
}

// TestReconActiveRequiresReconOnly 主动验证只能挂在只做信息收集的任务上——
// 普通 SRC 任务本来就能主动探测，这个字段没有意义，容易误用。
func TestReconActiveRequiresReconOnly(t *testing.T) {
	if _, _, err := Resolve(ModeSrc, json.RawMessage(`{"recon_active":true}`)); err == nil {
		t.Fatal("recon_active 不带 recon_only 时应被拒绝")
	}
}

// TestReconActiveRejectedWithNoAutomation 平台禁止自动化时不允许对目标发任何请求。
func TestReconActiveRejectedWithNoAutomation(t *testing.T) {
	raw := json.RawMessage(`{"recon_only":true,"recon_active":true,"no_automation":true}`)
	if _, _, err := Resolve(ModeSrc, raw); err == nil {
		t.Fatal("no_automation 时开启 recon_active 应被拒绝")
	}
}

// TestReconActiveAccepted 合法的组合能解析，并保留 recon_active。
func TestReconActiveAccepted(t *testing.T) {
	_, doc, err := Resolve(ModeSrc, json.RawMessage(`{"recon_only":true,"recon_active":true}`))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !doc.ReconActive || !doc.ReconOnly {
		t.Fatalf("recon_active/recon_only 未生效：%+v", doc)
	}
}

// TestReconOnlyWorkerOverlayActive 开启主动验证后，worker 提示要允许 alive/fingerprint。
func TestReconOnlyWorkerOverlayActive(t *testing.T) {
	on := WorkerOverlay(ModeDoc{Mode: ModeSrc, ReconOnly: true, ReconActive: true})
	if !strings.Contains(on, "alive") || !strings.Contains(on, "fingerprint") {
		t.Fatalf("开启主动验证后 worker 提示未放行 alive/fingerprint：%s", on)
	}
	off := WorkerOverlay(ModeDoc{Mode: ModeSrc, ReconOnly: true})
	if strings.Contains(off, "kind=alive") {
		t.Fatal("未开启主动验证时不应提示可以调 alive 探针")
	}
}
