package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/policy"
	"github.com/lin9527ya/bladepro/sched"
)

// P22 计划任务：按结构化频率到点自动建任务。
//
// 语义（写进 UI 与 docs/情报与计划任务.md，避免"为什么没跑/为什么跑了两次"的歧义）：
//   - 一份计划任务 = 一份**确定的可重放规格**（task_spec，与创建任务 API 同形状的子集）
//     + 一个结构化频率。到点就按规格原样建一个普通任务，之后的推送/交付全走既有链路。
//   - **不重复触发**靠 CAS：先把 next_run_at 推到下一次（原子），推成功的那次才建任务；
//     多 tick、重启、同实例并发都不会建出两个任务。
//   - **不追补**：进程停机期间错过的次数只跑一次（开机时那一次），不会连建三个任务；
//     要补就人工点「立即运行」。
//   - 「立即运行」是**额外**一次运行，不改排期（下次仍按原计划）。

// scheduleSpec 是到点要建的任务规格：与 createTaskReq 同形状的子集（字段名一致，
// 前端可以直接复用建任务表单的字段名）。刻意不做隐式继承——模板改了不该影响已在跑的计划。
type scheduleSpec struct {
	Name                 string                 `json:"name,omitempty"`
	Description          string                 `json:"description"`
	Goal                 string                 `json:"goal"`
	CategoryID           *int64                 `json:"category_id,omitempty"`
	TemplateID           *int64                 `json:"template_id,omitempty"`
	LLMProfileIDs        []int64                `json:"llm_profile_ids,omitempty"`
	CompanyIDs           []int64                `json:"company_ids,omitempty"`
	TimeoutSeconds       int                    `json:"timeout_seconds,omitempty"`
	TokenBudget          int64                  `json:"token_budget,omitempty"` // 0 = 全局
	Priority             int                    `json:"priority,omitempty"`
	PlanHeartbeatSeconds int                    `json:"plan_heartbeat_seconds,omitempty"`
	CoverageEnabled      *bool                  `json:"coverage_enabled,omitempty"`
	SeedFirstIntent      *bool                  `json:"seed_first_intent,omitempty"`
	InterceptRules       []taskInterceptRuleReq `json:"intercept_rules,omitempty"`
	ExplorationPolicy    *policy.Override       `json:"exploration_policy,omitempty"`
	// SRC：模式 + 平台项目 + 跨任务覆盖继承；与建任务请求同义。
	Mode            string          `json:"mode,omitempty"`
	ModeDoc         json.RawMessage `json:"mode_doc,omitempty"`
	SrcProgramID    *int64          `json:"src_program_id,omitempty"`
	InheritCoverage *bool           `json:"inherit_coverage,omitempty"`
}

// taskScheduleDTO 是给 UI 的计划任务视图：多给一个 frequency_text（人话），
// 省得前端再实现一遍频率渲染。
type taskScheduleDTO struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	Enabled       bool            `json:"enabled"`
	TaskSpec      scheduleSpec    `json:"task_spec"`
	Frequency     sched.Frequency `json:"frequency"`
	FrequencyText string          `json:"frequency_text"`
	NextRunAt     string          `json:"next_run_at,omitempty"`
	LastRunAt     string          `json:"last_run_at,omitempty"`
	LastTaskID    string          `json:"last_task_id,omitempty"`
	LastError     string          `json:"last_error,omitempty"`
	CreatedAt     string          `json:"created_at"`
}

func scheduleDTO(s *db.TaskSchedule) taskScheduleDTO {
	var spec scheduleSpec
	_ = json.Unmarshal(s.TaskSpec, &spec)
	var freq sched.Frequency
	_ = json.Unmarshal(s.Frequency, &freq)
	dto := taskScheduleDTO{
		ID: s.ID, Name: s.Name, Enabled: s.Enabled, TaskSpec: spec, Frequency: freq,
		FrequencyText: freq.Describe(), LastTaskID: s.LastTaskID, LastError: s.LastError,
		CreatedAt: s.CreatedAt.Format(time.RFC3339),
	}
	if s.NextRunAt != nil {
		dto.NextRunAt = s.NextRunAt.Format(time.RFC3339)
	}
	if s.LastRunAt != nil {
		dto.LastRunAt = s.LastRunAt.Format(time.RFC3339)
	}
	return dto
}

