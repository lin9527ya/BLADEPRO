package server

// SRC 挖掘模式（tasks.mode='src'）的 HTTP 面：读覆盖矩阵、人工点格、人工放行。
//
// 为什么不把这些塞进 tools：这里的每个接口都是**人在环路**的入口——任务详情页的
// 矩阵是给人看缺口的，force-finish 是人替 engine 做的判定。agent 侧的等价物是
// src_state / src_mark_cell / src_finish_check（见 agent/tools_src.go）。

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

// srcTask resolves the task from the path and returns it with its SRC mode doc.
// mode="" (普通渗透) is reported with ok=false so handlers can answer "not an SRC
// task" instead of inventing an empty matrix.
func (s *Server) srcTask(w http.ResponseWriter, r *http.Request) (*Task, srcmode.ModeDoc, int64, bool) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return nil, srcmode.ModeDoc{}, 0, false
	}
	taskID, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil || taskID <= 0 {
		writeErr(w, 400, "任务 id 无效")
		return nil, srcmode.ModeDoc{}, 0, false
	}
	doc := srcmode.Parse(t.ModeDoc)
	if strings.TrimSpace(t.Mode) != srcmode.ModeSrc {
		return t, doc, taskID, false
	}
	return t, doc, taskID, true
}

// taskSrcSnapshot builds the coverage snapshot for a task (shared by the HTTP
// endpoint and the agent's src_state tool so both tell the same story).
func (s *Server) taskSrcSnapshot(r *http.Request, t *Task, doc srcmode.ModeDoc, taskID int64) (map[string]any, error) {
	tsx := agent.NewToolSet(t.Store, "server")
	tsx.SetTaskID(taskID)
	tsx.SetSrcMode(doc, s.m.PG().Src())
	tsx.SetSrcVerifier(s.verifySrcCheckMetrics)
	return tsx.SrcSnapshot(r.Context())
}

