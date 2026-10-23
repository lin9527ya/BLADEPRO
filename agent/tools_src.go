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
	"strings"

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
				cell := db.SrcCell{}
				found := false
				for _, c := range cells {
					if c.AssetID == a.ID && c.Area == area && c.Item == item {
						cell, found = c, true
						break
					}
				}
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
			}
			items = append(items, entry)
		}
		checkRows = append(checkRows, map[string]any{
			"asset_id": a.ID, "asset": a.Label, "asset_type": a.Type, "items": items,
		})
	}
	return map[string]any{
		"mode":         srcmode.ModeSrc,
		"summary":      rep,
		"gaps":         rep.Gaps,
		"gaps_total":   rep.GapsTotal,
		"gaps_by_area": rep.GapsByArea,
		"checks":       checkRows,
		"cells":        cells,
		"assets":       matrix,
		"finishable":   rep.Finishable,
		"doc": map[string]any{
			"allow_budget_stop":     t.srcDoc.AllowBudgetStop,
			"max_attempts_per_cell": t.srcDoc.MaxAttemptsPerCell,
			"max_intents_per_asset": t.srcDoc.MaxIntentsPerAsset,
			"platform":              t.srcDoc.Platform,
			"program":               t.srcDoc.Program,
			"check_tier":            t.srcDoc.Tier(),
			"require_screenshots":   t.srcDoc.RequireScreenshots,
			"checks_total":          rep.ChecksTotal,
			"checks_done":           rep.ChecksDone,
		},
		"legend": srcmode.AreaLegend(),
		// 交付面（P2）：台账随快照一起给，agent 与 UI 看的是同一份"交到哪一步"。
		"submissions": t.srcSubmissionBriefs(ctx),
	}, nil
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
	return map[string]any{
		"gaps_total":            rep.GapsTotal,
		"gaps_by_area":          rep.GapsByArea,
		"top_gaps":              top,
		"cells_total":           rep.CellsTotal,
		"cells_done":            rep.CellsTerminal,
		"cells_measured":        rep.CellsMeasured,
		"assets":                rep.Assets,
		"finishable":            rep.Finishable,
		"blocked_finish":        !rep.Finishable,
		"blocked_reason":        rep.BlockedReason,
		"allow_budget_stop":     t.srcDoc.AllowBudgetStop,
		"max_attempts_per_cell": t.srcDoc.MaxAttemptsPerCell,
		"note":                  "SRC 模式：本轮只派能点亮空格、或把 tested_clear 变 tested_found 的意图；gaps_total>0 时不要 prove_goal。",
	}, nil
}

