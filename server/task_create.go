package server

// 单任务与批量扇出共用的创建管线：校验请求 → 编译模式/策略/项目规则 → 落库 →
// 可选覆盖继承。把这段抽出来，是为了让 createTask 与 createTaskBatch 不会各写一套
// 「项目规则 / SRC 快照 / 全局预算」的解析逻辑而逐渐漂移。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/intercept"
	"github.com/lin9527ya/bladepro/srcmode"
)

// taskCreateErr carries the HTTP status the caller should return.
type taskCreateErr struct {
	code int
	msg  string
}

func (e *taskCreateErr) Error() string { return e.msg }

func taskErr(code int, msg string) *taskCreateErr { return &taskCreateErr{code: code, msg: msg} }

// preparedTaskCreate is one fully-resolved base task spec. Batch fan-out reuses it
// for every shard, so the program rules / mode snapshot / ROI prior are read and
// compiled once per batch rather than once per shard.
type preparedTaskCreate struct {
	req              createTaskReq
	sourceIDs        []int64
	companyIDs       []int64
	interceptRules   []db.TaskInterceptRuleInput
	policyDoc        json.RawMessage
	modeDoc          json.RawMessage
	srcDoc           srcmode.ModeDoc
	program          *srcmode.Program
	programInScope   []db.TaskScopeInput
	programOutScope  []db.TaskScopeInput
	inheritRequested bool
	inheritFromID    int64
}

// taskShardSpec overrides the per-task fields for one shard (nil = the single-task
// path, which takes everything from the request).
type taskShardSpec struct {
	Name           string
	Description    string
	Goal           string
	TokenBudget    int64
	Priority       int
	ScopeRows      []db.TaskScopeInput
	AssetIDs       []int64
	OmitCompanyIDs bool // 分片任务用精确 scope，不能把整个企业再灌进来
}

