package notify

import (
	"strings"
	"testing"
)

// TestTaskEventFilter pins the opt-in and the deliberately narrow filter surface:
// once a channel enables task alerts, vulnclass/severity filters designed for
// findings must not silently suppress them.
func TestTaskEventFilter(t *testing.T) {
	snap := Snapshot{Kind: EventSrcBudget, TaskID: 7, Severity: "medium", Title: "预算告警"}
	if Match(Filter{}, snap) {
		t.Fatal("task alerts must be opt-in")
	}
	if !Match(Filter{OnTaskEvents: true}, snap) {
		t.Fatal("on_task_events should accept the alert")
	}
	f := Filter{OnTaskEvents: true, MinSeverity: "critical", VulnClassInclude: []string{"SQL注入"}, AssetIDs: []int64{99}}
	if !Match(f, snap) {
		t.Fatal("finding-only filters must not suppress task alerts")
	}
	if Match(Filter{OnTaskEvents: true, TaskIDs: []int64{8}}, snap) {
		t.Fatal("task id filter should still apply")
	}
	if !Match(Filter{OnTaskEvents: true, TaskIDs: []int64{7}}, snap) {
		t.Fatal("task id filter should accept matching task")
	}
	if Match(Filter{OnTaskEvents: true}, Snapshot{Kind: EventFindingStatusChanged, Severity: "high"}) {
		t.Fatal("status change still needs its own opt-in")
	}
}

func TestTaskEventMarkdownRendering(t *testing.T) {
	it := Item{EventKind: EventSrcGateRejected, EventTitle: "收尾门拒绝 done", EventBody: "还有 3 个缺口", Severity: "medium"}
	m := Message{Items: []Item{it}}
	if title := markdownTitle(m); !strings.Contains(title, "收尾门拒绝") || !strings.Contains(title, "收尾门拒绝 done") {
		t.Fatalf("title=%q", title)
	}
	body, kept := markdownBody(m, 0)
	if kept != 1 || !strings.Contains(body, "收尾门拒绝 done") || !strings.Contains(body, "还有 3 个缺口") {
		t.Fatalf("body=%q kept=%d", body, kept)
	}
	// 批次标题只在全是任务告警时才改成「任务告警」。
	batch := Message{Batch: true, Items: []Item{it, it}}
	if title := markdownTitle(batch); !strings.HasPrefix(title, "任务告警") {
		t.Fatalf("batch title=%q", title)
	}
	mixed := Message{Batch: true, Items: []Item{it, {FindingID: 1, Severity: "high", VulnClass: "XSS"}}}
	if title := markdownTitle(mixed); !strings.HasPrefix(title, "漏洞汇总") {
		t.Fatalf("mixed title=%q", title)
	}
}
