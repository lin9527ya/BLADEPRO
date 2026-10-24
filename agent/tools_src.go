package agent

// SRC 挖掘模式（tasks.mode='src'）的三个专属工具 + 态势块。
//
// 这些工具**不进 tools 表**（docs/SRC挖掘agent改造方案.md §8.1）：wireTools 会把
// 「DB 里绑定了该 agent、但 base 列表里没有」的域工具注入回去，一旦 seed 进去，
// 非 SRC 任务也会被注入。它们只由 AppendSrcTools 在 mode=src 时按角色追加。

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	actool "github.com/lin9527ya/Norma/tool"
	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

// SrcSnapshot 把完整覆盖快照暴露给宿主（HTTP API），保证 UI 与 agent 读的是同一份计算。
func (t *ToolSet) SrcSnapshot(ctx context.Context) (map[string]any, error) {
	return t.srcSnapshot(ctx)
}

// srcSnapshot 是 src_state 与 GET /api/tasks/{id}/src 共用的完整快照。
func (t *ToolSet) srcSnapshot(ctx context.Context) (map[string]any, error) {
	if t.srcStore == nil {
		return nil, fmt.Errorf("SRC 覆盖存储未接线")
	}
	rep, err := srcmode.CheckFinish(ctx, t.srcStore, t.taskID, t.srcDoc)
	if err != nil {
		return nil, err
	}
	cells, err := t.srcStore.ListSrcCells(ctx, t.taskID)
	if err != nil {
		return nil, err
	}
	assets, err := t.srcStore.InScopeSrcAssets(ctx, t.taskID)
	if err != nil {
		return nil, err
	}
	assetIDs := make([]int64, 0, len(assets))
	for _, a := range assets {
		assetIDs = append(assetIDs, a.ID)
	}
	priorCoverage, _ := t.srcStore.PriorSrcCoverage(ctx, t.taskID, assetIDs, time.Now().Add(-30*24*time.Hour), 60)
	var assetUsage []db.AssetTokenUsage
	if t.srcDoc.AssetTokenBudget > 0 {
		assetUsage, _ = t.srcStore.AssetTokenUsageByTask(ctx, t.taskID)
		if len(assetUsage) > 50 {
			assetUsage = assetUsage[:50]
		}
	}
	// 一次遍历建索引：大任务下把 O(资产 × 项 × 全量 cells) 的嵌套扫描降成 O(cells)。
	// 键是 assetID|area|item；同一格理论上只有一行（唯一约束），这里保留第一条也更稳。
	cellIndex := make(map[string]db.SrcCell, len(cells))
	for _, c := range cells {
		k := srcCellKey(c.AssetID, c.Area, c.Item)
		if _, ok := cellIndex[k]; !ok {
			cellIndex[k] = c
		}
	}
	matrix := make([]map[string]any, 0, len(assets))
	for _, a := range assets {
		areas := t.srcDoc.RequiredFor(a.Type)
		row := make([]map[string]any, 0, len(areas))
		for _, area := range areas {
			items := []string{srcmode.AreaAll}
			if hard := t.srcDoc.HardItemsFor(area); len(hard) > 0 {
				items = hard
			}
			for _, item := range items {
				cell, found := cellIndex[srcCellKey(a.ID, area, item)]
				entry := map[string]any{
					"area": area, "item": item, "title": srcmode.AreaTitle(area),
					"weight": srcmode.AreaWeight(area),
				}
				if found {
					entry["state"] = cell.State
					entry["reason"] = cell.Reason
					entry["attempts"] = cell.Attempts
					if cell.EvidenceNodeID > 0 {
						entry["evidence_node_id"] = cell.EvidenceNodeID
					}
					if cell.FindingID > 0 {
						entry["finding_id"] = cell.FindingID
					}
					if cell.InheritedFromTaskID > 0 {
						entry["inherited_from_task_id"] = cell.InheritedFromTaskID
					}
				}
				row = append(row, entry)
			}
		}
		matrix = append(matrix, map[string]any{
			"asset_id": a.ID, "asset": a.Label, "asset_type": a.Type,
			"required_areas": areas, "cells": row,
		})
	}
	// 收尾闸（F1）：每个资产 × 适用收尾项，连定义（门槛/理由码）一起给，
	// 这样 agent 不用猜"怎样才算做完"，UI 也能直接渲染。
	checksByAsset, err := t.srcStore.SrcChecksByAsset(ctx, t.taskID)
	if err != nil {
		return nil, err
	}
	checkRows := make([]map[string]any, 0, len(assets))
	for _, a := range assets {
		defs := srcmode.ChecksFor(a.Type)
		if t.srcDoc.SkipCheckGate || t.srcDoc.Tier() == srcmode.CheckTierOff || len(defs) == 0 {
			continue
		}
		items := make([]map[string]any, 0, len(defs))
		for _, def := range defs {
			entry := map[string]any{
				"key": def.Key, "title": def.Title, "hint": def.Hint,
				"weight": def.Weight, "require_evidence": def.RequireEvidence,
			}
			fields := make([]map[string]any, 0, len(def.Metrics))
			for _, m := range def.Metrics {
				fields = append(fields, map[string]any{
					"key": m.Key, "label": m.Label, "min": t.srcDoc.Threshold(m.Key),
				})
			}
			entry["metrics_fields"] = fields
			if na := def.CheckNACodes(); len(na) > 0 {
				entry["na_codes"] = na
			}
			if c, ok := checksByAsset[a.ID][def.Key]; ok {
				entry["state"] = c.State
				entry["na_code"] = c.NACode
				entry["reason"] = c.Reason
				entry["metrics"] = c.Metrics
				if c.EvidenceNodeID > 0 {
					entry["evidence_node_id"] = c.EvidenceNodeID
				}
				if c.InheritedFromTaskID > 0 {
					entry["inherited_from_task_id"] = c.InheritedFromTaskID
				}
				// 截图项把产物 id 一并给出，UI 可直接预览（不再只看到一个 fact 节点）。
				if def.Key == "screenshots" && c.EvidenceNodeID > 0 {
					if art, err := t.srcStore.ScreenshotArtifactForNode(ctx, t.taskID, c.EvidenceNodeID); err == nil && art != nil {
						entry["artifact_id"] = art.ID
					}
				}
				// 继承项只核对数字与流量，不重跑写入校验（证据在源任务）。
				if c.State == db.SrcCheckDone && c.InheritedFromTaskID == 0 && t.srcVerifier != nil {
					warns, obs := t.srcVerifier(ctx, t.taskID, a.ID, def.Key, c.Metrics)
					if len(obs) > 0 {
						entry["observed_metrics"] = obs
					}
					if len(warns) > 0 {
						entry["verify_warnings"] = warns
					}
				}
			}
			items = append(items, entry)
		}
		checkRows = append(checkRows, map[string]any{
			"asset_id": a.ID, "asset": a.Label, "asset_type": a.Type, "items": items,
		})
	}
	leases, _ := t.srcStore.ListActiveSrcLeases(ctx, t.taskID)
	return map[string]any{
		"mode":               srcmode.ModeSrc,
		"summary":            rep,
		"gaps":               rep.Gaps,
		"leases":             leases,
		"gaps_total":         rep.GapsTotal,
		"gaps_by_area":       rep.GapsByArea,
		"checks":             checkRows,
		"cells":              cells,
		"assets":             matrix,
		"asset_token_usage":  assetUsage,
		"prior_coverage":     priorCoverage,
		"negative_knowledge": t.srcNegativeBriefs(ctx, t.taskID, 20),
		"finishable":         rep.Finishable,
		"doc": map[string]any{
			"allow_budget_stop":     t.srcDoc.AllowBudgetStop,
			"max_attempts_per_cell": t.srcDoc.MaxAttemptsPerCell,
			"max_intents_per_asset": t.srcDoc.MaxIntentsPerAsset,
			"platform":              t.srcDoc.Platform,
			"program":               t.srcDoc.Program,
			"platform_template":     t.srcDoc.PlatformTemplate,
			"check_tier":            t.srcDoc.Tier(),
			"require_screenshots":   t.srcDoc.RequireScreenshots,
			"require_retest":        t.srcDoc.RequireRetest,
			"area_roi":              t.srcDoc.AreaROI,
			"asset_token_budget":    t.srcDoc.AssetTokenBudget,
			"checks_total":          rep.ChecksTotal,
			"checks_done":           rep.ChecksDone,
		},
		"legend": srcmode.AreaLegend(),
		// 交付面（P2）：台账随快照一起给，agent 与 UI 看的是同一份"交到哪一步"。
		"submissions": t.srcSubmissionBriefs(ctx),
	}, nil
}

