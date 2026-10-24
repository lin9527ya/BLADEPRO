package agent

// src_negative：结构化负知识的读写工具。
//
// 为什么单独一个工具而不是塞进 src_mark_cell：格是“这个面测过”，负知识是
// “这个具体入口 + 打法确认不通”。后者粒度更细、可跨任务复用，也不该因为某个格
// 还没收口就被埋在格子的 reason 里。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	actool "github.com/lin9527ya/Norma/tool"
	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

func (t *ToolSet) srcNegativeTool() actool.CoreTool {
	return writeTool(
		"src_negative",
		"【SRC 模式】记录/查看“这个资产（或具体端点）的这个打法走不通”的结构化负知识。\n"+
			"用法：\n"+
			"  action=add（默认）——记录一条；asset_id 必填且在任务范围内，area 用 src_state 的面名，endpoint/technique 可空，reason 写清证据；\n"+
			"  action=list——列出本任务范围内资产的负知识（含其他任务留下的，用于跨任务复用）；传 asset_id 可只看一个资产；\n"+
			"  action=clear——删除本任务记录的一条（id）。\n"+
			"负知识不是“没测”：没测的格请用 src_mark_cell。发现新证据推翻了旧结论时，用 add 覆盖同键记录。",
		obj(map[string]any{
			"action":    str("add | list | clear（默认 add）"),
			"id":        intp("clear 时要删除的负知识 id"),
			"asset_id":  intp("资产 id（add 必填；list 可选）"),
			"area":      str("风险面：" + srcmode.AreaLegend()),
			"endpoint":  str("具体入口（可选，如 POST /api/orders/{id}）"),
			"technique": str("打法/指纹（可选，如 sql_error_based / idor_swap_id）"),
			"reason":    str("为什么走不通（add 必填，写证据与结论）"),
			"limit":     intp("list 返回条数，默认 50，最大 200"),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !t.srcReady() {
				return actool.Errorf("src_negative 仅在 SRC 模式的任务内可用"), nil
			}
			var req struct {
				Action    string `json:"action"`
				ID        int64  `json:"id"`
				AssetID   int64  `json:"asset_id"`
				Area      string `json:"area"`
				Endpoint  string `json:"endpoint"`
				Technique string `json:"technique"`
				Reason    string `json:"reason"`
				Limit     int    `json:"limit"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			switch strings.ToLower(strings.TrimSpace(req.Action)) {
			case "", "add":
				if req.AssetID <= 0 {
					return actool.Errorf("add 需要 asset_id"), nil
				}
				asset, err := t.srcStore.SrcAssetInScope(ctx, t.taskID, req.AssetID)
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				if asset == nil {
					return actool.Errorf(fmt.Sprintf("资产 %d 不在本任务测试范围内，不能记录负知识", req.AssetID)), nil
				}
				area := strings.TrimSpace(req.Area)
				if area != "" && !srcmode.IsArea(area) {
					return actool.Errorf(fmt.Sprintf("未知风险面 %q；可用：%s", area, srcmode.AreaLegend())), nil
				}
				if strings.TrimSpace(req.Reason) == "" {
					return actool.Errorf("reason 必填：写清为什么这个打法走不通"), nil
				}
				row, err := t.srcStore.UpsertSrcNegative(ctx, db.SrcNegative{
					TaskID: t.taskID, AssetID: req.AssetID, Area: area,
					Endpoint: req.Endpoint, Technique: req.Technique, Reason: req.Reason,
					Source: "agent",
				})
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				row.Asset = asset.Label
				return jsonResult(map[string]any{"negative": row})
			case "list":
				var rows []db.SrcNegative
				var err error
				if req.AssetID > 0 {
					asset, aerr := t.srcStore.SrcAssetInScope(ctx, t.taskID, req.AssetID)
					if aerr != nil {
						return actool.Errorf(aerr.Error()), nil
					}
					if asset == nil {
						return actool.Errorf(fmt.Sprintf("资产 %d 不在本任务测试范围内", req.AssetID)), nil
					}
					rows, err = t.srcStore.ListSrcNegativesForAsset(ctx, req.AssetID, req.Limit)
				} else {
					rows, err = t.srcStore.ListSrcNegativesForTask(ctx, t.taskID, req.Limit)
				}
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				return jsonResult(map[string]any{"negatives": rows, "total": len(rows)})
			case "clear":
				if req.ID <= 0 {
					return actool.Errorf("clear 需要 id"), nil
				}
				found, err := t.srcStore.DeleteSrcNegative(ctx, t.taskID, req.ID)
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				if !found {
					return actool.Errorf(fmt.Sprintf("负知识 #%d 不存在，或不属于本任务", req.ID)), nil
				}
				return jsonResult(map[string]any{"deleted": req.ID})
			default:
				return actool.Errorf("action 只能是 add / list / clear"), nil
			}
		})
}
