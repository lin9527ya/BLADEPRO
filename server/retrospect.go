package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/lin9527ya/bladepro/db"
)

// P-Q 任务复盘（移植自 quiver retrospect，简化：不做 LLM 蒸馏）：
//
//	POST /api/tasks/{id}/retrospect  手动复盘
//	设置 retro_auto_enabled=true     任务终态后由 scheduler 自动复盘（水位 retro_last_task_id）
//
// 产物一律是知识提案（approvals），不直接写入知识库——复盘最容易产生"看起来对"的结论，
// 必须人工过一遍。

const (
	settingRetroAuto  = "retro_auto_enabled"
	retroWatermarkKey = "retro_last_task_id"
	retroSummaryLimit = 5
)

// handleTaskRetrospect 手动复盘一个任务。
func (s *Server) handleTaskRetrospect(w http.ResponseWriter, r *http.Request) {
	taskID := parseIntDefault(r.PathValue("id"))
	if taskID <= 0 {
		writeErr(w, 400, "bad task id")
		return
	}
	apID, err := s.runTaskRetrospect(r.Context(), taskID)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"approval_id": apID, "note": "复盘提案已进入审批队列"})
}

// runTaskRetrospect 汇总任务统计并生成一条 methodology 知识提案。
func (s *Server) runTaskRetrospect(ctx context.Context, taskID int64) (int64, error) {
	var (
		desc, goal, status string
		companyID          int64
		explorationID      int64
	)
	err := s.m.pg.QueryRowContext(ctx, `SELECT description, goal, status, COALESCE(company_id,0), exploration_id
		FROM tasks WHERE id=$1 AND deleted_at IS NULL`, taskID).Scan(&desc, &goal, &status, &companyID, &explorationID)
	if err != nil {
		return 0, fmt.Errorf("任务 %d 不存在或已删除", taskID)
	}
	var assets, facts, findings, intents, blocked, falsified int
	_ = s.m.pg.QueryRowContext(ctx, `SELECT count(*) FROM assets WHERE $1 = ANY(task_ids)`, taskID).Scan(&assets)
	_ = s.m.pg.QueryRowContext(ctx, `SELECT
		count(*) FILTER (WHERE kind='fact'),
		count(*) FILTER (WHERE kind='finding'),
		count(*) FILTER (WHERE kind='intent'),
		count(*) FILTER (WHERE kind='intent' AND state='blocked'),
		count(*) FILTER (WHERE kind='intent' AND falsified)
		FROM exploration_nodes WHERE exploration_id=$1`, explorationID).Scan(&facts, &findings, &intents, &blocked, &falsified)

	var b strings.Builder
	fmt.Fprintf(&b, "# 任务复盘 #%d\n\n", taskID)
	fmt.Fprintf(&b, "- 状态：%s\n- 资产：%d\n- 事实：%d\n- 发现：%d\n- 意图：%d（blocked %d / 被证伪 %d）\n",
		status, assets, facts, findings, intents, blocked, falsified)
	fmt.Fprintf(&b, "\n## 目标\n%s\n\n## 描述\n%s\n", strings.TrimSpace(goal), strings.TrimSpace(desc))
	b.WriteString("\n## 复盘要点（待人工补充）\n")
	b.WriteString("- 哪些方向验证有效？为什么？\n- 哪些方向被证伪？触发条件与否定证据是什么？\n- 哪些方向 blocked？缺少什么前置？\n- 下一次同类目标应优先做什么、避免什么？\n")
	b.WriteString("\n（本提案由统计自动生成，不含 LLM 蒸馏结论；请在批准前补齐要点。）")

	// P-Q：有激活 LLM profile 时让模型基于统计与样本写复盘；失败保留结构化骨架。
	bodyMD := b.String()
	distilled := false
	distillErr := ""
	if sample := s.retrospectSamples(ctx, explorationID); sample != "" {
		system := "你是 SRC 赏金任务的复盘助手。基于任务统计与样本，写一份可复用的复盘（Markdown，≤800 字）：" +
			"1) 哪些方向验证有效、为什么；2) 哪些方向被证伪、否定证据是什么；3) 哪些方向 blocked、缺什么前置；" +
			"4) 下一次同类目标优先做什么、避免什么。只写能从统计/样本推出的结论，不编造；不确定标 inferred。"
		user := fmt.Sprintf("目标：%s\n描述：%s\n统计：状态=%s 资产=%d 事实=%d 发现=%d 意图=%d（blocked %d，证伪 %d）\n样本：\n%s",
			strings.TrimSpace(goal), strings.TrimSpace(desc), status, assets, facts, findings, intents, blocked, falsified, sample)
		if out, err := s.distillText(ctx, system, user); err == nil && len([]rune(out)) >= 80 {
			bodyMD = "# 任务复盘 #" + fmt.Sprintf("%d", taskID) + "\n\n" + out
			distilled = true
		} else if err != nil && !errors.Is(err, errNoLLM) {
			distillErr = err.Error()
		}
	}

	summary := fmt.Sprintf("任务复盘 #%d：资产 %d / 事实 %d / 发现 %d / 意图 %d（blocked %d，证伪 %d）",
		taskID, assets, facts, findings, intents, blocked, falsified)
	scope := db.KnowledgeScopeGlobal
	if companyID > 0 {
		scope = db.KnowledgeScopeCompany
	}
	payload, _ := json.Marshal(map[string]any{
		"entry_type": db.KnowledgeTypeMethodology, "scope": scope, "company_id": companyID,
		"title": fmt.Sprintf("任务复盘 #%d", taskID), "summary": summary, "body_md": bodyMD,
		"source_type": "task_retro", "source_ref": fmt.Sprintf("task:%d", taskID),
		"source_task_id": fmt.Sprintf("%d", taskID),
	})
	reason := "任务复盘提案"
	if distilled {
		reason = "任务复盘提案（LLM 蒸馏）"
	} else if distillErr != "" {
		reason = "任务复盘提案（LLM 蒸馏失败，使用统计骨架）：" + distillErr
	}
	ap, err := s.knowledgeStore().CreateApproval(db.ApprovalKindKnowledge, payload, reason)
	if err != nil {
		return 0, err
	}
	return ap.ID, nil
}

