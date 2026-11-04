package server

import (
	"encoding/json"
	"net/http"

	"github.com/lin9527ya/bladepro/db"
)

// P-Q 厂商档案 API：companies.profile_*（不建 vendors 表）。
// 前端 /function/vendors 用；深度与厂商注入块读取同一份数据。
func (s *Server) registerCompanyProfileRoutes(mux routeRegistrar) {
	mux.HandleFunc("GET /api/company-profiles", s.listCompanyProfiles)
	mux.HandleFunc("GET /api/company-profiles/{id}", s.getCompanyProfile)
	mux.HandleFunc("PUT /api/company-profiles/{id}", s.putCompanyProfile)
}

func (s *Server) listCompanyProfiles(w http.ResponseWriter, r *http.Request) {
	list, err := s.m.PG().Companies().ListCompanyProfiles()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"profiles": list, "count": len(list)})
}

func (s *Server) getCompanyProfile(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	if id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	p, err := s.m.PG().Companies().GetCompanyProfile(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if p == nil {
		writeErr(w, 404, "公司不存在")
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) putCompanyProfile(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	if id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	var req db.CompanyProfile
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	req.CompanyID = id
	if err := s.m.PG().Companies().UpsertCompanyProfile(req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	p, err := s.m.PG().Companies().GetCompanyProfile(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, p)
}
