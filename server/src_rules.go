package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/lin9527ya/bladepro/db"
)

// P-Q 定级规则库 API（移植自 quiver rating_rules）：company_id 为空 = 通用规则。
func (s *Server) registerRatingRuleRoutes(mux routeRegistrar) {
	mux.HandleFunc("GET /api/rating-rules", s.listRatingRules)
	mux.HandleFunc("POST /api/rating-rules", s.upsertRatingRule)
	mux.HandleFunc("DELETE /api/rating-rules/{id}", s.deleteRatingRule)
}

func (s *Server) listRatingRules(w http.ResponseWriter, r *http.Request) {
	companyID := parseIntDefault(r.URL.Query().Get("company_id"))
	rules, err := s.knowledgeStore().ListRatingRules(companyID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"rules": rules, "count": len(rules)})
}

func (s *Server) upsertRatingRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CompanyID int64  `json:"company_id"`
		VulnClass string `json:"vulnclass"`
		Severity  string `json:"severity"`
		Title     string `json:"title"`
		Note      string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	rule, err := s.knowledgeStore().UpsertRatingRule(req.CompanyID, req.VulnClass, req.Severity, req.Title, req.Note)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, rule)
}

func (s *Server) deleteRatingRule(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	if err := s.knowledgeStore().DeleteRatingRule(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// 编译期确认 db.RatingRule 仍在用（避免未来删字段时静默丢接口）。
var _ = db.RatingRule{}
