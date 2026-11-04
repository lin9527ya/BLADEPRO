package agent

import (
	"context"
	"encoding/json"
	"strings"

	actool "github.com/lin9527ya/Norma/tool"
)

// DivergeDefault, if set, backs divergence_brainstorm: the server fans one
// question out to up to 4 LLM profiles in parallel and merges the answers.
var DivergeDefault func(question string, profiles []string) (string, error)

// divergeTool builds the multi-model brainstorm tool (planner + mainagent).
func (t *ToolSet) divergeTool() actool.CoreTool {
	return readTool("divergence_brainstorm",
		"(思维发散)把一个问题并行交给最多 4 个不同模型独立发散（利用面/业务逻辑/横向联想/反常规视角），返回去重合并后的观点清单（带提出模型标注）。用于规划卡壳、想开新路线、或对高价值目标找多角度打法。本工具的结果不能再触发本工具（防自循环）。",
		obj(map[string]any{
			"question": str("要发散的问题（自包含，含必要上下文）"),
			"profiles": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "LLM 配置名列表（最多 4 个，见 list_llm_profiles；留空用当前激活配置）"},
		}, "question"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if DivergeDefault == nil {
				return actool.Errorf("divergence_brainstorm 不可用：发散后端未接入"), nil
			}
			var args struct {
				Question string   `json:"question"`
				Profiles []string `json:"profiles"`
			}
			if err := json.Unmarshal(in, &args); err != nil || strings.TrimSpace(args.Question) == "" {
				return actool.Errorf("question 必填"), nil
			}
			if len(args.Profiles) > 4 {
				args.Profiles = args.Profiles[:4]
			}
			out, err := DivergeDefault(args.Question, args.Profiles)
			if err != nil {
				return actool.Errorf("发散失败: " + err.Error()), nil
			}
			return actool.Text(out), nil
		})
}
