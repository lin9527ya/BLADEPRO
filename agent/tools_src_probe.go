package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	actool "github.com/lin9527ya/Norma/tool"
	"github.com/lin9527ya/bladepro/srcprobe"
)

// SrcProbeRequest 是确定性只读探针的输入；AssetID 决定目标与范围。
type SrcProbeRequest struct {
	TaskID    int64
	AssetID   int64
	Kind      string
	MaxPaths  int
	Endpoints []string
	// Force bypasses the asset-level result cache (paths/js/methods) and re-runs.
	Force bool
}

// SrcProber 由宿主（server）实现：解析资产的 URL/范围、走录制代理、按 QPS 跑探针。
type SrcProber func(ctx context.Context, req SrcProbeRequest) (*srcprobe.Result, error)

// SetSrcProber wires the host-side probe executor (nil = tool reports 未接线).
func (t *ToolSet) SetSrcProber(fn SrcProber) { t.srcProber = fn }

func (t *ToolSet) srcProbeTool() actool.CoreTool {
	return writeTool(
		"src_probe",
		"【SRC 只读探针】对一个资产跑确定性探针，把重复动作从 LLM 回合里拿掉。只发 GET/HEAD/OPTIONS，按任务 QPS 串行、走录制代理留证。\n"+
			"  paths   —— 用内置 API 路径字典枚举（默认 200 条，可调 max_paths）；结果给 paths/non404。\n"+
			"  js      —— 抽首页的内联脚本、同 host 外链 JS、chunk 与 sourcemap；结果给 js_inline/js_external/js_chunks/sourcemap。\n"+
			"  methods —— 对 endpoints（默认资产 URL）测 GET/HEAD/OPTIONS，记录状态与 Allow；状态变更方法不自动发。\n"+
			"  non404  —— 对本任务流量里该 host 的非 404 路由慢速复测 2 次；结果给 non404/retested。\n"+
			"返回的 metrics 与 src_mark_check 的指标同名，可直接用于登记收尾项；探针不会自动点格。\n"+
			"paths/js/methods 的结果按资产缓存 12h（跨任务复用），返回 cached=true 时是历史结果；需要新鲜数据传 force=true。",
		obj(map[string]any{
			"asset_id":  intp("要探测的资产 id（来自 src_state / list_assets）"),
			"kind":      map[string]any{"type": "string", "enum": []string{srcprobe.KindPaths, srcprobe.KindJS, srcprobe.KindMethods, srcprobe.KindNon404}, "description": "探针类型"},
			"max_paths": intp("paths 探针最多跑多少条（默认 200，上限 1000）"),
			"endpoints": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "methods 探针的可选端点列表（URL 或相对路径，默认资产 URL）"},
			"force":     map[string]any{"type": "boolean", "description": "true = 跳过资产级枚举缓存重新跑（默认 false，12h 内的 paths/js/methods 结果会直接复用）"},
		}, "asset_id", "kind"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !t.srcReady() {
				return actool.Errorf("src_probe 仅在 SRC 模式的任务内可用"), nil
			}
			if t.srcProber == nil {
				return actool.Errorf("src_probe 未接线（宿主未注入探针执行器）"), nil
			}
			var req struct {
				AssetID   int64    `json:"asset_id"`
				Kind      string   `json:"kind"`
				MaxPaths  int      `json:"max_paths"`
				Endpoints []string `json:"endpoints"`
				Force     bool     `json:"force"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			if req.AssetID <= 0 {
				return actool.Errorf("asset_id 必填且必须为正数"), nil
			}
			kind := strings.ToLower(strings.TrimSpace(req.Kind))
			switch kind {
			case srcprobe.KindPaths, srcprobe.KindJS, srcprobe.KindMethods, srcprobe.KindNon404:
			default:
				return actool.Errorf("kind 只能是 paths / js / methods / non404"), nil
			}
			res, err := t.srcProber(ctx, SrcProbeRequest{
				TaskID: t.taskID, AssetID: req.AssetID, Kind: kind,
				MaxPaths: req.MaxPaths, Endpoints: req.Endpoints, Force: req.Force,
			})
			if err != nil {
				return actool.Errorf("探针失败：" + err.Error()), nil
			}
			return jsonResult(map[string]any{
				"probe":   res,
				"metrics": res.Metrics,
				"note":    fmt.Sprintf("只读探针完成：%d 个请求，用时 %dms；metrics 可直接用于 src_mark_check。", res.Requests, res.ElapsedMS),
			})
		})
}