// srcNegativeBriefs returns structured negative knowledge for the task's assets.
// Read failures degrade to an empty list: negative knowledge is an accelerator,
// not a precondition for planning.
func (t *ToolSet) srcNegativeBriefs(ctx context.Context, taskID int64, limit int) []db.SrcNegative {
	if t.srcStore == nil || taskID <= 0 {
		return nil
	}
	rows, err := t.srcStore.ListSrcNegativesForTask(ctx, taskID, limit)
	if err != nil {
		return nil
	}
	return rows
}

// srcSubmissionBriefs 是台账的精简视图（去掉正文字段：正文只在导出与详情里取）。
func (t *ToolSet) srcSubmissionBriefs(ctx context.Context) []map[string]any {
	if t.srcStore == nil || t.taskID <= 0 {
		return nil
	}
	subs, err := t.srcStore.ListSrcSubmissions(ctx, db.SrcSubmissionFilter{TaskID: t.taskID, Limit: 100})
	if err != nil || len(subs) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(subs))
	for _, s := range subs {
		m := map[string]any{
			"id": s.ID, "finding_id": s.FindingID, "title": s.Title, "severity": s.Severity,
			"status": s.Status, "platform": s.Platform, "external_id": s.ExternalID,
			"reward": s.Reward,
		}
		if s.Fingerprint != "" {
			m["fingerprint"] = s.Fingerprint
		}
		out = append(out, m)
	}
	return out
}

