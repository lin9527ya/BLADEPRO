package agent

import (
	"encoding/json"
	"testing"

	actool "github.com/lin9527ya/Norma/tool"

	"github.com/lin9527ya/bladepro/srcmode"
)

// 工具 schema 必须能被 provider 序列化进请求。曾有过把 itemSchema 自己塞进
// `cells/checks.items` 的写法，形成 map 自引用，导致规划请求在提交前就报
// "json: unsupported value: encountered a cycle"（任务 1 实测）。
func TestToolSchemasAreSerializable(t *testing.T) {
	base := NewToolSet(nil, "planner")
	src := NewToolSet(nil, "planner")
	src.SetSrcMode(srcmode.DefaultDoc(), nil)
	lists := map[string][]actool.CoreTool{
		"planner":     base.PlannerTools(),
		"worker":      base.WorkerTools(),
		"planner-src": src.PlannerTools(),
		"worker-src":  src.WorkerTools(),
	}
	for group, tools := range lists {
		for _, tl := range tools {
			if _, err := json.Marshal(tl.InputSchema()); err != nil {
				t.Errorf("%s/%s schema: %v", group, tl.Name(), err)
			}
		}
	}
	for _, s := range BuiltinToolSeeds() {
		if _, err := json.Marshal(s.Schema); err != nil {
			t.Errorf("builtin seed %s: %v", s.Key, err)
		}
	}
}
