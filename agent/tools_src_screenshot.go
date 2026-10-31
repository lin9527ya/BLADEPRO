package agent

// src_screenshot：宿主用 headless 浏览器对资产截图，落成 evidence_artifacts，
// 并创建一条 fact 证据节点。平台要求截图时，src_mark_check(screenshots, done)
// 未给证据节点会自动调用同一实现。

import (
	"context"
	"encoding/json"
	"fmt"

	actool "github.com/lin9527ya/Norma/tool"
)

type SrcScreenshotRequest struct {
	TaskID  int64
	AssetID int64
	// URL 可选：仅允许同 host 的已授权 URL；空 = 用资产的主 URL。
	URL      string
	Note     string
	Width    int
	Height   int
	FullPage bool
	// Steps 是截图前的关键交互步骤（点按钮、填表单、等待渲染）。只允许固定动作，
	// 不执行任意 JS——目标页里的脚本已经不可信，再让 agent 注入 JS 等于绕开 RoE。
	Steps []SrcScreenshotStep
}

// SrcScreenshotStep is one pre-screenshot interaction. Action is one of:
// navigate / click / fill / press / wait / scroll.
type SrcScreenshotStep struct {
	Action   string `json:"action"`
	URL      string `json:"url,omitempty"`      // navigate：必须是任务范围内 URL
	Selector string `json:"selector,omitempty"` // click/fill/press：CSS selector
	Value    string `json:"value,omitempty"`    // fill 文本 / press 键名
	WaitMS   int    `json:"wait_ms,omitempty"`  // wait：毫秒（上限 10s）
	Y        int    `json:"y,omitempty"`        // scroll：纵向像素
}

type SrcScreenshotResult struct {
	NodeID     int64  `json:"node_id"`
	ArtifactID int64  `json:"artifact_id"`
	URL        string `json:"url"`
	MIME       string `json:"mime,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Note       string `json:"note,omitempty"`
}

// SrcScreenshotter 由宿主（server）实现：校验范围/时间窗、headless 截图、落证据。
type SrcScreenshotter func(ctx context.Context, req SrcScreenshotRequest) (*SrcScreenshotResult, error)

func (t *ToolSet) SetSrcScreenshotter(fn SrcScreenshotter) { t.srcScreenshotter = fn }

// 三个 agent 的接入口：ToolSet 每次 run 新建，实现挂在 agent 结构体上。
func (p *Planner) SetSrcScreenshotter(fn SrcScreenshotter) { p.srcScreenshotter = fn }
func (w *Worker) SetSrcScreenshotter(fn SrcScreenshotter)  { w.srcScreenshotter = fn }

func (t *ToolSet) srcScreenshotTool() actool.CoreTool {
	return writeTool(
		"src_screenshot",
		"【SRC 证据】用 headless 浏览器对资产（或同 host 的指定 URL）截图，保存为证据产物并创建一条 fact 证据节点。\n"+
			"返回 node_id 可直接作为 src_mark_check 的 evidence_node_id（尤其 screenshots 收尾项）。\n"+
			"截图走录制代理、校验任务范围与作业时间窗；steps 可做固定交互（填表/点击/等待/滚动）以截到关键步骤，但不执行任意 JS；navigate 目标同样受范围门约束。\n"+
			"平台要求截图（require_screenshots）时，src_mark_check 的 screenshots done 若未给证据节点，系统会自动截一张。",
		obj(map[string]any{
			"asset_id":  intp("要截图的资产 id（来自 src_state / list_assets）"),
			"url":       str("可选：同 host 的具体页面 URL；留空 = 资产主 URL"),
			"note":      str("截图说明（写进证据节点摘要）"),
			"width":     intp("视口宽度，默认 1280"),
			"height":    intp("视口高度，默认 900"),
			"full_page": map[string]any{"type": "boolean", "description": "true = 截整页（默认只截视口）"},
			"steps": map[string]any{
				"type": "array",
				"description": "截图前的关键交互（最多 20 步）。动作：navigate(url) / click(selector) / fill(selector,value) / " +
					"press(selector,value=Enter) / wait(wait_ms) / scroll(y)。不执行任意 JS；navigate 目标必须在授权范围内。",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":   str("navigate | click | fill | press | wait | scroll"),
						"url":      str("navigate 的绝对 URL（必须在任务范围内）"),
						"selector": str("CSS selector"),
						"value":    str("fill 的文本 / press 的键名"),
						"wait_ms":  intp("wait 毫秒，上限 10000"),
						"y":        intp("scroll 纵向像素"),
					},
					"required": []any{"action"},
				},
			},
		}, "asset_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !t.srcReady() {
				return actool.Errorf("src_screenshot 仅在 SRC 模式的任务内可用"), nil
			}
			if t.srcScreenshotter == nil {
				return actool.Errorf("src_screenshot 未接线（宿主没有注入截图实现）"), nil
			}
			var req struct {
				AssetID  int64               `json:"asset_id"`
				URL      string              `json:"url"`
				Note     string              `json:"note"`
				Width    int                 `json:"width"`
				Height   int                 `json:"height"`
				FullPage bool                `json:"full_page"`
				Steps    []SrcScreenshotStep `json:"steps"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			if req.AssetID <= 0 {
				return actool.Errorf("asset_id 必填且必须为正数"), nil
			}
			res, err := t.srcScreenshotter(ctx, SrcScreenshotRequest{
				TaskID: t.taskID, AssetID: req.AssetID, URL: req.URL, Note: req.Note,
				Width: req.Width, Height: req.Height, FullPage: req.FullPage, Steps: req.Steps,
			})
			if err != nil {
				return actool.Errorf("截图失败：" + err.Error()), nil
			}
			t.writes.Evidence++
			return jsonResult(map[string]any{
				"screenshot": res,
				"note":       fmt.Sprintf("截图已保存为证据 #%d，可把 node_id=%d 填进 src_mark_check 的 evidence_node_id。", res.ArtifactID, res.NodeID),
			})
		})
}
