package agent

import (
	"testing"

	"github.com/lin9527ya/bladepro/guard"
)

// TestPlannerGuardHooks 钉住「规划者也必须过拦截面」这条接线。planner 手里有
// Bash / run_execution，这条不变量断掉时不会报错，只会让 RoE 范围门与拦截规则
// 对规划者静默失效。
func TestPlannerGuardHooks(t *testing.T) {
	p := NewPlanner(nil, "m", "", nil, 0, 0)
	if h := p.guardHooks(); h != nil {
		t.Fatal("未挂 guard 时不应安装 hooks")
	}
	p.SetGuard(guard.New())
	if h := p.guardHooks(); h == nil {
		t.Fatal("挂了 guard 就必须产出 hooks（否则范围门/规则/裁判全失效）")
	}
}
