package server

// SRC 交付面（P2）：把一条发现准备成可提交件、提交台账的读写、重复的定案。
//
// 为什么准备逻辑在 server 而不是 agent 工具里：它要读绑定流量的请求/响应原文、
// 任务范围（host 是否在授权内）、以及台账与元数据。agent 侧的 src_prepare_submission
// 只是这个实现的入口（宿主注入，见 agent.SetSrcPreparer）——判定只有一份，不会出现
// "模型以为能交、台账以为不能交"。

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/report"
	"github.com/lin9527ya/bladepro/srcmode"
)

// srcPlatformInfo 返回任务绑定的平台项目与映射后的等级：level 为空表示没有
// severity_map 命中；programID=0 表示任务没绑项目。提交块与台账共用这一份口径，
// 避免"报告写高危、台账记 critical"。
func (s *Server) srcPlatformInfo(ctx context.Context, taskID int64, severity string) (level string, programID int64) {
	if taskID <= 0 {
		return "", 0
	}
	row, err := s.m.PG().Src().SrcProgramForTask(ctx, taskID)
	if err != nil || row == nil {
		return "", 0
	}
	p, err := srcProgramFromRow(row)
	if err != nil || p == nil {
		return "", 0
	}
	return p.PlatformLevel(severity), p.ID
}

func (s *Server) registerSrcDelivery(mux routeRegistrar) {
	mux.HandleFunc("POST /api/findings/{id}/src-submission", s.postFindingSrcSubmission)
	mux.HandleFunc("PATCH /api/findings/{id}/src-dedupe", s.patchFindingSrcDedupe)
	mux.HandleFunc("GET /api/src/submissions", s.listSrcSubmissions)
	mux.HandleFunc("PATCH /api/src/submissions/{id}", s.patchSrcSubmission)
}

