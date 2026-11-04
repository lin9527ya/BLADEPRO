package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/db"
)

// P-Q 统一知识体系 API（移植自 quiver，AGPL-3.0）：知识条目检索/反馈/状态 + 审批队列。
// 页面：/function/knowledge（知识）与 /function/approvals（审批）。
func (s *Server) registerKnowledgeRoutes(mux routeRegistrar) {
	mux.HandleFunc("GET /api/knowledge", s.listKnowledge)
	mux.HandleFunc("GET /api/knowledge/{id}", s.getKnowledge)
	mux.HandleFunc("POST /api/knowledge/{id}/feedback", s.postKnowledgeFeedback)
	mux.HandleFunc("PATCH /api/knowledge/{id}", s.patchKnowledgeStatus)
	mux.HandleFunc("GET /api/approvals", s.listApprovals)
	mux.HandleFunc("POST /api/knowledge/ingest", s.handleKnowledgeIngest)
	mux.HandleFunc("POST /api/tasks/{id}/retrospect", s.handleTaskRetrospect)
	mux.HandleFunc("POST /api/approvals", s.createApproval)
	mux.HandleFunc("POST /api/approvals/{id}/decide", s.decideApproval)
}

// wireKnowledgeProvider 把统一知识库接进 planner/worker 的 system prompt 注入。
// 开关 knowledge_inject_enabled 默认关（灰度）；打开后 graph_overview 仍会保留旧两段，
// 但注入块是唯一带预算与 ⚠ 复核标记的入口。
func (s *Server) wireKnowledgeProvider() {
	agent.KnowledgeProvider = func(taskID int64, planner bool) string {
		if v, _, _ := s.m.pg.GetSetting("knowledge_inject_enabled"); v != "true" {
			return ""
		}
		budget := 3000
		if v, _, _ := s.m.pg.GetSetting("knowledge_block_budget"); strings.TrimSpace(v) != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 500 && n <= 12000 {
				budget = n
			}
		}
		block, err := s.m.PG().Assets().KnowledgeBlock(taskID, planner, budget)
		if err != nil {
			return ""
		}
		return block
	}
}

func (s *Server) knowledgeStore() *db.AssetStore { return s.m.PG().Assets() }

func (s *Server) listKnowledge(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	kq := db.KnowledgeQuery{
		Scope:     strings.TrimSpace(q.Get("scope")),
		Text:      strings.TrimSpace(q.Get("q")),
		Limit:     limit,
		CompanyID: parseIntDefault(q.Get("company_id")),
		TaskID:    parseIntDefault(q.Get("task_id")),
	}
	if v := strings.TrimSpace(q.Get("types")); v != "" {
		for _, t := range strings.Split(v, ",") {
			if n := db.NormalizeKnowledgeType(t); n != "" {
				kq.Types = append(kq.Types, n)
			}
		}
	}
	if v := strings.TrimSpace(q.Get("status")); v != "" {
		kq.Status = strings.Split(v, ",")
	}
	entries, err := s.knowledgeStore().LookupKnowledge(kq)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"entries": entries, "count": len(entries)})
}