// srcOverviewBlock 是 graph_overview 里的 src 摘要块（只放计数 + 前 10 条缺口）。
func (t *ToolSet) srcOverviewBlock(ctx context.Context) (map[string]any, error) {
	if !t.srcReady() {
		return nil, nil
	}
	rep, err := srcmode.CheckFinish(ctx, t.srcStore, t.taskID, t.srcDoc)
	if err != nil {
		return nil, err
	}
	top := rep.Gaps
	if len(top) > 10 {
		top = top[:10]
	}
	leases, _ := t.srcStore.ListActiveSrcLeases(ctx, t.taskID)
	topLeases := leases
	if len(topLeases) > 20 {
		topLeases = topLeases[:20]
	}
	out := map[string]any{
		"gaps_total":            rep.GapsTotal,
		"leases":                topLeases,
		"leased_total":          len(leases),
		"gaps_by_area":          rep.GapsByArea,
		"top_gaps":              top,
		"cells_total":           rep.CellsTotal,
		"cells_done":            rep.CellsTerminal,
		"cells_measured":        rep.CellsMeasured,
		"assets":                rep.Assets,
		"finishable":            rep.Finishable,
		"blocked_finish":        !rep.Finishable,
		"blocked_reason":        rep.BlockedReason,
		"focus_asset":           rep.FocusAsset,
		"focus_asset_id":        rep.FocusAssetID,
		"checks_total":          rep.ChecksTotal,
		"checks_done":           rep.ChecksDone,
		"checks_by_key":         rep.ChecksByKey,
		"area_roi":              t.srcDoc.AreaROI,
		"cold_assets":           rep.ColdAssets,
		"cold_total":            len(rep.ColdAssets),
		"recon_pending":         rep.ReconPending,
		"asset_yield":           rep.AssetYield,
		"suspicious_na":         rep.SuspiciousNA,
		"allow_budget_stop":     t.srcDoc.AllowBudgetStop,
		"max_attempts_per_cell": t.srcDoc.MaxAttemptsPerCell,
		"note": "SRC 模式：本轮只派能点亮空格、或把 tested_clear 变 tested_found、或做实收尾项的意图；" +
			"gaps_total>0 时不要 prove_goal。focus_asset 是当前最该清零的资产。",
	}
	// 跨任务复用提示：同一资产在其他 SRC 任务里的最近终态（只读，不改本任务格）。
	scopeAssets, _ := t.srcStore.InScopeSrcAssets(ctx, t.taskID)
	scopeAssetIDs := make([]int64, 0, len(scopeAssets))
	for _, a := range scopeAssets {
		scopeAssetIDs = append(scopeAssetIDs, a.ID)
	}
	if prior, err := t.srcStore.PriorSrcCoverage(ctx, t.taskID, scopeAssetIDs, time.Now().Add(-30*24*time.Hour), 10); err == nil && len(prior) > 0 {
		out["prior_coverage"] = prior
		out["prior_coverage_note"] = "其他任务在相同资产 × 风险面上的最近结论；tested_clear/na 可直接复用（建任务时开启覆盖继承会自动带入），tested_found 先确认是否已交付。"
	}
	if negatives := t.srcNegativeBriefs(ctx, t.taskID, 20); len(negatives) > 0 {
		out["negative_knowledge"] = negatives
		out["negative_note"] = "本任务范围内资产已确认走不通的打法（含其他任务留下的）。不要重复试同键组合；有新证据时用 src_negative 覆盖。"
	}
	// 资产级 token 预算：只给预算开启的任务附上用量（最多 20 个资产，按用量降序），
	// 让 planner 在派意图前就能看到谁快/已经爆预算。
	if t.srcDoc.AssetTokenBudget > 0 {
		usage, err := t.srcStore.AssetTokenUsageByTask(ctx, t.taskID)
		if err == nil {
			sort.SliceStable(usage, func(i, j int) bool { return usage[i].Tokens > usage[j].Tokens })
			if len(usage) > 20 {
				usage = usage[:20]
			}
			out["asset_token_budget"] = t.srcDoc.AssetTokenBudget
			out["asset_token_usage"] = usage
		}
	}
	return out, nil
}

