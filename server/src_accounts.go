package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lin9527ya/bladepro/db"
)

// P-Q 测试账号池 API（移植自 quiver test_accounts）+ 探活。
// 凭据只写不读：列表给掩码；agent 通过 get_account_credential 取明文（走拦截层审计）。
func (s *Server) registerAccountRoutes(mux routeRegistrar) {
	mux.HandleFunc("GET /api/accounts", s.listAccounts)
	mux.HandleFunc("POST /api/accounts", s.createAccount)
	mux.HandleFunc("PATCH /api/accounts/{id}", s.updateAccount)
	mux.HandleFunc("DELETE /api/accounts/{id}", s.deleteAccount)
	mux.HandleFunc("POST /api/accounts/{id}/credential", s.rotateAccountCredential)
	mux.HandleFunc("POST /api/accounts/{id}/check", s.checkAccount)
}

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	companyID := parseIntDefault(r.URL.Query().Get("company_id"))
	list, err := s.knowledgeStore().ListTestAccounts(companyID, 200)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"accounts": list, "count": len(list)})
}

func (s *Server) createAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CompanyID  int64  `json:"company_id"`
		Label      string `json:"label"`
		RoleTier   string `json:"role_tier"`
		CredType   string `json:"cred_type"`
		Credential string `json:"credential"`
		OwnerPhone string `json:"owner_phone"`
		ExpiresAt  string `json:"expires_at"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	a := db.TestAccount{
		CompanyID: req.CompanyID, Label: req.Label, RoleTier: req.RoleTier,
		CredType: req.CredType, OwnerPhone: req.OwnerPhone,
	}
	if t := strings.TrimSpace(req.ExpiresAt); t != "" {
		if ts, err := time.Parse(time.RFC3339, t); err == nil {
			a.ExpiresAt = &ts
		}
	}
	out, err := s.knowledgeStore().CreateTestAccount(a, req.Credential)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	if id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	var req struct {
		Label      string `json:"label"`
		RoleTier   string `json:"role_tier"`
		Status     string `json:"status"`
		OwnerPhone string `json:"owner_phone"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	out, err := s.knowledgeStore().UpdateTestAccount(id, req.Label, req.RoleTier, req.Status, req.OwnerPhone, nil)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if out == nil {
		writeErr(w, 404, "账号不存在")
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	if id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	if err := s.knowledgeStore().DeleteTestAccount(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) rotateAccountCredential(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	if id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	var req struct {
		Credential string `json:"credential"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	if err := s.knowledgeStore().SetTestAccountCredential(id, req.Credential); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// checkAccount 探活：GET 公司档案里的 check_url，按凭据类型注入请求头。
// 判定：2xx → 有效；401/403 → 失效；3xx 到登录页 → 失效；网络错误 → 失效（记 note）。
// 10 分钟内重复点击直接返回上次结果。
func (s *Server) checkAccount(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	if id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	if s.knowledgeStore().AccountCheckThrottled(id) {
		writeJSON(w, 200, map[string]any{"ok": true, "throttled": true, "note": "10 分钟内已检查过，直接返回上次结果"})
		return
	}
	out, err := s.probeAccount(r.Context(), id)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) probeAccount(ctx context.Context, id int64) (map[string]any, error) {
	acct, cred, err := s.knowledgeStore().GetTestAccountCredential(id)
	if err != nil {
		return nil, err
	}
	if acct == nil {
		return nil, fmt.Errorf("账号不存在")
	}
	profile, err := s.m.PG().Companies().GetCompanyProfile(acct.CompanyID)
	if err != nil {
		return nil, err
	}
	target := ""
	if profile != nil {
		target = strings.TrimSpace(profile.CheckURL)
	}
	if target == "" {
		return nil, fmt.Errorf("公司未配置 check_url（公司档案 → 账号探活地址）")
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("check_url 不是有效的 http(s) 地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	switch acct.CredType {
	case "cookie":
		req.Header.Set("Cookie", cred)
	case "token":
		req.Header.Set("Authorization", "Bearer "+cred)
	case "password":
		// 密码类型不做表单登录（凭据形态未知）；只测目标可达性，note 说明。
	}
	req.Header.Set("User-Agent", "bladepro-account-check/1")
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	note := ""
	if err != nil {
		note = "请求失败（未回包）：" + err.Error()
		_ = s.knowledgeStore().RecordAccountCheck(id, false, note)
		return map[string]any{"ok": false, "note": note}, nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		ok = false
		note = fmt.Sprintf("状态 %d：凭据失效或权限不足", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		low := strings.ToLower(loc)
		if strings.Contains(low, "login") || strings.Contains(low, "signin") || strings.Contains(low, "sso") {
			ok = false
			note = "被重定向到登录页，凭据失效"
		}
	}
	if acct.CredType == "password" {
		note = strings.TrimSpace(note + " password 类型仅探可达性，不做表单登录")
	}
	if note == "" && ok {
		note = fmt.Sprintf("状态 %d：有效", resp.StatusCode)
	}
	_ = s.knowledgeStore().RecordAccountCheck(id, ok, note)
	return map[string]any{"ok": ok, "status": resp.StatusCode, "note": note}, nil
}