func (s *Server) prepareTaskCreate(ctx context.Context, req createTaskReq) (*preparedTaskCreate, *taskCreateErr) {
	if strings.TrimSpace(req.Description) == "" {
		req.Description = "未命名任务"
	}
	if req.SrcProgramID != nil && *req.SrcProgramID <= 0 {
		req.SrcProgramID = nil
	}
	if len(req.LLMProfileIDs) == 0 && req.LLMProfileID != nil {
		req.LLMProfileIDs = []int64{*req.LLMProfileID}
	}
	if err := s.validateTaskProfileIDs(req.LLMProfileIDs); err != nil {
		return nil, taskErr(400, err.Error())
	}
	if req.TimeoutSeconds < 0 {
		req.TimeoutSeconds = 0
	}
	if req.TokenBudget < 0 {
		req.TokenBudget = 0
	}
	if req.Priority < -1000 {
		req.Priority = -1000
	}
	if req.Priority > 1000 {
		req.Priority = 1000
	}
	if len(req.SourceTaskIDs) > db.MaxTaskSourceCount {
		return nil, taskErr(400, fmt.Sprintf("关联任务最多选择 %d 个", db.MaxTaskSourceCount))
	}
	sourceIDs := make([]int64, 0, len(req.SourceTaskIDs))
	seenSources := map[int64]bool{}
	for _, raw := range req.SourceTaskIDs {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || id <= 0 || seenSources[id] {
			return nil, taskErr(400, "关联任务 id 无效或重复")
		}
		if _, ok := s.m.Task(strconv.FormatInt(id, 10)); !ok {
			return nil, taskErr(400, fmt.Sprintf("关联任务 #%d 不存在", id))
		}
		seenSources[id] = true
		sourceIDs = append(sourceIDs, id)
	}
	companyIDs, err := db.NormalizeTaskCompanyIDs(req.CompanyIDs)
	if err != nil {
		return nil, taskErr(400, fmt.Sprintf("关联企业无效：最多选择 %d 个有效企业", db.MaxTaskCompanyCount))
	}
	req.CompanyIDs = companyIDs
	interceptRules, err := buildTaskInterceptRules(req.InterceptRules)
	if err != nil {
		return nil, taskErr(400, "任务级拦截规则无效："+err.Error())
	}
	// SRC 平台项目（P3）：选了项目就把它的规则编译进这个任务——模式、必做面、额度、
	// 禁用手段/写操作、命令级限额，全部在创建时落成快照，之后改项目不影响在跑的任务。
	var program *srcmode.Program
	var programInScope, programOutScope []db.TaskScopeInput
	if req.SrcProgramID != nil && *req.SrcProgramID > 0 {
		row, err := s.m.PG().Src().GetSrcProgram(ctx, *req.SrcProgramID)
		if err != nil {
			return nil, taskErr(500, err.Error())
		}
		if row == nil {
			return nil, taskErr(400, "SRC 平台项目不存在")
		}
		program, err = srcProgramFromRow(row)
		if err != nil {
			return nil, taskErr(400, "该项目的规则配置损坏："+err.Error())
		}
		if err := program.Validate(); err != nil {
			return nil, taskErr(400, "该项目的规则不合法："+err.Error())
		}
		// 选了平台项目就是 SRC 任务：不显式给 mode 时按 src 处理。
		if strings.TrimSpace(req.Mode) == "" {
			req.Mode = srcmode.ModeSrc
		}
		// 项目显式 in/out 范围：in 进 task_scope，out 进 task_scope_exclusions。
		scopeRows, err := s.m.PG().Src().ListProgramScope(ctx, program.ID)
		if err != nil {
			return nil, taskErr(500, err.Error())
		}
		for _, r := range scopeRows {
			ts := db.ProgramScopeToTaskScope(r, "平台项目范围")
			if r.Direction == "in" {
				programInScope = append(programInScope, ts)
			} else {
				programOutScope = append(programOutScope, ts)
			}
		}
		// 项目绑了企业就自动带上该企业的资产范围（"选项目自动灌范围"）。
		// 但项目已经显式写了 in 范围时，不再用企业粗范围把它放大。
		if len(programInScope) == 0 && program.CompanyID != nil && *program.CompanyID > 0 {
			exists := false
			for _, id := range req.CompanyIDs {
				if id == *program.CompanyID {
					exists = true
					break
				}
			}
			if !exists {
				req.CompanyIDs = append(req.CompanyIDs, *program.CompanyID)
			}
		}
	}
	companyIDs, err = db.NormalizeTaskCompanyIDs(req.CompanyIDs)
	if err != nil {
		return nil, taskErr(400, fmt.Sprintf("关联企业无效：最多选择 %d 个有效企业", db.MaxTaskCompanyCount))
	}
	req.CompanyIDs = companyIDs
	// SRC 任务的探索策略多一层内建红线（禁登出/注销/吊销）：它不依赖 program，
	// 是挖洞动作本身的边界；编译成命令门规则后，说了不算、拦得住才算。
	floor := programPolicyFloor(req.ExplorationPolicy, program)
	if strings.TrimSpace(req.Mode) == srcmode.ModeSrc {
		floor = srcBuiltinPolicyFloor(floor)
	}
	policyDoc, err := s.newTaskPolicy(floor)
	if err != nil {
		return nil, taskErr(400, err.Error())
	}
	// SRC 模式：创建时把「模式 + 覆盖配置」解析成快照落库，运行中只读。
	modeDoc, srcDoc, err := srcmode.Resolve(req.Mode, req.ModeDoc)
	if err != nil {
		return nil, taskErr(400, err.Error())
	}
	if program != nil {
		srcDoc = program.ApplyToModeDoc(srcDoc)
		note := program.Describe()
		if fb := s.programFeedbackNote(ctx, program.ID); fb != "" {
			if note != "" {
				note += " "
			}
			note += fb
		}
		if in, out, err := s.m.PG().Src().CountProgramScope(ctx, program.ID); err == nil && (in > 0 || out > 0) {
			scopeLine := fmt.Sprintf("项目显式范围：in %d 条 / out %d 条（建任务时编译进 task_scope）。", in, out)
			if note != "" {
				note += " "
			}
			note += scopeLine
		}
		if note != "" {
			srcDoc.ProgramNote = note
		}
		modeDoc, err = json.Marshal(srcDoc)
		if err != nil {
			return nil, taskErr(500, err.Error())
		}
	}
	// 侦察先行闸：mode_doc 显式给了就用自己的；没给则继承全局设置。
	if srcDoc.IsSrc() && srcDoc.ReconGate == nil {
		if s.srcReconGate() {
			on := true
			srcDoc.ReconGate = &on
			modeDoc, err = json.Marshal(srcDoc)
			if err != nil {
				return nil, taskErr(500, err.Error())
			}
		}
	}
	// 风险面模型分层（可选）：mode_doc 显式给了就用自己的；没给则继承全局
	// setting `src_area_tiers`。创建时快照，运行中改全局不影响已建任务。
	if srcDoc.IsSrc() && len(srcDoc.AreaTiers) == 0 {
		if tiers := s.srcAreaTiers(); len(tiers) > 0 {
			srcDoc.AreaTiers = tiers
			modeDoc, err = json.Marshal(srcDoc)
			if err != nil {
				return nil, taskErr(500, err.Error())
			}
		}
	}
	// 运行隔离：勾选“SRC 必须降权运行”时，没有真正启用 agent 沙箱的实例拒绝创建
	// SRC 任务——宁可创建失败，也不要给出一个“看起来隔离、实际同权”的任务。
	if srcDoc.IsSrc() && s.srcRequireIsolation() {
		srcDoc.RequireIsolation = true
		modeDoc, err = json.Marshal(srcDoc)
		if err != nil {
			return nil, taskErr(500, err.Error())
		}
	}
	if srcDoc.IsSrc() && s.srcRequireIsolation() && !AgentIsolationEnabled() {
		return nil, taskErr(400, "当前实例未启用 agent 降权运行（BLADEPRO_AGENT_USER），但设置要求 SRC 任务必须隔离：先按 deploy/README.md 配置并重启，或关闭该要求")
	}
	// 资产级 token 预算（P2）：mode_doc 显式给了就用自己的；没给（0）则继承全局
	// setting `src_asset_token_budget`，创建时快照进 mode_doc，运行中改全局不影响。
	if srcDoc.IsSrc() && (srcDoc.AssetTokenBudget <= 0 || srcDoc.AssetTimeBudgetMinutes <= 0 || srcDoc.AssetCostBudgetUSD <= 0) {
		changed := false
		if srcDoc.AssetTokenBudget <= 0 {
			if v := s.srcAssetTokenBudget(); v > 0 {
				srcDoc.AssetTokenBudget = v
				changed = true
			}
		}
		if srcDoc.AssetTimeBudgetMinutes <= 0 {
			if v := s.srcAssetTimeBudgetMinutes(); v > 0 {
				srcDoc.AssetTimeBudgetMinutes = v
				changed = true
			}
		}
		if srcDoc.AssetCostBudgetUSD <= 0 {
			if v := s.srcAssetCostBudgetUSD(); v > 0 {
				srcDoc.AssetCostBudgetUSD = v
				changed = true
			}
		}
		if changed {
			modeDoc, err = json.Marshal(srcDoc)
			if err != nil {
				return nil, taskErr(500, err.Error())
			}
		}
	}
	// 跨任务覆盖继承（P2）：校验来源后再建任务，避免建出一条永远无法执行的孤儿任务。
	inheritRequested := (req.InheritCoverage != nil && *req.InheritCoverage) ||
		(req.InheritCoverageFrom != nil && *req.InheritCoverageFrom > 0)
	if inheritRequested && !srcDoc.IsSrc() {
		return nil, taskErr(400, "覆盖继承仅适用于 SRC 挖掘任务")
	}
	var inheritFromID int64
	if req.InheritCoverageFrom != nil && *req.InheritCoverageFrom > 0 {
		inheritFromID = *req.InheritCoverageFrom
		srcTask, err := s.m.PG().GetTask(inheritFromID)
		if err != nil {
			return nil, taskErr(500, err.Error())
		}
		if srcTask == nil {
			return nil, taskErr(400, fmt.Sprintf("覆盖继承来源任务 #%d 不存在", inheritFromID))
		}
		if strings.TrimSpace(srcTask.Mode) != srcmode.ModeSrc {
			return nil, taskErr(400, fmt.Sprintf("覆盖继承来源任务 #%d 不是 SRC 任务", inheritFromID))
		}
	}
	// ROI 先验（P2）：按台账历史给风险面一个补格优先级乘子；创建时快照。
	if srcDoc.IsSrc() {
		var programID int64
		if req.SrcProgramID != nil {
			programID = *req.SrcProgramID
		}
		if roi := s.srcAreaROI(ctx, programID); len(roi) > 0 {
			srcDoc.AreaROI = roi
			modeDoc, err = json.Marshal(srcDoc)
			if err != nil {
				return nil, taskErr(500, err.Error())
			}
		}
	}
	return &preparedTaskCreate{
		req: req, sourceIDs: sourceIDs, companyIDs: companyIDs,
		interceptRules: interceptRules, policyDoc: policyDoc,
		modeDoc: modeDoc, srcDoc: srcDoc, program: program,
		programInScope: programInScope, programOutScope: programOutScope,
		inheritRequested: inheritRequested, inheritFromID: inheritFromID,
	}, nil
}