// srcState 让 planner/main 读完整覆盖状态（矩阵 + 缺口 + 终态格）。
func (t *ToolSet) srcState() actool.CoreTool {
	return readTool(
		"src_state",
		"【SRC 模式】读本任务的覆盖状态：范围内资产 × 必做风险面 的矩阵、未清零的缺口(gaps)、"+
			"已点亮的终态格(cells)、以及收尾门结论(finishable)。派意图前先看 gaps_by_area 与 top_gaps。"+
			"终态语义：tested_found(有洞，挂 finding)/tested_clear(测了没洞)/na(不适用)/budget_stop(预算用尽)。"+
			"prior_coverage 是同一资产在其他任务里的最近终态（30 天内，只读）：可复用其结论，避免无理由重复测。",
		obj(map[string]any{
			"asset_id": intp("只看某个资产（可选，配合 detail 用）"),
			"area":     str("只看某个风险面（可选）"),
			"detail":   map[string]any{"type": "boolean", "description": "true = 返回逐格矩阵与全部 cells（默认 false：摘要视图，只给计数与缺口）"},
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !t.srcReady() {
				return actool.Errorf("src_state 仅在 SRC 模式（tasks.mode='src'）的任务内可用"), nil
			}
			var req struct {
				AssetID int64  `json:"asset_id"`
				Area    string `json:"area"`
				Detail  bool   `json:"detail"`
			}
			_ = json.Unmarshal(in, &req)
			snap, err := t.srcSnapshot(ctx)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			// 默认摘要：一次调用把"每个资产 × 每个面 × 每项收尾"全塞回去，大任务能到几百 KB，
			// 而 planner 真正要的是"还差多少、差在哪"。要逐格看时显式 detail=true。
			if !req.Detail {
				snap = summaryView(snap)
			}
			if req.AssetID > 0 || strings.TrimSpace(req.Area) != "" {
				snap = filterSnapshot(snap, req.AssetID, strings.TrimSpace(req.Area))
			}
			return jsonResult(snap)
		})
}

// srcCellKey 是矩阵查找键（assetID|area|item）。
func srcCellKey(assetID int64, area, item string) string {
	return strconv.FormatInt(assetID, 10) + "|" + area + "|" + item
}

// summaryView 把完整快照压成决策视图：只留计数、缺口与每资产进度，不含逐格行。
// 任务级字段（summary/finishable/doc/legend/submissions）原样保留——它们才是"能不能收"的依据。
func summaryView(snap map[string]any) map[string]any {
	out := make(map[string]any, len(snap)+2)
	for k, v := range snap {
		out[k] = v
	}
	delete(out, "cells") // 逐格行只在 detail 视图里给
	if assets, ok := snap["assets"].([]map[string]any); ok {
		brief := make([]map[string]any, 0, len(assets))
		for _, a := range assets {
			total, done := 0, 0
			if cells, ok := a["cells"].([]map[string]any); ok {
				total = len(cells)
				for _, c := range cells {
					if c["state"] != nil && c["state"] != "" {
						done++
					}
				}
			}
			brief = append(brief, map[string]any{
				"asset_id": a["asset_id"], "asset": a["asset"], "asset_type": a["asset_type"],
				"cells_total": total, "cells_done": done,
			})
		}
		out["assets"] = brief
	}
	if rows, ok := snap["checks"].([]map[string]any); ok {
		brief := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			items, _ := row["items"].([]map[string]any)
			done := 0
			missing := make([]string, 0, 4)
			warns := make([]string, 0, 2)
			for _, it := range items {
				if ws, ok := it["verify_warnings"].([]string); ok {
					title, _ := it["title"].(string)
					for _, w := range ws {
						warns = append(warns, title+"："+w)
					}
				}
				if it["state"] != nil && it["state"] != "" {
					done++
					continue
				}
				if key, ok := it["key"].(string); ok {
					missing = append(missing, key)
				}
			}
			rowOut := map[string]any{
				"asset_id": row["asset_id"], "asset": row["asset"],
				"checks_total": len(items), "checks_done": done, "missing": missing,
			}
			if len(warns) > 0 {
				rowOut["verify_warnings"] = warns
			}
			brief = append(brief, rowOut)
		}
		out["checks"] = brief
	}
	if gaps, ok := snap["gaps"].([]srcmode.Gap); ok && len(gaps) > maxSummaryGaps {
		out["gaps"] = gaps[:maxSummaryGaps]
		out["gaps_truncated"] = true
	}
	out["view"] = "summary"
	out["hint"] = "这是摘要；需要逐格矩阵/全部 cells 时传 detail=true，可再用 asset_id/area 收窄"
	return out
}

