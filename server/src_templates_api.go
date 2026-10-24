package server

// 用户自定义平台提交模板的 HTTP 面。内置模板由 srcmode 提供（只读），这里返回
// “内置 + 自定义” 的合并列表，并把自定义模板的 CRUD 落到 src_platform_templates。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

var submitFieldSources = map[string]bool{
	"title": true, "severity": true, "cwe": true, "cvss": true, "asset": true,
	"repro_env": true, "attachments": true, "platform": true, "program": true,
	"egress_ip": true, "test_accounts": true,
}

type srcSubmitTemplateDTO struct {
	Key      string              `json:"key"`
	Name     string              `json:"name"`
	Header   string              `json:"header,omitempty"`
	RuleURL  string              `json:"rule_url,omitempty"`
	RuleDate string              `json:"rule_date,omitempty"`
	Fields   []db.SrcSubmitField `json:"fields,omitempty"`
	Custom   bool                `json:"custom"`
}

func (s *Server) listSrcSubmitTemplates(w http.ResponseWriter, r *http.Request) {
	out := make([]srcSubmitTemplateDTO, 0, len(srcmode.PlatformTemplates()))
	for _, t := range srcmode.PlatformTemplates() {
		out = append(out, srcSubmitTemplateDTO{Key: t.Key, Name: t.Name, RuleURL: t.RuleURL, RuleDate: t.RuleDate})
	}
	if s.m.pg != nil {
		custom, err := s.m.PG().Src().ListSrcSubmitTemplates(r.Context())
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		for _, t := range custom {
			out = append(out, srcSubmitTemplateDTO{
				Key: t.Key, Name: t.Name, Header: t.Header, RuleURL: t.RuleURL, RuleDate: t.RuleDate,
				Fields: t.Fields, Custom: true,
			})
		}
	}
	writeJSON(w, 200, map[string]any{"templates": out})
}

type srcSubmitTemplateReq struct {
	Key      string              `json:"key"`
	Name     string              `json:"name"`
	Header   string              `json:"header"`
	RuleURL  string              `json:"rule_url"`
	RuleDate string              `json:"rule_date"`
	Fields   []db.SrcSubmitField `json:"fields"`
}

func (req srcSubmitTemplateReq) normalize() (db.SrcSubmitTemplate, error) {
	key := strings.ToLower(strings.TrimSpace(req.Key))
	if !strings.HasPrefix(key, "custom:") || len(key) <= len("custom:") {
		return db.SrcSubmitTemplate{}, fmt.Errorf("模板 key 必须以 custom: 开头")
	}
	if strings.TrimSpace(req.Name) == "" {
		return db.SrcSubmitTemplate{}, fmt.Errorf("模板名称不能为空")
	}
	if len(req.Fields) == 0 {
		return db.SrcSubmitTemplate{}, fmt.Errorf("至少需要一个字段")
	}
	fields := make([]db.SrcSubmitField, 0, len(req.Fields))
	for i, f := range req.Fields {
		source := strings.ToLower(strings.TrimSpace(f.Source))
		if !submitFieldSources[source] {
			return db.SrcSubmitTemplate{}, fmt.Errorf("第 %d 个字段的 source 无效：%s", i+1, f.Source)
		}
		label := strings.TrimSpace(f.Label)
		if label == "" {
			return db.SrcSubmitTemplate{}, fmt.Errorf("第 %d 个字段缺少 label", i+1)
		}
		fields = append(fields, db.SrcSubmitField{Label: label, Source: source, Required: f.Required})
	}
	return db.SrcSubmitTemplate{
		Key: key, Name: strings.TrimSpace(req.Name), Header: strings.TrimSpace(req.Header),
		RuleURL: strings.TrimSpace(req.RuleURL), RuleDate: strings.TrimSpace(req.RuleDate), Fields: fields,
	}, nil
}

func (s *Server) saveSrcSubmitTemplate(w http.ResponseWriter, r *http.Request) {
	var req srcSubmitTemplateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	tpl, err := req.normalize()
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	row, err := s.m.PG().Src().UpsertSrcSubmitTemplate(r.Context(), tpl)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, row)
}

func (s *Server) deleteSrcSubmitTemplate(w http.ResponseWriter, r *http.Request) {
	key := strings.ToLower(strings.TrimSpace(r.PathValue("key")))
	if !strings.HasPrefix(key, "custom:") {
		writeErr(w, 400, "只能删除 custom: 开头的自定义模板")
		return
	}
	found, err := s.m.PG().Src().DeleteSrcSubmitTemplate(r.Context(), key)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !found {
		writeErr(w, 404, "模板不存在")
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": key})
}