// runDueRetrospects 由 scheduler 调用：终态任务按水位逐个自动复盘（默认关）。
func (s *Server) runDueRetrospects(ctx context.Context) {
	if !s.m.pg.GetBool(settingRetroAuto, false) {
		return
	}
	last := 0
	if v, _, _ := s.m.pg.GetSetting(retroWatermarkKey); strings.TrimSpace(v) != "" {
		if n := parseIntDefault(v); n > 0 {
			last = int(n)
		}
	}
	rows, err := s.m.pg.QueryContext(ctx, `SELECT id FROM tasks
		WHERE id > $1 AND deleted_at IS NULL AND completed_at IS NOT NULL
		  AND status IN ('done','failed','timeout','policy_blocked')
		ORDER BY id LIMIT $2`, last, retroSummaryLimit)
	if err != nil {
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if _, err := s.runTaskRetrospect(ctx, id); err != nil {
			// 单个失败不阻塞水位推进以外的事务；记录后继续。
			continue
		}
		last = int(id)
	}
	if last > 0 {
		_ = s.m.pg.SetSetting(retroWatermarkKey, fmt.Sprintf("%d", last))
	}
}

// retrospectSamples 取少量样本（发现/blocked/证伪意图）供蒸馏参考。
func (s *Server) retrospectSamples(ctx context.Context, explorationID int64) string {
	rows, err := s.m.pg.QueryContext(ctx, `SELECT kind, state,
			COALESCE(payload->>'summary',''), COALESCE(payload->>'note','')
		FROM exploration_nodes
		WHERE exploration_id=$1 AND (
			kind='finding'
			OR (kind='intent' AND (state='blocked' OR falsified))
		)
		ORDER BY id DESC LIMIT 30`, explorationID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var b strings.Builder
	n := 0
	for rows.Next() {
		var kind, state, summary, note string
		if rows.Scan(&kind, &state, &summary, &note) != nil {
			continue
		}
		n++
		fmt.Fprintf(&b, "- [%s/%s] %s%s\n", kind, state, distillClip(summary, 160), func() string {
			if strings.TrimSpace(note) != "" {
				return "（" + distillClip(note, 120) + "）"
			}
			return ""
		}())
		if n >= 20 {
			break
		}
	}
	return b.String()
}