// maxSummaryGaps 是摘要视图里缺口的上限（完整清单在矩阵页签与 detail 视图里）。
const maxSummaryGaps = 20

// filterSnapshot 按资产/风险面裁剪快照。任务级信息（summary/finishable/doc/legend/
// submissions）必须保留：过滤只是"看局部"，不是"换一个任务"。
func filterSnapshot(snap map[string]any, assetID int64, area string) map[string]any {
	out := make(map[string]any, len(snap))
	for k, v := range snap {
		out[k] = v
	}
	if assets, ok := snap["assets"].([]map[string]any); ok {
		kept := make([]map[string]any, 0, len(assets))
		for _, a := range assets {
			if assetID > 0 {
				if id, ok := a["asset_id"].(int64); !ok || id != assetID {
					continue
				}
			}
			if area != "" {
				if cells, ok := a["cells"].([]map[string]any); ok {
					only := make([]map[string]any, 0, len(cells))
					for _, c := range cells {
						if c["area"] == area {
							only = append(only, c)
						}
					}
					a["cells"] = only
				}
			}
			kept = append(kept, a)
		}
		out["assets"] = kept
	}
	if cells, ok := snap["cells"].([]db.SrcCell); ok {
		kept := make([]db.SrcCell, 0, len(cells))
		for _, c := range cells {
			if assetID > 0 && c.AssetID != assetID {
				continue
			}
			if area != "" && c.Area != area {
				continue
			}
			kept = append(kept, c)
		}
		out["cells"] = kept
	}
	if rows, ok := snap["checks"].([]map[string]any); ok {
		kept := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			if assetID > 0 {
				if id, ok := row["asset_id"].(int64); !ok || id != assetID {
					continue
				}
			}
			kept = append(kept, row)
		}
		out["checks"] = kept
	}
	if leases, ok := snap["leases"].([]db.SrcLease); ok {
		kept := make([]db.SrcLease, 0, len(leases))
		for _, l := range leases {
			if assetID > 0 && l.AssetID != assetID {
				continue
			}
			if area != "" && l.Area != area {
				continue
			}
			kept = append(kept, l)
		}
		out["leases"] = kept
	}
	if gaps, ok := snap["gaps"].([]srcmode.Gap); ok {
		kept := make([]srcmode.Gap, 0, len(gaps))
		for _, g := range gaps {
			if assetID > 0 && g.AssetID != assetID {
				continue
			}
			if area != "" && g.Area != area {
				continue
			}
			kept = append(kept, g)
		}
		out["gaps"] = kept
		out["gaps_total"] = len(kept)
	}
	out["filter"] = map[string]any{"asset_id": assetID, "area": area}
	return out
}

// srcFinishCheck 是收尾门的只读预检：planner 判 met 前先问一句"还差哪些格"。
func (t *ToolSet) srcFinishCheck() actool.CoreTool {
	return readTool(
		"src_finish_check",
		"【SRC 模式】收尾门预检：返回还差多少覆盖缺口、缺哪种（blank/missing_reason/missing_evidence/"+
			"missing_finding/budget_stop）、以及 finishable。finishable=false 时 prove_goal 也不会让任务结束——"+
			"engine 会拒绝 done 并把缺口注入下一轮。",
		obj(map[string]any{}),
		func(ctx context.Context, _ json.RawMessage) (actool.Result, error) {
			if !t.srcReady() {
				return actool.Errorf("src_finish_check 仅在 SRC 模式的任务内可用"), nil
			}
			rep, err := srcmode.CheckFinish(ctx, t.srcStore, t.taskID, t.srcDoc)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(rep)
		})
}

// srcCellArg is one coverage-cell mark. The tool accepts either one top-level
// mark (legacy) or a `cells` array; both share this shape.
type srcCellArg struct {
	AssetID        int64  `json:"asset_id"`
	Area           string `json:"area"`
	Item           string `json:"item"`
	State          string `json:"state"`
	Reason         string `json:"reason"`
	NACode         string `json:"na_code"`
	EvidenceNodeID int64  `json:"evidence_node_id"`
	FindingID      int64  `json:"finding_id"`
}

