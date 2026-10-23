package srcmode

import (
	"strings"
	"testing"
)

// TestStripPentestOnly 钉住 SRC 模式下要删掉的段落：据点/凭据/webshell 与
// "深度优先于覆盖度"。report_finding 行里提到 report_artifact 不能误删。
func TestStripPentestOnly(t *testing.T) {
	in := strings.Join([]string{
		"keep-1",
		"- **拿到据点 → report_foothold（探索图）**：反弹 shell / webshell",
		"  · 状态如实标：active / unknown",
		"  · 清掉之后用 mark_artifact_cleared 销账",
		"- **读到凭据 → report_credential（探索图）**：口令 / 哈希",
		"- **留在目标上的东西 → report_artifact（探索图）**：webshell、建账号",
		"  · 状态如实标：active",
		"- **确认漏洞 → report_finding（探索图，含 PoC）**：落地了文件同时用 report_artifact 记一条",
		"   - **深度优先于覆盖度**：优先把那条路往深打穿",
		"keep-2",
	}, "\n")
	got := StripPentestOnly(in)
	for _, gone := range []string{"report_foothold", "report_credential", "留在目标上的东西", "mark_artifact_cleared", "深度优先于覆盖度", "状态如实标"} {
		if strings.Contains(got, gone) {
			t.Fatalf("应删除 %q：\n%s", gone, got)
		}
	}
	for _, keep := range []string{"keep-1", "keep-2", "report_finding", "report_artifact 记一条"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("不该删 %q：\n%s", keep, got)
		}
	}
}

func TestSrcPromptNotes(t *testing.T) {
	for name, text := range map[string]string{
		"precedence": SrcPrecedenceNote(),
		"worker":     SrcWrapupNote(),
		"planner":    SrcPlannerWrapupNote(),
	} {
		if strings.TrimSpace(text) == "" {
			t.Fatalf("%s note 不能为空", name)
		}
	}
	if !strings.Contains(SrcWrapupNote(), "webshell") || !strings.Contains(SrcPlannerWrapupNote(), "prove_goal") {
		t.Fatal("收尾 note 必须点名 webshell 与 prove_goal")
	}
}