// srcState 让 planner/main 读完整覆盖状态（矩阵 + 缺口 + 终态格）。
func (t *ToolSet) srcState() actool.CoreTool {
	return readTool(
		"src_state",
		"【SRC 模式】读本任务的覆盖状态：范围内资产 × 必做风险面 的矩阵、未清零的缺口(gaps)、"+
			"已点亮的终态格(cells)、以及收尾门结论(finishable)。派意图前先看 gaps_by_area 与 top_gaps。"+
			"终态语义：tested_found(有洞，挂 finding)/tested_clear(测了没洞)/na(不适用)/budget_stop(预算用尽)。",
		obj(map[string]any{
			"asset_id": intp("只看某个资产的格（可选）"),
			"area":     str("只看某个风险面（可选）"),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !t.srcReady() {
				return actool.Errorf("src_state 仅在 SRC 模式（tasks.mode='src'）的任务内可用"), nil
			}
			var filter struct {
				AssetID int64  `json:"asset_id"`
				Area    string `json:"area"`
			}
			_ = json.Unmarshal(in, &filter)
			snap, err := t.srcSnapshot(ctx)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if filter.AssetID <= 0 && filter.Area == "" {
				return jsonResult(snap)
			}
			out := map[string]any{"mode": srcmode.ModeSrc}
			if assets, ok := snap["assets"].([]map[string]any); ok {
				kept := make([]map[string]any, 0, len(assets))
				for _, a := range assets {
					if filter.AssetID > 0 && a["asset_id"] != filter.AssetID {
						continue
					}
					if filter.Area != "" {
						if cells, ok := a["cells"].([]map[string]any); ok {
							only := make([]map[string]any, 0, len(cells))
							for _, c := range cells {
								if c["area"] == filter.Area {
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
			gaps := make([]srcmode.Gap, 0, 16)
			if all, ok := snap["gaps"].([]srcmode.Gap); ok {
				for _, g := range all {
					if filter.AssetID > 0 && g.AssetID != filter.AssetID {
						continue
					}
					if filter.Area != "" && g.Area != filter.Area {
						continue
					}
					gaps = append(gaps, g)
				}
			}
			out["gaps"] = gaps
			out["gaps_total"] = len(gaps)
			out["legend"] = snap["legend"]
			return jsonResult(out)
		})
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

// srcMarkCell 点亮一格（写工具）。终态校验在 srcmode.ValidateCell 里，一条都不能省：
// tested_found 必须挂 finding、tested_clear 必须有证据、na/budget_stop 必须写原因。
func (t *ToolSet) srcMarkCell() actool.CoreTool {
	return writeTool(
		"src_mark_cell",
		"【SRC 模式】点亮一个覆盖格（asset_id + area + item）的终态。结束一条意图前必须调用。\n"+
			"state 取值与要求：\n"+
			"  tested_found  —— 测出洞：必须带 finding_id（先 report_finding 拿到 id）；\n"+
			"  tested_clear  —— 测了没洞：带 evidence_node_id（record_fact 返回的节点 id）或写清 reason；\n"+
			"  na            —— 该面不适用：必须写 reason + na_code（可选值见 src_state 的 na_codes）；\n"+
			"  budget_stop   —— 预算/时间用尽没测完：必须写 reason。\n"+
			"area 只能是："+srcmode.AreaLegend()+"；item 用 \"*\" 表示整组，或用该面的细分项（如 injection/sql、paths/enum-200）。\n"+
			"同一格尝试次数超过上限后只能用 budget_stop 收口。",
		obj(map[string]any{
			"asset_id":         intp("要标记的资产 id（来自 src_state / list_assets）"),
			"area":             str("风险面：" + srcmode.AreaLegend()),
			"item":             str("细分项，默认 \"*\"（整组）"),
			"state":            str("终态：tested_found | tested_clear | na | budget_stop"),
			"reason":           str("结论说明（na/budget_stop 必填；tested_clear 无证据节点时必填）"),
			"na_code":          str("na 的理由码（只在该面允许的码里选，见 src_state）"),
			"evidence_node_id": intp("tested_clear 的证据节点 id（一条 fact，可选但推荐）"),
			"finding_id":       intp("tested_found 对应的 findings.id（必填）"),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !t.srcReady() {
				return actool.Errorf("src_mark_cell 仅在 SRC 模式的任务内可用"), nil
			}
			var req struct {
				AssetID        int64  `json:"asset_id"`
				Area           string `json:"area"`
				Item           string `json:"item"`
				State          string `json:"state"`
				Reason         string `json:"reason"`
				NACode         string `json:"na_code"`
				EvidenceNodeID int64  `json:"evidence_node_id"`
				FindingID      int64  `json:"finding_id"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			item := req.Item
			if item == "" {
				item = srcmode.AreaAll
			}
			cell := db.SrcCell{
				TaskID: t.taskID, AssetID: req.AssetID, Area: req.Area, Item: item,
				State: req.State, Reason: req.Reason, NACode: req.NACode,
				EvidenceNodeID: req.EvidenceNodeID, FindingID: req.FindingID,
				UpdatedBy: t.worker,
			}
			if err := srcmode.ValidateCell(ctx, t.srcStore, t.taskID, t.srcDoc, cell); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			saved, err := t.srcStore.UpsertSrcCell(ctx, &cell)
			if err != nil {
				return actool.Errorf("写覆盖格失败：" + err.Error()), nil
			}
			// 只刷一格就能算完的统计走轻量查询；顺带把剩余缺口的摘要回给 worker/planner。
			rep, err := srcmode.CheckFinish(ctx, t.srcStore, t.taskID, t.srcDoc)
			if err != nil {
				return jsonResult(map[string]any{"cell": saved})
			}
			return jsonResult(map[string]any{
				"cell":         saved,
				"gaps_total":   rep.GapsTotal,
				"gaps_by_area": rep.GapsByArea,
				"finishable":   rep.Finishable,
			})
		})
}

// srcMarkCheck 点亮一项收尾闸（写工具）。收尾闸回答"测够没有"：
// JS 抽全、路径枚举、非 404 复测、方法矩阵、参数变体、防护对照、判死三条件、截图。
// 与 src_mark_cell 一样，终态校验在 srcmode.ValidateCheck：done 必须有达标的数字与
// 证据节点，na 必须给理由码，budget_stop 必须写原因。
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
			"local_unreachable": intp("本地不可达(1=是)"),
			"external_exits":    intp("外部出口复测数（≥2）"),
			"dns_resolvers":     intp("公共 DNS 解析器数（≥2）"),
		},
		"additionalProperties": map[string]any{"type": "integer"},
	}
	return writeTool(
		"src_mark_check",
		"【SRC 模式】登记一项收尾闸（asset_id + check_key）的终态。它与 src_mark_cell 是两层：\n"+
			"  cell  = 这个风险面测过没有；\n"+
			"  check = 测够没有（JS 抽全 / 路径枚举 / 非 404 复测 / 方法矩阵 / 参数变体 / 防护对照 / 判死三条件 / 截图）。\n"+
			"收尾门两层都要清零。check_key 只能是："+strings.Join(srcmode.CheckKeys(), " / ")+"。\n"+
			"done 必须有达标的数字（metrics）与 evidence_node_id（先 record_fact 记下命令与结果）；\n"+
			"na 必须写 reason + na_code（只在 src_state 给出的该 check 的 na_codes 里选）；\n"+
			"budget_stop 必须写 reason。数字不达标会被拒绝并告诉你还差多少。",
		obj(map[string]any{
			"asset_id":         intp("资产 id（来自 src_state / list_assets）"),
			"check_key":        str("收尾项：" + strings.Join(srcmode.CheckKeys(), " / ")),
			"state":            str("终态：done | na | budget_stop"),
			"na_code":          str("na 的理由码（工具描述与 src_state 里给出可选值）"),
			"reason":           str("结论说明（na/budget_stop 必填；降级也要写原因）"),
			"metrics":          metrics,
			"evidence_node_id": intp("证据节点 id（record_fact 返回；done 必填）"),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !t.srcReady() {
				return actool.Errorf("src_mark_check 仅在 SRC 模式的任务内可用"), nil
			}
			var req struct {
				AssetID        int64          `json:"asset_id"`
				CheckKey       string         `json:"check_key"`
				State          string         `json:"state"`
				NACode         string         `json:"na_code"`
				Reason         string         `json:"reason"`
				Metrics        map[string]int `json:"metrics"`
				EvidenceNodeID int64          `json:"evidence_node_id"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			chk := db.SrcCheck{
				TaskID: t.taskID, AssetID: req.AssetID, CheckKey: req.CheckKey,
				State: req.State, NACode: req.NACode, Reason: req.Reason,
				Metrics: req.Metrics, EvidenceNodeID: req.EvidenceNodeID,
				UpdatedBy: t.worker,
			}
			if err := srcmode.ValidateCheck(ctx, t.srcStore, t.taskID, t.srcDoc, chk); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			saved, err := t.srcStore.UpsertSrcCheck(ctx, &chk)
			if err != nil {
				return actool.Errorf("写收尾项失败：" + err.Error()), nil
			}
			rep, err := srcmode.CheckFinish(ctx, t.srcStore, t.taskID, t.srcDoc)
			if err != nil {
				return jsonResult(map[string]any{"check": saved})
			}
			return jsonResult(map[string]any{
				"check":         saved,
				"gaps_total":    rep.GapsTotal,
				"gaps_by_area":  rep.GapsByArea,
				"checks_total":  rep.ChecksTotal,
				"checks_done":   rep.ChecksDone,
				"checks_by_key": rep.ChecksByKey,
				"finishable":    rep.Finishable,
			})
		})
}