// prepareSrcSubmission 是交付准备的唯一实现：
// 正文校验 → 证据合规 → 指纹 → 跨任务查重 → 落台账与元数据 → 渲染八段正文。
func (s *Server) prepareSrcSubmission(ctx context.Context, req agent.SrcPrepareRequest) (*agent.SrcPrepareResult, error) {
	if req.TaskID <= 0 || req.FindingID <= 0 {
		return nil, fmt.Errorf("需要 task_id 与 finding_id")
	}
	f, err := s.m.PG().GetFinding(req.FindingID)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, fmt.Errorf("发现 #%d 不存在", req.FindingID)
	}
	if f.TaskID == nil || *f.TaskID != req.TaskID {
		return nil, fmt.Errorf("发现 #%d 不属于任务 #%d，不能在本任务的台账里准备交付", req.FindingID, req.TaskID)
	}
	store := s.m.PG().Src()
	traffic, err := store.SrcFindingTraffic(ctx, f.ID)
	if err != nil {
		traffic = nil
	}

	steps := make([]srcmode.EvidenceStep, 0, 4)
	refs := make([]report.EndpointRef, 0, 4)
	if traffic != nil {
		for _, b := range traffic.Bindings {
			sn := b.Snapshot
			host := hostOfURL(sn.URL)
			inScope, decidable, err := store.SrcHostInScope(ctx, req.TaskID, host)
			if err != nil {
				return nil, err
			}
			steps = append(steps, srcmode.EvidenceStep{
				BindingID: b.ID, Role: b.Role, Method: sn.Method, URL: sn.URL,
				ReqHead: sn.ReqHead, RespHead: sn.RespHead,
				ReqLen: sn.ReqLen, RespLen: sn.RespLen, Status: sn.Status,
				ReqHash: sn.ReqHash, InScope: inScope, ScopeDecidable: decidable,
			})
			refs = append(refs, report.EndpointRef{Method: sn.Method, URL: sn.URL})
		}
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = report.FindingTitle(f)
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		// 回落顺序与导出面一致：详细报告优先，其次摘要。
		description = strings.TrimSpace(f.Report)
		if description == "" {
			description = strings.TrimSpace(f.Summary)
		}
	}
	impact := strings.TrimSpace(req.Impact)
	fix := strings.TrimSpace(req.Fix)

	// 八段正文先渲染：后面的报文级校验与提交块都要看成品。
	// 接口清单/复现整包仍由证据生成（与导出面同一份 buildSrcInput），
	// 描述/危害/修复用本次写清的三段覆盖。
	in, warnings := s.buildSrcInput(ctx, f)
	if len(warnings) > 0 {
		log.Printf("[src] 发现 #%d 交付告警 %d 条：%s", f.ID, len(warnings), strings.Join(warnings, "；"))
	}
	in.Title = title
	in.Description = description
	in.Impact = impact
	in.Fix = fix
	doc := s.srcDocForTask(req.TaskID)
	if doc.StrictLayout {
		// 严格版式：备注不进退稿风险最高的正文（发现 id / 证据版本由台账承担），
		// 但**交付告警不能丢**——"缺 proof 绑定 / 包被截断"正是审核打回的原因。
		in.Notes = warningNotes(in.Notes)
	}
	// 提交块里的危害等级优先用平台映射后的等级（严重/高危 vs critical/high），
	// 没有映射时回落到内部等级的交付用语。
	platformLevel, programID := s.srcPlatformInfo(ctx, req.TaskID, f.Severity)
	submitLabel := report.SrcSeverityLabel(f.Severity)
	if platformLevel != "" {
		submitLabel = platformLevel
	}
	in.SubmitBlock = srcmode.SubmitInfo(doc, submitLabel)
	markdown := report.SrcMarkdown(in)

	var issues []string
	for _, is := range srcmode.CheckCompliance(f.Severity, steps) {
		issues = append(issues, "证据不合规："+is.Detail)
	}
	issues = append(issues, srcmode.ValidateReportText(title, description, impact, fix)...)
	// P1：medium+ 必须有独立复验 reproduced。没有就在这一刻自动发起（干净会话、
	// 只按原始证据最小重放）；首次准备不会是 ready，等复验结论回来后再准备一次。
	if srcmode.IsMediumPlus(f.Severity) && doc.RequireRetest {
		if msg := s.ensureSrcRetest(ctx, f); msg != "" {
			issues = append(issues, "独立复验："+msg)
		}
	}
	// 成品再查一遍报文层：缺 Host、整行省略号、跨步骤重复的唯一标识头、
	// Content-Length 与正文不符。快照层查过的东西在导出后仍可能被改坏。
	for _, p := range srcmode.CheckReportMarkdown(markdown) {
		issues = append(issues, "报文格式："+p)
	}

	fingerprint := report.Fingerprint(f.VulnClass, refs)
	dupes, err := store.SrcDuplicatesByFingerprint(ctx, fingerprint, f.ID, 10)
	if err != nil {
		return nil, err
	}

	complianceJSON, _ := json.Marshal(map[string]any{
		"ok":     len(issues) == 0,
		"issues": issues,
		"steps":  len(steps),
	})
	status := db.SrcSubDraft
	ready := len(issues) == 0
	if ready {
		status = db.SrcSubReady
	}

	// P3：programID/platformLevel 已在上方算好（提交块与台账共用同一口径）。

	sub, meta, err := store.PrepareSrcSubmission(ctx, db.SrcSubmissionInput{
		TaskID: req.TaskID, FindingID: f.ID, Platform: req.Platform,
		Title: title, Severity: f.Severity, Fingerprint: fingerprint,
		Description: description, Impact: impact, Fix: fix, ReportMD: markdown,
		Status: status, ReportReady: ready, Compliance: complianceJSON,
		ProgramID: programID, PlatformLevel: platformLevel,
	})
	if err != nil {
		return nil, err
	}
	// 查重命中先记一笔 dedupe_of：这会把"疑似重复"顶到收尾门上，必须有人定案
	// （src_dedupe clear 或把台账标 duplicate）才放行。
	dedupeOf := int64(0)
	if meta != nil {
		dedupeOf = meta.DedupeOf
	}
	if dedupeOf == 0 && len(dupes) > 0 {
		if err := store.SetSrcDedupeOf(ctx, f.ID, dupes[0].FindingID); err == nil {
			dedupeOf = dupes[0].FindingID
		}
	}

	res := &agent.SrcPrepareResult{
		SubmissionID: sub.ID, Status: sub.Status, Fingerprint: fingerprint, ReportReady: ready,
		Issues: issues, Duplicates: srcDuplicateRefs(dupes), DedupeOf: dedupeOf,
		Markdown: clipRunes(markdown, 4000),
	}
	if doc := s.srcDocForTask(req.TaskID); doc.IsSrc() {
		if rep, err := srcmode.CheckFinish(ctx, store, req.TaskID, doc); err == nil {
			res.GapsTotal = rep.GapsTotal
			res.Finishable = rep.Finishable
		}
	}
	if dedupeOf > 0 {
		res.Note = fmt.Sprintf("指纹命中已有发现 #%d：已登记 dedupe_of。若确实是同一个洞，把台账状态标为 duplicate；若不是，用 src_dedupe clear 解除。", dedupeOf)
	} else if !ready {
		res.Note = "issues 未清零：台账已落 draft（便于迭代），按清单补齐后重新调用。"
	} else {
		res.Note = "可提交件已齐备（status=ready）。"
	}
	return res, nil
}