// persistPreparedTask creates the task (and its shard scope/assets) then applies
// coverage inheritance. It does not launch the engine; callers do that.
func (s *Server) persistPreparedTask(ctx context.Context, prep *preparedTaskCreate, shard *taskShardSpec) (*Task, *taskCreateErr) {
	if prep == nil {
		return nil, taskErr(500, "任务创建管线未初始化")
	}
	name, desc, goal := prep.req.Name, prep.req.Description, prep.req.Goal
	tokenBudget := prep.req.TokenBudget
	priority := prep.req.Priority
	companyIDs := prep.companyIDs
	var scopeRows []db.TaskScopeInput
	var assetIDs []int64
	scopeRows = append(scopeRows, prep.programInScope...)
	if prep.srcDoc.IsSrc() && len(scopeRows) == 0 && len(companyIDs) == 0 {
		for _, host := range intercept.ExtractTargets(prep.req.Description) {
			if ip := net.ParseIP(host); ip != nil {
				scopeRows = append(scopeRows, db.TaskScopeInput{Kind: "ip", Net: hostCIDR(ip), Reason: "任务描述中的明确目标"})
			} else {
				scopeRows = append(scopeRows, db.TaskScopeInput{Kind: "subdomain", Domain: host, Reason: "任务描述中的明确目标"})
			}
		}
	}

	if shard != nil {
		name, desc, goal = shard.Name, shard.Description, shard.Goal
		// 分片自带精确 scope 时，不要再叠加 program 的粗 in 范围（会放大分片）。
		if len(shard.ScopeRows) > 0 {
			scopeRows = append([]db.TaskScopeInput(nil), shard.ScopeRows...)
		}
		assetIDs = shard.AssetIDs
		if shard.TokenBudget > 0 {
			tokenBudget = shard.TokenBudget
		}
		if shard.Priority != 0 {
			priority = shard.Priority
		}
		if shard.OmitCompanyIDs {
			companyIDs = nil
		}
	}
	scopeExclusions := append([]db.TaskScopeInput(nil), prep.programOutScope...)
	t, err := s.m.CreateTaskWithOptions(desc, goal, db.TaskCreateOptions{
		Name: strings.TrimSpace(name), CategoryID: prep.req.CategoryID,
		SourceTaskIDs: prep.sourceIDs, CompanyIDs: companyIDs, LLMProfileIDs: prep.req.LLMProfileIDs,
		TimeoutSeconds: prep.req.TimeoutSeconds, TokenBudget: tokenBudget,
		Priority:             priority,
		PlanHeartbeatSeconds: prep.req.PlanHeartbeatSeconds,
		CoverageEnabled:      prep.req.CoverageEnabled,
		InterceptRules:       prep.interceptRules,
		ExplorationPolicy:    prep.policyDoc,
		Mode:                 prep.srcDoc.Mode,
		ModeDoc:              prep.modeDoc,
		SrcProgramID:         prep.req.SrcProgramID,
		ScopeRows:            scopeRows,
		ScopeExclusions:      scopeExclusions,
		AssetIDs:             assetIDs,
	})
	if err != nil {
		if errors.Is(err, db.ErrTaskCategoryInvalid) || errors.Is(err, db.ErrTaskCategoryNotFound) {
			return nil, taskErr(400, "任务分类不存在或无效")
		}
		if errors.Is(err, db.ErrTaskCompanyIDsInvalid) || errors.Is(err, db.ErrTaskCompanyNotFound) {
			return nil, taskErr(400, "关联企业不存在或无效")
		}
		if errors.Is(err, db.ErrTaskScopeInvalid) {
			return nil, taskErr(400, err.Error())
		}
		return nil, taskErr(500, err.Error())
	}
	if prep.srcDoc.IsSrc() {
		if err := s.m.pg.SealSrcAuthorization(ctx, parseIntDefault(t.ID)); err != nil {
			return nil, taskErr(500, "保存授权快照失败："+err.Error())
		}
	}
	// 单任务自动排队优先级：没有显式 priority 时，按范围内资产分级给一个
	// 可解释的分数（与批量扇出同一套权重）。批量分片已在 shard.Priority 里算过。
	if shard == nil && prep.req.Priority == 0 && prep.srcDoc.IsSrc() {
		if score, err := s.m.PG().Src().AutoTaskPriority(ctx, parseIntDefault(t.ID)); err == nil && score != 0 {
			if err := s.m.PG().SetTaskPriority(parseIntDefault(t.ID), score); err == nil {
				t.Priority = score
			}
		}
	}
	s.applyTaskCoverageInheritance(ctx, t, prep)
	return t, nil
}

