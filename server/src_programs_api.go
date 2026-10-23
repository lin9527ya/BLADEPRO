package server

// SRC 平台面（P3）：平台项目的 CRUD、看板统计，以及"选一个项目就把规则编译进任务"。
//
// 关键取舍：program 只在**创建任务时**读一次，之后任务只认自己的 mode_doc 与探索策略
// 快照——这样改项目规则不会改变已经在跑的任务（与 P20 的策略快照语义一致）。

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/policy"
	"github.com/lin9527ya/bladepro/srcmode"
)

func (s *Server) registerSrcPrograms(mux routeRegistrar) {
	mux.HandleFunc("GET /api/src/programs", s.listSrcPrograms)
	mux.HandleFunc("POST /api/src/programs", s.createSrcProgram)
	mux.HandleFunc("GET /api/src/programs/{id}", s.getSrcProgram)
	mux.HandleFunc("PATCH /api/src/programs/{id}", s.updateSrcProgram)
	mux.HandleFunc("DELETE /api/src/programs/{id}", s.deleteSrcProgram)
	mux.HandleFunc("GET /api/src/stats", s.getSrcStats)
}

// srcProgramFromRow 把库里的 JSON 行转成 srcmode.Program（JSON 坏掉时返回错误，
// 不静默丢规则——规则丢了等于平台约束失效）。
func srcProgramFromRow(row *db.SrcProgram) (*srcmode.Program, error) {
	if row == nil {
		return nil, nil
	}
	p := &srcmode.Program{
		ID: row.ID, CompanyID: row.CompanyID, Name: row.Name, Platform: row.Platform,
		ScopeNote: row.ScopeNote, RateLimit: row.RateLimit, TimeWindow: row.TimeWindow,
	}
	if len(strings.TrimSpace(string(row.Rules))) > 0 {
		if err := json.Unmarshal(row.Rules, &p.Rules); err != nil {
			return nil, err
		}
	}
	if len(strings.TrimSpace(string(row.SeverityMap))) > 0 {
		if err := json.Unmarshal(row.SeverityMap, &p.SeverityMap); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// programPolicyFloor 把平台规则压进本任务的探索策略：**只许更严**。
// 禁用手法/写操作取并集，额度取更小值，命令级限额追加为自定义禁止条目。
func programPolicyFloor(user *policy.Override, p *srcmode.Program) *policy.Override {
	if p == nil {
		return user
	}
	out := policy.Override{}
	if user != nil {
		out = *user
	}
	union := func(a *[]string, add []string) *[]string {
		if len(add) == 0 {
			return a
		}
		set := map[string]bool{}
		var merged []string
		if a != nil {
			for _, v := range *a {
				if !set[v] {
					set[v] = true
					merged = append(merged, v)
				}
			}
		}
		for _, v := range add {
			v = strings.TrimSpace(v)
			if v == "" || set[v] {
				continue
			}
			set[v] = true
			merged = append(merged, v)
		}
		return &merged
	}
	out.Techniques = union(out.Techniques, p.Rules.ForbiddenTechniques)
	out.ForbiddenWrites = union(out.ForbiddenWrites, p.Rules.ForbiddenWrites)

	minInt := func(cur *int, floor int) *int {
		if floor <= 0 {
			return cur
		}
		if cur == nil || *cur <= 0 || floor < *cur {
			v := floor
			return &v
		}
		return cur
	}
	out.MaxIntentsTotal = minInt(out.MaxIntentsTotal, p.Rules.MaxIntentsTotal)
	out.MaxIntentsPerRound = minInt(out.MaxIntentsPerRound, p.Rules.MaxIntentsPerRound)
	out.MaxOpenIntents = minInt(out.MaxOpenIntents, p.Rules.MaxOpenIntents)

	var rules []policy.CustomRule
	if out.Custom != nil {
		rules = append(rules, (*out.Custom)...)
	}
	for _, r := range p.Rules.CommandRules {
		rules = append(rules, policy.CustomRule{Label: r.Label, Pattern: r.Pattern, Regex: r.Regex, Layer: policy.CustomLayerCommand})
	}
	for _, r := range p.RateLimitRules() {
		rules = append(rules, policy.CustomRule{Label: r.Label, Pattern: r.Pattern, Regex: r.Regex, Layer: policy.CustomLayerCommand})
	}
	for _, r := range p.NoAutomationRules() {
		rules = append(rules, policy.CustomRule{Label: r.Label, Pattern: r.Pattern, Regex: r.Regex, Layer: policy.CustomLayerCommand})
	}
	if len(rules) > 0 {
		out.Custom = &rules
	}
	return &out
}

// srcBuiltinPolicyFloor 把"任何 SRC 任务都成立"的红线并进策略：目前只有会话红线
// （禁登出/注销/吊销）。它不依赖 program——技能里这条是挖洞动作本身的边界。
func srcBuiltinPolicyFloor(user *policy.Override) *policy.Override {
	out := policy.Override{}
	if user != nil {
		out = *user
	}
	var rules []policy.CustomRule
	if out.Custom != nil {
		rules = append(rules, (*out.Custom)...)
	}
	for _, r := range srcmode.BuiltinRules() {
		rules = append(rules, policy.CustomRule{Label: r.Label, Pattern: r.Pattern, Regex: r.Regex, Layer: policy.CustomLayerCommand})
	}
	if len(rules) > 0 {
		out.Custom = &rules
	}
	return &out
}

// validateProgramPolicy 在保存/绑定 program 前先验证它的规则能被策略词表接受。
func validateProgramPolicy(p *srcmode.Program) error {
	eff := policy.Effective{
		Techniques:      p.Rules.ForbiddenTechniques,
		ForbiddenWrites: p.Rules.ForbiddenWrites,
	}
	for _, r := range p.Rules.CommandRules {
		eff.Custom = append(eff.Custom, policy.CustomRule{Label: r.Label, Pattern: r.Pattern, Regex: r.Regex, Layer: policy.CustomLayerCommand})
	}
	for _, r := range p.RateLimitRules() {
		eff.Custom = append(eff.Custom, policy.CustomRule{Label: r.Label, Pattern: r.Pattern, Regex: r.Regex, Layer: policy.CustomLayerCommand})
	}
	for _, r := range p.NoAutomationRules() {
		eff.Custom = append(eff.Custom, policy.CustomRule{Label: r.Label, Pattern: r.Pattern, Regex: r.Regex, Layer: policy.CustomLayerCommand})
	}
	return policy.Validate(eff)
}

func (s *Server) listSrcPrograms(w http.ResponseWriter, r *http.Request) {
	rows, err := s.m.PG().Src().ListSrcPrograms(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]*srcmode.Program, 0, len(rows))
	for i := range rows {
		p, err := srcProgramFromRow(&rows[i])
		if err != nil {
			writeErr(w, 500, "项目 #"+strconv.FormatInt(rows[i].ID, 10)+" 的规则 JSON 坏了："+err.Error())
			return
		}
		out = append(out, p)
	}
	writeJSON(w, 200, map[string]any{"programs": out, "total": len(out)})
}

type srcProgramReq struct {
	CompanyID   *int64               `json:"company_id,omitempty"`
	Name        string               `json:"name"`
	Platform    string               `json:"platform"`
	ScopeNote   string               `json:"scope_note"`
	Rules       srcmode.ProgramRules `json:"rules"`
	RateLimit   int                  `json:"rate_limit"`
	TimeWindow  string               `json:"time_window"`
	SeverityMap map[string]string    `json:"severity_map"`
}

func (req srcProgramReq) toProgram() (*srcmode.Program, []byte, []byte, error) {
	p := &srcmode.Program{
		CompanyID: req.CompanyID, Name: req.Name, Platform: req.Platform,
		ScopeNote: req.ScopeNote, Rules: req.Rules, RateLimit: req.RateLimit,
		TimeWindow: req.TimeWindow, SeverityMap: req.SeverityMap,
	}
	if err := p.Validate(); err != nil {
		return nil, nil, nil, err
	}
	if err := validateProgramPolicy(p); err != nil {
		return nil, nil, nil, err
	}
	rulesJSON, err := json.Marshal(p.Rules)
	if err != nil {
		return nil, nil, nil, err
	}
	sevJSON, err := json.Marshal(p.SeverityMap)
	if err != nil {
		return nil, nil, nil, err
	}
	return p, rulesJSON, sevJSON, nil
}

func (s *Server) createSrcProgram(w http.ResponseWriter, r *http.Request) {
	var req srcProgramReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	p, rulesJSON, sevJSON, err := req.toProgram()
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	row, err := s.m.PG().Src().CreateSrcProgram(r.Context(), db.SrcProgramInput{
		CompanyID: p.CompanyID, Name: p.Name, Platform: p.Platform, ScopeNote: p.ScopeNote,
		Rules: rulesJSON, RateLimit: p.RateLimit, TimeWindow: p.TimeWindow, SeverityMap: sevJSON,
	})
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	out, err := srcProgramFromRow(row)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, out)
}

func (s *Server) getSrcProgram(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	row, err := s.m.PG().Src().GetSrcProgram(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if row == nil {
		writeErr(w, 404, "SRC 平台项目不存在")
		return
	}
	p, err := srcProgramFromRow(row)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) updateSrcProgram(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	var req srcProgramReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	p, rulesJSON, sevJSON, err := req.toProgram()
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	row, err := s.m.PG().Src().UpdateSrcProgram(r.Context(), id, db.SrcProgramInput{
		CompanyID: p.CompanyID, Name: p.Name, Platform: p.Platform, ScopeNote: p.ScopeNote,
		Rules: rulesJSON, RateLimit: p.RateLimit, TimeWindow: p.TimeWindow, SeverityMap: sevJSON,
	})
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if row == nil {
		writeErr(w, 404, "SRC 平台项目不存在")
		return
	}
	out, err := srcProgramFromRow(row)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) deleteSrcProgram(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if id <= 0 {
		writeErr(w, 400, "bad program id")
		return
	}
	if err := s.m.PG().Src().DeleteSrcProgram(r.Context(), id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true, "id": id})
}

// getSrcStats 是 SRC 看板的聚合数字。
func (s *Server) getSrcStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.m.PG().Src().SrcDashboardStats(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, stats)
}