// srcDocForTask 读出任务的 SRC 模式快照（非 SRC 任务返回零值——注意不能只 Parse：
// mode_doc 为空的普通渗透任务会被 Parse 退化成"默认 SRC 文档"，那会让收尾门
// 在一个根本没开 SRC 的任务上跑一遍）。
func (s *Server) srcDocForTask(taskID int64) srcmode.ModeDoc {
	t, ok := s.m.Task(strconv.FormatInt(taskID, 10))
	if !ok || t.Mode != srcmode.ModeSrc {
		return srcmode.ModeDoc{}
	}
	return srcmode.Parse(t.ModeDoc)
}

func srcDuplicateRefs(in []db.SrcDuplicate) []agent.SrcDuplicateRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]agent.SrcDuplicateRef, 0, len(in))
	for _, d := range in {
		out = append(out, agent.SrcDuplicateRef{
			TaskID: d.TaskID, TaskName: d.TaskName, FindingID: d.FindingID, Title: d.Title,
			Severity: d.Severity, Status: d.Status, Platform: d.Platform,
			ExternalID: d.ExternalID, SubmissionStatus: d.SubmissionSt,
		})
	}
	return out
}

func hostOfURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}

// postFindingSrcSubmission 是人在页面/curl 也能走的准备入口（与 agent 工具同一实现）。
func (s *Server) postFindingSrcSubmission(w http.ResponseWriter, r *http.Request) {
	f := s.srcFinding(w, r)
	if f == nil {
		return
	}
	if f.TaskID == nil || *f.TaskID <= 0 {
		writeErr(w, 400, "该发现没有所属任务，无法建交付台账")
		return
	}
	var req struct {
		Platform    string `json:"platform"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Impact      string `json:"impact"`
		Fix         string `json:"fix"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	res, err := s.prepareSrcSubmission(r.Context(), agent.SrcPrepareRequest{
		TaskID: *f.TaskID, FindingID: f.ID, Platform: req.Platform,
		Title: req.Title, Description: req.Description, Impact: req.Impact, Fix: req.Fix,
	})
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

// patchFindingSrcDedupe 定案一条发现的重复标记（0 = 解除）。
func (s *Server) patchFindingSrcDedupe(w http.ResponseWriter, r *http.Request) {
	f := s.srcFinding(w, r)
	if f == nil {
		return
	}
	var req struct {
		DedupeOf int64 `json:"dedupe_of"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	if req.DedupeOf == f.ID {
		writeErr(w, 400, "不能把一条发现标成它自己的重复")
		return
	}
	if req.DedupeOf > 0 {
		other, err := s.m.PG().GetFinding(req.DedupeOf)
		if err != nil || other == nil {
			writeErr(w, 400, "dedupe_of 指向的发现不存在")
			return
		}
	}
	if err := s.m.PG().Src().SetSrcDedupeOf(r.Context(), f.ID, req.DedupeOf); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	meta, _ := s.m.PG().Src().GetSrcFindingMeta(r.Context(), f.ID)
	writeJSON(w, 200, map[string]any{"finding_id": f.ID, "meta": meta})
}

// listSrcSubmissions 是全局台账（可按任务/状态/平台过滤）。
func (s *Server) listSrcSubmissions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	taskID, _ := strconv.ParseInt(strings.TrimSpace(q.Get("task_id")), 10, 64)
	limit, _ := strconv.Atoi(strings.TrimSpace(q.Get("limit")))
	rows, err := s.m.PG().Src().ListSrcSubmissions(r.Context(), db.SrcSubmissionFilter{
		TaskID: taskID, Status: q.Get("status"), Platform: q.Get("platform"), Limit: limit,
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// report_md 可能很大，列表接口不回正文（详情页/导出再取）。
	for i := range rows {
		rows[i].ReportMD = ""
	}
	writeJSON(w, 200, map[string]any{"submissions": rows, "total": len(rows)})
}

// patchSrcSubmission 推进台账状态机（提交/受理/重复/驳回/到账/撤回）。
func (s *Server) patchSrcSubmission(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if id <= 0 {
		writeErr(w, 400, "bad submission id")
		return
	}
	var patch db.SrcSubmissionPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	sub, err := s.m.PG().Src().UpdateSrcSubmission(r.Context(), id, patch)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	sub.ReportMD = ""
	writeJSON(w, 200, sub)
}