// applyTaskCoverageInheritance handles the optional cross-task coverage copy.
// Failure never blocks task creation: inheritance is an accelerator, not a
// precondition.
func (s *Server) applyTaskCoverageInheritance(ctx context.Context, t *Task, prep *preparedTaskCreate) {
	if t == nil || prep == nil || !prep.inheritRequested || !prep.srcDoc.IsSrc() {
		return
	}
	taskID := parseIntDefault(t.ID)
	var programID int64
	if prep.req.SrcProgramID != nil {
		programID = *prep.req.SrcProgramID
	}
	fromID := prep.inheritFromID
	if fromID <= 0 {
		if autoID, err := s.m.PG().Src().LatestInheritSource(ctx, taskID, programID, prep.companyIDs, prep.sourceIDs, time.Now().Add(-30*24*time.Hour)); err != nil {
			log.Printf("[task] #%s 选择覆盖继承来源失败: %v", t.ID, err)
		} else {
			fromID = autoID
		}
	}
	if fromID <= 0 || fromID == taskID {
		return
	}
	res, err := s.m.PG().Src().InheritSrcCoverage(ctx, fromID, taskID)
	if err != nil {
		log.Printf("[task] #%s 从任务 #%d 继承覆盖失败: %v", t.ID, fromID, err)
		return
	}
	if res.Cells > 0 || res.Checks > 0 {
		summary := fmt.Sprintf("继承覆盖：从任务 #%d 复制 %d 格 tested_clear、%d 项收尾数字", fromID, res.Cells, res.Checks)
		log.Printf("[task] #%s %s", t.ID, summary)
		s.engine.emitActivity(t, db.Activity{Worker: "system", Kind: "src_gate", Summary: summary})
	}
}

func hostCIDR(ip net.IP) string {
	if ip.To4() != nil {
		return ip.String() + "/32"
	}
	return ip.String() + "/128"
}