func (s *Server) getKnowledge(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	if id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	e, err := s.knowledgeStore().GetKnowledgeEntry(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if e == nil {
		writeErr(w, 404, "知识条目不存在")
		return
	}
	writeJSON(w, 200, e)
}

func (s *Server) postKnowledgeFeedback(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	if id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	var req struct {
		Outcome string `json:"outcome"`
		Note    string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	if err := s.knowledgeStore().FeedbackKnowledge(id, req.Outcome, req.Note); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) patchKnowledgeStatus(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	if id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	var req struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	if req.Status == db.KnowledgeStatusRetired || req.Status == db.KnowledgeStatusDeprecated {
		if strings.TrimSpace(req.Reason) == "" {
			writeErr(w, 400, "废弃/退休必须给出理由")
			return
		}
	}
	if err := s.knowledgeStore().SetKnowledgeStatus(id, req.Status, req.Reason); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := s.knowledgeStore().ListApprovals(q.Get("status"), limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"approvals": list, "count": len(list)})
}

func (s *Server) createApproval(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind    string          `json:"kind"`
		Payload json.RawMessage `json:"payload"`
		Reason  string          `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	a, err := s.knowledgeStore().CreateApproval(req.Kind, req.Payload, req.Reason)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, a)
}

// decideApproval 审批并执行 side effect：
//   - knowledge / ammo_global → 写入 knowledge_entries；
//   - vendor_profile         → 更新 companies.profile_*；
//   - rating_rule            → upsert rating_rules。
//
// 已被处理过（重复点击/并发）返回 409，不重复执行 side effect。
func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	if id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	var req struct {
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	cur, err := s.knowledgeStore().GetApproval(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if cur == nil {
		writeErr(w, 404, "审批不存在")
		return
	}
	if cur.Status != "pending" {
		writeErr(w, 409, "该审批已处理（"+cur.Status+"）")
		return
	}
	var entryID int64
	if req.Approve {
		entryID, err = s.applyApproval(cur)
		if err != nil {
			writeErr(w, 400, "批准失败："+err.Error())
			return
		}
	}
	decidedBy := strings.TrimSpace(req.Reason)
	if decidedBy == "" {
		decidedBy = "human"
	}
	a, changed, err := s.knowledgeStore().DecideApproval(id, req.Approve, decidedBy)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !changed {
		writeErr(w, 409, "该审批已被其他人处理")
		return
	}
	if entryID > 0 {
		_ = s.knowledgeStore().MarkApprovalEntry(id, entryID)
	}
	writeJSON(w, 200, map[string]any{"approval": a, "entry_id": entryID})
}

// applyApproval 执行已批准提案的落库逻辑，返回写入的知识条目 id（非知识类为 0）。
func (s *Server) applyApproval(a *db.Approval) (int64, error) {
	var p struct {
		EntryType   string `json:"entry_type"`
		Scope       string `json:"scope"`
		CompanyID   int64  `json:"company_id"`
		Key         string `json:"key"`
		Title       string `json:"title"`
		Summary     string `json:"summary"`
		BodyMD      string `json:"body_md"`
		AmmoKind    string `json:"ammo_kind"`
		Confidence  string `json:"confidence"`
		SourceType  string `json:"source_type"`
		SourceRef   string `json:"source_ref"`
		SourceTask  string `json:"source_task_id"`
		EvidenceRef string `json:"evidence_ref"`
		// vendor_profile
		MaxDepth          int      `json:"max_depth"`
		BannedVulnClasses []string `json:"banned_vuln_classes"`
		ReportFormatMD    string   `json:"report_format_md"`
		TestWindow        string   `json:"test_window"`
		RateLimitNote     string   `json:"rate_limit_note"`
		CheckURL          string   `json:"check_url"`
		Domains           []string `json:"domains"`
		// rating_rule
		VulnClass string `json:"vulnclass"`
		Severity  string `json:"severity"`
		Note      string `json:"note"`
	}
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return 0, err
	}
	switch a.Kind {
	case db.ApprovalKindKnowledge, db.ApprovalKindAmmoGlobal:
		entryType := db.NormalizeKnowledgeType(p.EntryType)
		if entryType == "" {
			entryType = db.KnowledgeTypeMemory
		}
		scope := p.Scope
		if a.Kind == db.ApprovalKindAmmoGlobal {
			scope = db.KnowledgeScopeGlobal
			entryType = db.KnowledgeTypeAmmo
		}
		e, err := s.knowledgeStore().InsertKnowledgeEntry(db.KnowledgeEntry{
			Scope: scope, CompanyID: p.CompanyID, EntryType: entryType,
			Key: p.Key, Title: p.Title, Summary: p.Summary, BodyMD: p.BodyMD,
			AmmoKind: p.AmmoKind, Confidence: db.NormalizeConfidence(p.Confidence),
			SourceType: p.SourceType, SourceRef: p.SourceRef, SourceTaskID: p.SourceTask,
			EvidenceRef: p.EvidenceRef,
		})
		if err != nil {
			return 0, err
		}
		return e.ID, nil
	case db.ApprovalKindVendorProfile:
		if p.CompanyID <= 0 {
			return 0, errText("vendor_profile 需要 company_id")
		}
		if err := s.m.PG().Companies().UpsertCompanyProfile(db.CompanyProfile{
			CompanyID: p.CompanyID, MaxDepth: p.MaxDepth, BannedVulnClasses: p.BannedVulnClasses,
			ReportFormatMD: p.ReportFormatMD, TestWindow: p.TestWindow, RateLimitNote: p.RateLimitNote,
			CheckURL: p.CheckURL, Domains: p.Domains,
		}); err != nil {
			return 0, err
		}
		return 0, nil
	case db.ApprovalKindRatingRule:
		if _, err := s.knowledgeStore().UpsertRatingRule(p.CompanyID, p.VulnClass, p.Severity, p.Title, p.Note); err != nil {
			return 0, err
		}
		return 0, nil
	}
	return 0, errText("未知审批类型")
}

type errText string

func (e errText) Error() string { return string(e) }