// getTaskSrc returns the SRC coverage view: mode doc, matrix, gaps, cells and the
// finish gate verdict. mode="" tasks get {mode:"", enabled:false}.
func (s *Server) getTaskSrc(w http.ResponseWriter, r *http.Request) {
	t, doc, taskID, ok := s.srcTask(w, r)
	if t == nil {
		return
	}
	if !ok {
		writeJSON(w, 200, map[string]any{
			"mode": t.Mode, "enabled": false,
			"note": "该任务不是 SRC 挖掘模式（tasks.mode≠'src'），没有覆盖矩阵",
		})
		return
	}
	snap, err := s.taskSrcSnapshot(r, t, doc, taskID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	snap["enabled"] = true
	writeJSON(w, 200, snap)
}

// postTaskSrcCell lets a human re-mark (or reset) one coverage cell. The same
// validator as src_mark_cell runs, so人工点格不能绕过终态证据要求。
func (s *Server) postTaskSrcCell(w http.ResponseWriter, r *http.Request) {
	t, doc, taskID, ok := s.srcTask(w, r)
	if t == nil {
		return
	}
	if !ok {
		writeErr(w, 400, "该任务不是 SRC 挖掘模式")
		return
	}
	var req struct {
		AssetID        int64  `json:"asset_id"`
		Area           string `json:"area"`
		Item           string `json:"item"`
		State          string `json:"state"`
		NACode         string `json:"na_code"`
		Reason         string `json:"reason"`
		EvidenceNodeID int64  `json:"evidence_node_id"`
		FindingID      int64  `json:"finding_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	item := strings.TrimSpace(req.Item)
	if item == "" {
		item = srcmode.AreaAll
	}
	store := s.m.PG().Src()
	cell := db.SrcCell{
		TaskID: taskID, AssetID: req.AssetID, Area: strings.TrimSpace(req.Area), Item: item,
		State: strings.TrimSpace(req.State), NACode: strings.TrimSpace(req.NACode),
		Reason:         strings.TrimSpace(req.Reason),
		EvidenceNodeID: req.EvidenceNodeID, FindingID: req.FindingID, UpdatedBy: "human",
	}
	if err := srcmode.ValidateCell(r.Context(), store, taskID, doc, cell); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	saved, err := store.UpsertSrcCell(r.Context(), &cell)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.engine.emitActivity(t, db.Activity{Worker: "system", Kind: "src_gate",
		Summary: "人工标记覆盖格：" + cell.Area + "/" + cell.Item + " → " + cell.State})
	writeJSON(w, 200, map[string]any{"cell": saved})
}

// postTaskSrcForceFinish is the human override for the收尾门: it requires a
// reason, records the audit activity, and only then flips the task to done.
// 没有这一步，"缺口"就会变成 agent 自己的道德约束而不是工程约束。
func (s *Server) postTaskSrcForceFinish(w http.ResponseWriter, r *http.Request) {
	t, doc, taskID, ok := s.srcTask(w, r)
	if t == nil {
		return
	}
	if !ok {
		writeErr(w, 400, "该任务不是 SRC 挖掘模式")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeErr(w, 400, "人工放行必须写 reason（会进审计）")
		return
	}
	store := s.m.PG().Src()
	rep, err := srcmode.CheckFinish(r.Context(), store, taskID, doc)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// 只允许"终态前置检查失败"时放行：没有缺口就不该用这个接口（那是正常收尾）。
	if rep.Finishable {
		writeErr(w, 400, "该任务已满足收尾条件，无需人工放行")
		return
	}
	if err := s.m.SetTaskStatus(t.ID, "done"); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	meta, _ := json.Marshal(map[string]any{
		"gaps_total": rep.GapsTotal, "cells_total": rep.CellsTotal, "reason": reason,
	})
	s.engine.emitActivity(t, db.Activity{Worker: "system", Kind: "src_gate",
		Metadata: meta,
		Summary:  "人工放行收尾：尚有 " + strconv.Itoa(rep.GapsTotal) + " 个缺口未清零，理由：" + reason})
	writeJSON(w, 200, map[string]any{"ok": true, "status": "done", "gaps_total": rep.GapsTotal})
}

// postTaskSrcCheck lets a human mark one收尾闸 (JS 抽全/路径枚举/…). The same
// validator as src_mark_check runs: done 需要达标数字 + 证据节点，na 需要理由码。
// 没有这个接口，人在 UI 上就只能 force-finish 才能过门——那等于把工程约束又变回人工自觉。
func (s *Server) postTaskSrcCheck(w http.ResponseWriter, r *http.Request) {
	t, doc, taskID, ok := s.srcTask(w, r)
	if t == nil {
		return
	}
	if !ok {
		writeErr(w, 400, "该任务不是 SRC 挖掘模式")
		return
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	store := s.m.PG().Src()
	check := db.SrcCheck{
		TaskID: taskID, AssetID: req.AssetID, CheckKey: strings.TrimSpace(req.CheckKey),
		State: strings.TrimSpace(req.State), NACode: strings.TrimSpace(req.NACode),
		Reason: strings.TrimSpace(req.Reason), Metrics: req.Metrics,
		EvidenceNodeID: req.EvidenceNodeID, UpdatedBy: "human",
	}
	if err := srcmode.ValidateCheck(r.Context(), store, taskID, doc, check); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	saved, err := store.UpsertSrcCheck(r.Context(), &check)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.engine.emitActivity(t, db.Activity{Worker: "system", Kind: "src_gate",
		Summary: "人工标记收尾项：" + check.CheckKey + " → " + check.State})
	rep, err := srcmode.CheckFinish(r.Context(), store, taskID, doc)
	if err != nil {
		writeJSON(w, 200, map[string]any{"check": saved})
		return
	}
	writeJSON(w, 200, map[string]any{
		"check": saved, "gaps_total": rep.GapsTotal,
		"checks_total": rep.ChecksTotal, "checks_done": rep.ChecksDone,
		"finishable": rep.Finishable,
	})
}