// validateScheduleSpec 校验规格（写入侧就拒，不等到触发那天才发现写错）。
// 只做"能不能建成任务"的确定性检查；能力的可用性（LLM 配置是否还在等）留给触发时记录。
func (s *Server) validateScheduleSpec(spec scheduleSpec) error {
	if strings.TrimSpace(spec.Description) == "" {
		return fmt.Errorf("任务描述不能为空")
	}
	if strings.TrimSpace(spec.Goal) == "" {
		return fmt.Errorf("任务目标不能为空")
	}
	if spec.TimeoutSeconds < 0 {
		return fmt.Errorf("超时秒数不能为负")
	}
	if spec.CategoryID != nil {
		// prepareTaskCreate 不校验分类（建任务时才由 DB 拒绝）；计划任务必须在写入时
		// 就报错，否则错误会拖到触发那天。
		cat, err := s.m.pg.GetTaskCategory(*spec.CategoryID)
		if err != nil || cat == nil {
			return fmt.Errorf("任务分类不存在")
		}
	}
	// 走与 HTTP 建任务完全相同的准备管线：项目规则、SRC 快照、策略、LLM 配置、
	// 分类、覆盖继承都在写入计划时就校验，避免到触发那天才发现配置写错。
	if _, perr := s.prepareTaskCreate(context.Background(), scheduleSpecToCreateReq(spec)); perr != nil {
		return fmt.Errorf("%s", perr.msg)
	}
	return nil
}

// createTaskFromSpec 按规格建一个任务并启动（与 HTTP 建任务同一条路径）。
func (s *Server) createTaskFromSpec(spec scheduleSpec) (*Task, error) {
	ctx := context.Background()
	prep, perr := s.prepareTaskCreate(ctx, scheduleSpecToCreateReq(spec))
	if perr != nil {
		return nil, fmt.Errorf("%s", perr.msg)
	}
	t, perr := s.persistPreparedTask(ctx, prep, nil)
	if perr != nil {
		return nil, fmt.Errorf("%s", perr.msg)
	}
	s.launchTask(t, spec.Description+" "+spec.Goal, spec.SeedFirstIntent != nil && *spec.SeedFirstIntent)
	return t, nil
}

// scheduleSpecToCreateReq maps a schedule's replayable spec onto the task-creation
// request. Keeping one mapping means schedules cannot silently drift from the HTTP
// creation path.
func scheduleSpecToCreateReq(spec scheduleSpec) createTaskReq {
	return createTaskReq{
		Name: spec.Name, Description: spec.Description, Goal: spec.Goal,
		CategoryID: spec.CategoryID, LLMProfileIDs: spec.LLMProfileIDs,
		CompanyIDs: spec.CompanyIDs, TimeoutSeconds: spec.TimeoutSeconds,
		TokenBudget: spec.TokenBudget, Priority: spec.Priority,
		PlanHeartbeatSeconds: spec.PlanHeartbeatSeconds,
		CoverageEnabled:      spec.CoverageEnabled, SeedFirstIntent: spec.SeedFirstIntent,
		InterceptRules: spec.InterceptRules, ExplorationPolicy: spec.ExplorationPolicy,
		Mode: spec.Mode, ModeDoc: spec.ModeDoc, SrcProgramID: spec.SrcProgramID,
		InheritCoverage: spec.InheritCoverage,
	}
}

// ---- HTTP ----

func (s *Server) listTaskSchedules(w http.ResponseWriter, r *http.Request) {
	rows, err := s.m.pg.ListTaskSchedules()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]taskScheduleDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, scheduleDTO(row))
	}
	writeJSON(w, 200, map[string]any{"schedules": out})
}

type taskScheduleReq struct {
	Name      string          `json:"name"`
	Enabled   *bool           `json:"enabled"`
	TaskSpec  scheduleSpec    `json:"task_spec"`
	Frequency sched.Frequency `json:"frequency"`
}

func (s *Server) createTaskSchedule(w http.ResponseWriter, r *http.Request) {
	var req taskScheduleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = strings.TrimSpace(req.TaskSpec.Name)
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = clipRunesLocal(req.TaskSpec.Description, 40)
	}
	if err := req.Frequency.Validate(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.validateScheduleSpec(req.TaskSpec); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	specRaw, _ := json.Marshal(req.TaskSpec)
	freqRaw, _ := json.Marshal(req.Frequency)
	enabled := req.Enabled == nil || *req.Enabled
	var next *time.Time
	if enabled {
		n, err := req.Frequency.Next(time.Now())
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		next = &n
	}
	row, err := s.m.pg.CreateTaskSchedule(req.Name, enabled, specRaw, freqRaw, next)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, scheduleDTO(row))
}

