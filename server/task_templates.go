package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

const maxTaskTemplateRequestBytes = 512 << 10

// taskTemplateRequest uses pointers so PATCH can distinguish omitted fields from
// explicit empty values. Empty values are still rejected by the DB validator.
// category_id / intercept_rules presence is detected via the raw key set (see
// decodeTaskTemplateRequest) so a PATCH can clear category to null.
type taskTemplateRequest struct {
	Name           *string                `json:"name"`
	Description    *string                `json:"description"`
	Goal           *string                `json:"goal"`
	CategoryID     *int64                 `json:"category_id"`
	InterceptRules []taskInterceptRuleReq `json:"intercept_rules"`
	// SRC 预设：mode='' 普通渗透 / 'src'；src_program_id 绑定平台项目；
	// inherit_coverage 应用模板时默认勾选跨任务覆盖继承。
	Mode            *string `json:"mode"`
	SrcProgramID    *int64  `json:"src_program_id"`
	InheritCoverage *bool   `json:"inherit_coverage"`
	// Batch 是批量扇出规格（与计划任务同形状）；null = 单任务模板。
	Batch *scheduleBatchSpec `json:"batch"`
}

// decodeTaskTemplateRequest decodes the body into req and returns the set of
// top-level keys present in the JSON (for PATCH presence detection).
func decodeTaskTemplateRequest(w http.ResponseWriter, r *http.Request, req *taskTemplateRequest) (map[string]struct{}, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTaskTemplateRequestBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge, "请求正文过大")
		} else {
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return nil, false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	if err := json.Unmarshal(body, req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	present := make(map[string]struct{}, len(raw))
	for k := range raw {
		present[k] = struct{}{}
	}
	return present, true
}

func validateTaskTemplateRequest(req taskTemplateRequest) error {
	checks := []struct {
		name  string
		value *string
		limit int
	}{
		{name: "name", value: req.Name, limit: db.MaxTaskTemplateNameRunes},
		{name: "description", value: req.Description, limit: db.MaxTaskTemplateTextRunes},
		{name: "goal", value: req.Goal, limit: db.MaxTaskTemplateTextRunes},
	}
	for _, check := range checks {
		if check.value == nil {
			continue
		}
		value := strings.TrimSpace(*check.value)
		if check.name == "name" {
			value = strings.Join(strings.Fields(value), " ")
		}
		if utf8.RuneCountInString(value) > check.limit {
			return fmt.Errorf("%s 最多 %d 个字符", check.name, check.limit)
		}
	}
	return nil
}

// validateTaskTemplateSRC checks the SRC preset fields. Program rules are
// validated at save time so a broken project cannot poison every future apply.
func (s *Server) validateTaskTemplateSRC(ctx context.Context, req taskTemplateRequest) error {
	mode := ""
	if req.Mode != nil {
		mode = strings.TrimSpace(*req.Mode)
	}
	if mode != "" && mode != srcmode.ModeSrc {
		return fmt.Errorf("mode 只能是 '' 或 'src'")
	}
	if req.InheritCoverage != nil && *req.InheritCoverage && mode != srcmode.ModeSrc {
		return fmt.Errorf("inherit_coverage 仅适用于 SRC 模板（请同时设 mode=src）")
	}
	if req.Batch != nil && mode != srcmode.ModeSrc {
		return fmt.Errorf("批量扇出仅适用于 SRC 模板（请同时设 mode=src）")
	}
	if req.Batch != nil {
		switch strings.ToLower(strings.TrimSpace(req.Batch.ShardBy)) {
		case "", "host", "root_domain":
		default:
			return fmt.Errorf("批量扇区分片方式只能是 host / root_domain")
		}
		if req.Batch.AssetsPerTask < 0 || req.Batch.MaxShards < 0 || req.Batch.MaxAssets < 0 {
			return fmt.Errorf("批量扇出配额不能为负")
		}
	}
	if req.SrcProgramID != nil && *req.SrcProgramID > 0 {
		if mode != srcmode.ModeSrc {
			return fmt.Errorf("绑定平台项目需要同时设 mode=src")
		}
		row, err := s.m.pg.Src().GetSrcProgram(ctx, *req.SrcProgramID)
		if err != nil {
			return err
		}
		if row == nil {
			return fmt.Errorf("SRC 平台项目不存在")
		}
		program, err := srcProgramFromRow(row)
		if err != nil {
			return fmt.Errorf("该项目的规则配置损坏：%w", err)
		}
		if err := program.Validate(); err != nil {
			return fmt.Errorf("该项目的规则不合法：%w", err)
		}
	}
	return nil
}

// templateBatchJSON turns the request batch spec into the JSONB payload stored on
// the template. nil = clear (SQL NULL).
func templateBatchJSON(b *scheduleBatchSpec) (json.RawMessage, error) {
	if b == nil {
		return nil, nil
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func writeTaskTemplateErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrTaskTemplateInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, db.ErrTaskTemplateNameConflict):
		writeErr(w, http.StatusConflict, "模板名称已存在")
	case errors.Is(err, db.ErrTaskTemplateNotFound):
		writeErr(w, http.StatusNotFound, "task template not found")
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) pgListTaskTemplates(w http.ResponseWriter, _ *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	templates, err := pg.ListTaskTemplates()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
}

func (s *Server) pgCreateTaskTemplate(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req taskTemplateRequest
	if _, ok := decodeTaskTemplateRequest(w, r, &req); !ok {
		return
	}
	if err := validateTaskTemplateRequest(req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rules, err := buildTaskInterceptRules(req.InterceptRules)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "拦截/允许规则无效："+err.Error())
		return
	}
	if err := s.validateTaskTemplateSRC(r.Context(), req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.SrcProgramID != nil && *req.SrcProgramID <= 0 {
		req.SrcProgramID = nil
	}
	batchRaw, err := templateBatchJSON(req.Batch)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	template, err := pg.CreateTaskTemplate(db.TaskTemplateInput{
		Name:            stringValue(req.Name),
		Description:     stringValue(req.Description),
		Goal:            stringValue(req.Goal),
		CategoryID:      req.CategoryID,
		InterceptRules:  rules,
		Mode:            stringValue(req.Mode),
		SrcProgramID:    req.SrcProgramID,
		InheritCoverage: req.InheritCoverage != nil && *req.InheritCoverage,
		Batch:           batchRaw,
	})
	if err != nil {
		writeTaskTemplateErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, template)
}

func (s *Server) pgUpdateTaskTemplate(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad task template id")
		return
	}
	var req taskTemplateRequest
	present, ok := decodeTaskTemplateRequest(w, r, &req)
	if !ok {
		return
	}
	if err := validateTaskTemplateRequest(req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	_, catPresent := present["category_id"]
	_, rulesPresent := present["intercept_rules"]
	_, modePresent := present["mode"]
	_, programPresent := present["src_program_id"]
	_, batchPresent := present["batch"]
	if req.Name == nil && req.Description == nil && req.Goal == nil && !catPresent && !rulesPresent &&
		!modePresent && !programPresent && !batchPresent && req.InheritCoverage == nil {
		writeErr(w, http.StatusBadRequest, "至少需要提供 name、description、goal、category_id、intercept_rules、mode、src_program_id、batch 或 inherit_coverage")
		return
	}
	if err := s.validateTaskTemplateSRC(r.Context(), req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	patch := db.TaskTemplatePatch{Name: req.Name, Description: req.Description, Goal: req.Goal}
	if catPresent {
		patch.SetCategoryID = true
		patch.CategoryID = req.CategoryID
	}
	if rulesPresent {
		rules, err := buildTaskInterceptRules(req.InterceptRules)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "拦截/允许规则无效："+err.Error())
			return
		}
		patch.SetInterceptRules = true
		patch.InterceptRules = rules
	}
	patch.Mode = req.Mode
	if batchPresent {
		batchRaw, err := templateBatchJSON(req.Batch)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		patch.SetBatch = true
		patch.Batch = batchRaw
	}
	if req.SrcProgramID != nil && *req.SrcProgramID <= 0 {
		req.SrcProgramID = nil
	}
	if programPresent {
		patch.SetSrcProgramID = true
		patch.SrcProgramID = req.SrcProgramID
	}
	// 切回普通渗透时顺手清掉 SRC 专属字段，避免模板留下"mode='' 但绑着项目/继承"的矛盾状态。
	if modePresent && strings.TrimSpace(stringValue(req.Mode)) != srcmode.ModeSrc {
		if !programPresent {
			patch.SetSrcProgramID = true
			patch.SrcProgramID = nil
		}
		if req.InheritCoverage == nil {
			off := false
			patch.InheritCoverage = &off
		}
	}
	template, err := pg.PatchTaskTemplate(id, patch)
	if err != nil {
		writeTaskTemplateErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, template)
}

func (s *Server) pgDeleteTaskTemplate(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad task template id")
		return
	}
	deleted, err := pg.DeleteTaskTemplate(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !deleted {
		writeErr(w, http.StatusNotFound, "task template not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
