package agent

import (
	"strings"
	"testing"
)

// TestSystemPromptCatalogVarsArePopulated 钉住"目录里承诺的变量必须真的传进来"：
// 用户模板引用 AssetSummary / WorkerName 时，过去会因为调用方没传而整段渲染失败、
// 静默回退到内置默认（用户以为自己的提示词生效了，其实没有）。
func TestSystemPromptCatalogVarsArePopulated(t *testing.T) {
	old := PromptOverride
	defer func() { PromptOverride = old }()
	PromptOverride = func(key string) (string, bool) {
		switch key {
		case "worker":
			return "W={{.WorkerName}} P={{.ProxyAddr}} D={{.DataDir}} N={{.Now}}", true
		case "planner":
			return "A={{.AssetSummary}} G={{.Goal}}", true
		case "mainagent":
			return "A={{.AssetSummary}} G={{.Goal}}", true
		}
		return "", false
	}

	got := workerSystem("proxy:1", "", "/data", "/run", "worker-9")
	for _, want := range []string{"W=worker-9", "P=proxy:1", "D=/data", "N="} {
		if !strings.Contains(got, want) {
			t.Fatalf("worker system 缺变量渲染 %q：%s", want, got)
		}
	}
	if got := plannerSystem("goal-x", "/data", "/run", "app:2 service:1"); !strings.Contains(got, "A=app:2 service:1") || !strings.Contains(got, "G=goal-x") {
		t.Fatalf("planner system 缺变量渲染：%s", got)
	}
	if got := mainAgentSystem("goal-y", "/data", "/run", "app:2"); !strings.Contains(got, "A=app:2") || !strings.Contains(got, "G=goal-y") {
		t.Fatalf("mainagent system 缺变量渲染：%s", got)
	}
}