func (s *Server) patchTaskSchedule(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	if id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	cur, err := s.m.pg.GetTaskSchedule(id)
	if err != nil || cur == nil {
		writeErr(w, 404, "计划任务不存在")
		return
	}
	var req struct {
		Name      *string          `json:"name"`
		Enabled   *bool            `json:"enabled"`
		TaskSpec  *scheduleSpec    `json:"task_spec"`
		Frequency *sched.Frequency `json:"frequency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	var specRaw, freqRaw json.RawMessage
	if req.TaskSpec != nil {
		if err := s.validateScheduleSpec(*req.TaskSpec); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		specRaw, _ = json.Marshal(*req.TaskSpec)
	}
	freq := sched.Frequency{}
	_ = json.Unmarshal(cur.Frequency, &freq)
	if req.Frequency != nil {
		if err := req.Frequency.Validate(); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		freq = *req.Frequency
		freqRaw, _ = json.Marshal(freq)
	}
	enabled := cur.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	// 排期规则：启用时按新频率从"现在"重排（改频率/重新启用都立即以新节奏生效）；
	// 禁用时清空排期。
	var next *time.Time
	clearNext := !enabled
	if enabled {
		n, err := freq.Next(time.Now())
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		next = &n
	}
	row, err := s.m.pg.UpdateTaskSchedule(id, req.Name, req.Enabled, specRaw, freqRaw, next, clearNext)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if row == nil {
		writeErr(w, 404, "计划任务不存在")
		return
	}
	if req.Enabled != nil && *req.Enabled && !cur.Enabled {
		s.taskScheduleAudit(fmt.Sprintf("计划任务 #%d「%s」已启用（下次 %s）", id, row.Name, freq.Describe()))
	}
	writeJSON(w, 200, scheduleDTO(row))
}

func (s *Server) deleteTaskSchedule(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	ok, err := s.m.pg.DeleteTaskSchedule(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !ok {
		writeErr(w, 404, "计划任务不存在")
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true})
}

// runTaskScheduleNow 立即按规格建一个任务（**额外**一次运行，不改排期）。
func (s *Server) runTaskScheduleNow(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	row, err := s.m.pg.GetTaskSchedule(id)
	if err != nil || row == nil {
		writeErr(w, 404, "计划任务不存在")
		return
	}
	t, err := s.fireSchedule(row, false)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"task_id": t.ID, "note": "已按规格建任务（不影响下次排期）"})
}

// fireSchedule 执行一次触发：建任务 + 记账。advance=false 表示"立即运行"（不动排期）。
func (s *Server) fireSchedule(row *db.TaskSchedule, advance bool) (*Task, error) {
	var spec scheduleSpec
	if err := json.Unmarshal(row.TaskSpec, &spec); err != nil {
		return nil, fmt.Errorf("规格不是合法 JSON：%w", err)
	}
	if err := s.validateScheduleSpec(spec); err != nil {
		return nil, err
	}
	t, err := s.createTaskFromSpec(spec)
	if err != nil {
		return nil, err
	}
	log.Printf("[schedule] 计划任务 #%d「%s」触发，建任务 #%s（%s）", row.ID, row.Name, t.ID, t.Description)
	_ = s.m.pg.FinishScheduleRun(row.ID, time.Now(), t.ID, "")
	return t, nil
}

// taskScheduleAudit 写一条全局审计（计划任务不属于任何任务，进不去任务时间线；
// 用系统日志 + 设置页的列表足够——这里刻意不造第四条"全局活动"存储）。
func (s *Server) taskScheduleAudit(msg string) { log.Printf("[schedule] %s", msg) }

// runDueSchedules 是 scheduler tick 的一部分：取到点的计划任务，逐个 CAS 抢跑。
func (sc *Scheduler) runDueSchedules(ctx context.Context) {
	due, err := sc.pg.DueTaskSchedules(time.Now(), 20)
	if err != nil || len(due) == 0 {
		return
	}
	for _, row := range due {
		// 抢跑**之前**再复查一次停机/时间窗。放在这里的而不是抢跑之后，是因为抢跑会推进
		// 水位：抢了却不建任务，这一次就永远丢了（停机期间本来就"不追补"，但那是用户
		// 可见的语义，不该由竞态决定）。
		if sc.s.safetyAdmissionBlocked() {
			return
		}
		if row.NextRunAt == nil {
			continue
		}
		var freq sched.Frequency
		if err := json.Unmarshal(row.Frequency, &freq); err != nil {
			_ = sc.pg.FinishScheduleRun(row.ID, time.Now(), "", "频率不是合法 JSON："+err.Error())
			continue
		}
		// 下一次从"现在"起算：停机期间错过的次数不追补（开机只跑这一次）。
		next, err := freq.Next(time.Now())
		if err != nil {
			_ = sc.pg.FinishScheduleRun(row.ID, time.Now(), "", "频率不合法："+err.Error())
			continue
		}
		won, err := sc.pg.AdvanceTaskSchedule(row.ID, *row.NextRunAt, next)
		if err != nil || !won {
			continue // 别的 tick 已经抢到了这一次
		}
		if _, err := sc.s.fireSchedule(row, true); err != nil {
			log.Printf("[schedule] 计划任务 #%d「%s」触发失败：%v", row.ID, row.Name, err)
			_ = sc.pg.FinishScheduleRun(row.ID, time.Now(), "", err.Error())
		}
	}
}