// srcMarkCell 点亮覆盖格（写工具）。支持单条（兼容旧调用）或 cells 数组批量落账。
// 终态校验在 srcmode.ValidateCell 里，一条都不能省。
func (t *ToolSet) srcMarkCell() actool.CoreTool {
	itemSchema := obj(map[string]any{
		"asset_id":         intp("要标记的资产 id（来自 src_state / list_assets）"),
		"area":             str("风险面：" + srcmode.AreaLegend()),
		"item":             str("细分项，默认 \"*\"（整组）"),
		"state":            str("终态：tested_found | tested_clear | na | budget_stop"),
		"reason":           str("结论说明（na/budget_stop 必填；tested_clear 无证据节点时必填）"),
		"na_code":          str("na 的理由码（只在该面允许的码里选，见 src_state）"),
		"evidence_node_id": intp("tested_clear 的证据节点 id（一条 fact，可选但推荐）"),
		"finding_id":       intp("tested_found 对应的 findings.id（必填）"),
	})
	props := itemSchema["properties"].(map[string]any)
	props["cells"] = map[string]any{
		"type": "array", "items": itemSchema,
		"description": "【批量】一次点亮多格；与顶层字段同形状，优先于顶层单条参数。返回 cells/errors 按数组下标对应。",
	}
	return writeTool(
		"src_mark_cell",
		"【SRC 模式】点亮一个或多个覆盖格（asset_id + area + item）的终态。结束一条意图前必须调用。\n"+
			"可以传顶层单条字段，也可以用 cells 数组一次提交多格（推荐，减少工具往返）。\n"+
			"state 取值与要求：\n"+
			"  tested_found  —— 测出洞：必须带 finding_id（先 report_finding 拿到 id）；\n"+
			"  tested_clear  —— 测了没洞：必须带 evidence_node_id（record_fact 返回的节点 id）；项目显式允许时才可只写 reason；\n"+
			"  na            —— 该面不适用：必须写 reason + na_code（只能在白名单内；platform_exempt 还需 program 的 exempt_areas 声明）；\n"+
			"  budget_stop   —— 预算/时间用尽没测完：必须写 reason。\n"+
			"area 只能是："+srcmode.AreaLegend()+"；item 用 \"*\" 表示整组，或用该面的细分项（如 injection/sql、paths/enum-200）。\n"+
			"同一格尝试次数超过上限后只能用 budget_stop 收口。",
		itemSchema,
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !t.srcReady() {
				return actool.Errorf("src_mark_cell 仅在 SRC 模式的任务内可用"), nil
			}
			var req struct {
				srcCellArg
				Cells []srcCellArg `json:"cells"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			args := req.Cells
			single := len(args) == 0
			if single {
				if req.AssetID <= 0 {
					return actool.Errorf("asset_id 必填（或用 cells 数组批量提交）"), nil
				}
				args = []srcCellArg{req.srcCellArg}
			}
			if len(args) > 100 {
				return actool.Errorf("一次最多提交 100 个覆盖格"), nil
			}
			type cellResult struct {
				Index int         `json:"index"`
				Cell  *db.SrcCell `json:"cell,omitempty"`
				Error string      `json:"error,omitempty"`
			}
			saved := make([]cellResult, 0, len(args))
			okCount := 0
			for i, arg := range args {
				item := strings.TrimSpace(arg.Item)
				if item == "" {
					item = srcmode.AreaAll
				}
				cell := db.SrcCell{
					TaskID: t.taskID, AssetID: arg.AssetID, Area: arg.Area, Item: item,
					State: arg.State, Reason: arg.Reason, NACode: arg.NACode,
					EvidenceNodeID: arg.EvidenceNodeID, FindingID: arg.FindingID,
					UpdatedBy: t.worker,
				}
				if err := srcmode.ValidateCell(ctx, t.srcStore, t.taskID, t.srcDoc, cell); err != nil {
					saved = append(saved, cellResult{Index: i, Error: err.Error()})
					continue
				}
				row, err := t.srcStore.UpsertSrcCell(ctx, &cell)
				if err != nil {
					saved = append(saved, cellResult{Index: i, Error: "写覆盖格失败：" + err.Error()})
					continue
				}
				okCount++
				saved = append(saved, cellResult{Index: i, Cell: row})
			}
			out := map[string]any{"cells": saved, "ok": okCount, "failed": len(saved) - okCount}
			if single && len(saved) == 1 {
				if saved[0].Cell != nil {
					out["cell"] = saved[0].Cell
				}
				if saved[0].Error != "" {
					out["error"] = saved[0].Error
				}
			}
			rep, err := srcmode.CheckFinish(ctx, t.srcStore, t.taskID, t.srcDoc)
			if err == nil {
				out["gaps_total"] = rep.GapsTotal
				out["gaps_by_area"] = rep.GapsByArea
				out["finishable"] = rep.Finishable
			}
			return jsonResult(out)
		})
}

// srcCheckArg is one closing-check mark; the tool accepts one top-level mark or a
// `checks` array.
type srcCheckArg struct {
	AssetID        int64          `json:"asset_id"`
	CheckKey       string         `json:"check_key"`
	State          string         `json:"state"`
	NACode         string         `json:"na_code"`
	Reason         string         `json:"reason"`
	Metrics        map[string]int `json:"metrics"`
	EvidenceNodeID int64          `json:"evidence_node_id"`
}

// srcMarkCheck 点亮收尾闸（写工具）。支持单条或 checks 数组批量落账。
func (t *ToolSet) srcMarkCheck() actool.CoreTool {
	metrics := map[string]any{
		"type":        "object",
		"description": "做完的量（数字）。按 check_key 填对应项，见参数说明与 src_state。",
		"properties": map[string]any{
			"js_inline":         intp("内联脚本数"),
			"js_external":       intp("外链 JS 数"),
			"js_chunks":         intp("异步 chunk 数"),
			"sourcemap":         intp("sourcemap 数"),
			"degraded":          intp("JS 抽取降级(1=是，须在 reason 写原因)"),
			"paths":             intp("枚举路径条数（full 档 ≥200）"),
			"manual_paths":      intp("手工整理的真实路径数（platform 档用）"),
			"non404":            intp("扫出的非 404 响应数"),
			"retested":          intp("已慢速复测条数（≥2）"),
			"methods":           intp("已测 HTTP 方法数（full 档 ≥7）"),
			"state_methods":     intp("状态变更端点已测方法数（platform 档用）"),
			"variants":          intp("做过的参数变体类型数（≥4：换值/补参/去参/重复）"),
			"contrasts":         intp("防护对照实验数（≥1）"),
			"cache_vary":        intp("缓存 Vary/未键控头对照数（cache_headers，≥1）"),
			"age_contrast":      intp("缓存 Age/X-Cache 对照数（cache_headers，≥1）"),
			"local_unreachable": intp("本地不可达(1=是)"),
			"external_exits":    intp("外部出口复测数（≥2）"),
			"dns_resolvers":     intp("公共 DNS 解析器数（≥2）"),
		},
		"additionalProperties": map[string]any{"type": "integer"},
	}
	itemSchema := obj(map[string]any{
		"asset_id":         intp("资产 id（来自 src_state / list_assets）"),
		"check_key":        str("收尾项：" + strings.Join(srcmode.CheckKeys(), " / ")),
		"state":            str("终态：done | na | budget_stop"),
		"na_code":          str("na 的理由码（工具描述与 src_state 里给出可选值）"),
		"reason":           str("结论说明（na/budget_stop 必填；降级也要写原因）"),
		"metrics":          metrics,
		"evidence_node_id": intp("证据节点 id（record_fact 返回；done 必填）"),
	})
	props := itemSchema["properties"].(map[string]any)
	props["checks"] = map[string]any{
		"type": "array", "items": itemSchema,
		"description": "【批量】一次登记多項收尾项；与顶层字段同形状，优先于顶层单条参数。返回 checks/errors 按数组下标对应。",
	}
	return writeTool(
		"src_mark_check",
		"【SRC 模式】登记一项或多项收尾闸（asset_id + check_key）的终态。它与 src_mark_cell 是两层：\n"+
			"  cell  = 这个风险面测过没有；\n"+
			"  check = 测够没有（JS 抽全 / 路径枚举 / 非 404 复测 / 方法矩阵 / 参数变体 / 防护对照 / 判死三条件 / 截图 / 缓存对照）。\n"+
			"收尾门两层都要清零。可以传顶层单条字段，也可以用 checks 数组批量提交（推荐）。\n"+
			"check_key 只能是："+strings.Join(srcmode.CheckKeys(), " / ")+"。\n"+
			"done 必须有达标的数字（metrics）与 evidence_node_id（先 record_fact 记下命令与结果）；\n"+
			"paths/js/methods/non404 优先用 src_probe 跑确定性探针，它返回的 metrics 可直接填这里；\n"+
			"done 提交后系统会用录制流量核对数字：返回的 verify_warnings 非空时说明自报与实测对不上，先解释差异（可能只是录制没走代理），不要无视；\n"+
			"na 必须写 reason + na_code（只在 src_state 给出的该 check 的 na_codes 里选）；\n"+
			"budget_stop 必须写 reason。数字不达标会被拒绝并告诉你还差多少。",
		itemSchema,
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !t.srcReady() {
				return actool.Errorf("src_mark_check 仅在 SRC 模式的任务内可用"), nil
			}
			var req struct {
				srcCheckArg
				Checks []srcCheckArg `json:"checks"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			args := req.Checks
			single := len(args) == 0
			if single {
				if req.AssetID <= 0 || strings.TrimSpace(req.CheckKey) == "" {
					return actool.Errorf("asset_id 与 check_key 必填（或用 checks 数组批量提交）"), nil
				}
				args = []srcCheckArg{req.srcCheckArg}
			}
			if len(args) > 100 {
				return actool.Errorf("一次最多提交 100 个收尾项"), nil
			}
			type checkResult struct {
				Index           int            `json:"index"`
				Check           *db.SrcCheck   `json:"check,omitempty"`
				Error           string         `json:"error,omitempty"`
				ObservedMetrics map[string]int `json:"observed_metrics,omitempty"`
				VerifyWarnings  []string       `json:"verify_warnings,omitempty"`
				AutoScreenshot  bool           `json:"auto_screenshot,omitempty"`
			}
			saved := make([]checkResult, 0, len(args))
			okCount := 0
			for i, arg := range args {
				// 平台要求截图时，done 且未给证据节点 → 宿主自动截一张并登记成证据。
				autoScreenshot := false
				if strings.TrimSpace(arg.CheckKey) == "screenshots" && strings.TrimSpace(arg.State) == db.SrcCheckDone &&
					arg.EvidenceNodeID <= 0 && t.srcDoc.RequireScreenshots && t.srcScreenshotter != nil {
					shot, err := t.srcScreenshotter(ctx, SrcScreenshotRequest{
						TaskID: t.taskID, AssetID: arg.AssetID, Note: "收尾项截图（自动）",
					})
					if err != nil {
						saved = append(saved, checkResult{Index: i, Error: "自动截图失败：" + err.Error() + "；可改用 src_screenshot 手动重试"})
						continue
					}
					arg.EvidenceNodeID = shot.NodeID
					autoScreenshot = true
				}
				chk := db.SrcCheck{
					TaskID: t.taskID, AssetID: arg.AssetID, CheckKey: arg.CheckKey,
					State: arg.State, NACode: arg.NACode, Reason: arg.Reason,
					Metrics: arg.Metrics, EvidenceNodeID: arg.EvidenceNodeID,
					UpdatedBy: t.worker,
				}
				if err := srcmode.ValidateCheck(ctx, t.srcStore, t.taskID, t.srcDoc, chk); err != nil {
					saved = append(saved, checkResult{Index: i, Error: err.Error(), AutoScreenshot: autoScreenshot})
					continue
				}
				row, err := t.srcStore.UpsertSrcCheck(ctx, &chk)
				if err != nil {
					saved = append(saved, checkResult{Index: i, Error: "写收尾项失败：" + err.Error(), AutoScreenshot: autoScreenshot})
					continue
				}
				entry := checkResult{Index: i, Check: row, AutoScreenshot: autoScreenshot}
				if t.srcVerifier != nil && row.State == db.SrcCheckDone {
					warns, obs := t.srcVerifier(ctx, t.taskID, row.AssetID, row.CheckKey, row.Metrics)
					entry.ObservedMetrics = obs
					entry.VerifyWarnings = warns
				}
				okCount++
				saved = append(saved, entry)
			}
			out := map[string]any{"checks": saved, "ok": okCount, "failed": len(saved) - okCount}
			if single && len(saved) == 1 {
				if saved[0].Check != nil {
					out["check"] = saved[0].Check
				}
				if saved[0].Error != "" {
					out["error"] = saved[0].Error
				}
				out["auto_screenshot"] = saved[0].AutoScreenshot
				if len(saved[0].ObservedMetrics) > 0 {
					out["observed_metrics"] = saved[0].ObservedMetrics
				}
				if len(saved[0].VerifyWarnings) > 0 {
					out["verify_warnings"] = saved[0].VerifyWarnings
				}
			}
			rep, err := srcmode.CheckFinish(ctx, t.srcStore, t.taskID, t.srcDoc)
			if err == nil {
				out["gaps_total"] = rep.GapsTotal
				out["gaps_by_area"] = rep.GapsByArea
				out["checks_total"] = rep.ChecksTotal
				out["checks_done"] = rep.ChecksDone
				out["checks_by_key"] = rep.ChecksByKey
				out["finishable"] = rep.Finishable
			}
			return jsonResult(out)
		})
}
