package server

// 人工复核队列：把「该人看一眼」的项按严重度 × 不确定性 × 临界程度排序，
// 支持批量接受 / 打回 / 退回重测。动作复用既有状态机：
//   - 发现类：PATCH 状态（confirmed / false_positive）或启动独立复测；
//   - 疑似重复：确认则把台账推到 duplicate，打回则清除 dedupe_of；
//   - budget_stop / 四件套全 N/A：退回重测 = 删除该格（变回空白）。
// 每个动作写 review_decisions，已裁决项不再出现在队列里；底层行变化会产生新 ref_key。

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

var reviewKinds = map[string]bool{
	"check_unverified":    true,
	"report_missing":      true,
	"retest_inconclusive": true,
	"dedupe_pending":      true,
	"budget_stop":         true,
	"suspicious_na":       true,
	"ready_stale":         true,
	"submitted_stale":     true,
	"accepted_unpaid":     true,
}

var fourPieceAreas = []string{"injection", "ssrf", "xss", "rce"}

func (s *Server) listReviewQueue(w http.ResponseWriter, r *http.Request) {
	limit := atoiDefault(r.URL.Query().Get("limit"), 200)
	kindFilter := strings.TrimSpace(r.URL.Query().Get("kind"))
	slaDays := atoiDefault(r.URL.Query().Get("sla_days"), 7)
	if slaDays <= 0 || slaDays > 90 {
		slaDays = 7
	}
	items, err := s.m.pg.ReviewQueue(r.Context(), limit, slaDays)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	decisions, err := s.m.pg.ReviewDecisions(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	counts := map[string]int{}
	for _, it := range items {
		if kindFilter != "" && it.Kind != kindFilter {
			continue
		}
		if _, decided := decisions[it.Kind+"|"+it.RefKey]; decided {
			continue
		}
		counts[it.Kind]++
		out = append(out, map[string]any{
			"kind": it.Kind, "ref_key": it.RefKey, "score": reviewScore(it),
			"task_id": it.TaskID, "task_label": it.TaskLabel,
			"finding_id": it.FindingID, "submission_id": it.SubmissionID, "retest_id": it.RetestID,
			"asset_id": it.AssetID, "asset_label": it.AssetLabel,
			"area": it.Area, "item": it.Item, "state": it.State,
			"reason": it.Reason, "title": it.Title, "severity": it.Severity,
			"updated_at": it.UpdatedAt.Format(time.RFC3339),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := out[i]["score"].(int), out[j]["score"].(int)
		if si != sj {
			return si > sj
		}
		return out[i]["updated_at"].(string) > out[j]["updated_at"].(string)
	})
	if len(out) > limit && limit > 0 {
		out = out[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": out, "counts": counts, "total": len(out)})
}

func reviewScore(it db.ReviewItem) int {
	sev := 30
	switch strings.ToLower(it.Severity) {
	case "critical":
		sev = 100
	case "high":
		sev = 80
	case "medium":
		sev = 60
	case "low":
		sev = 40
	}
	bonus := 15
	switch it.Kind {
	case "dedupe_pending":
		bonus = 50
	case "report_missing":
		bonus = 40
	case "retest_inconclusive":
		bonus = 35
	case "ready_stale":
		bonus = 45
	case "submitted_stale":
		bonus = 40
	case "accepted_unpaid":
		bonus = 35
	case "budget_stop":
		bonus = 20
	}
	age := int(time.Since(it.UpdatedAt).Hours() / 24)
	if age < 0 {
		age = 0
	}
	if age > 30 {
		age = 30
	}
	return sev + bonus + age
}

type reviewActionItem struct {
	Kind         string `json:"kind"`
	RefKey       string `json:"ref_key"`
	TaskID       int64  `json:"task_id"`
	FindingID    int64  `json:"finding_id"`
	SubmissionID int64  `json:"submission_id"`
	AssetID      int64  `json:"asset_id"`
	Area         string `json:"area"`
	Item         string `json:"item"`
}

type reviewActionResult struct {
	RefKey string `json:"ref_key"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

func (s *Server) postReviewActions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string             `json:"action"`
		Note   string             `json:"note"`
		Items  []reviewActionItem `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	action := strings.TrimSpace(req.Action)
	switch action {
	case "accept", "reject", "retest":
	default:
		writeErr(w, 400, "action 只能是 accept / reject / retest")
		return
	}
	if len(req.Items) == 0 || len(req.Items) > 100 {
		writeErr(w, 400, "一次需要 1–100 个复核项")
		return
	}
	note := strings.TrimSpace(req.Note)
	if len([]rune(note)) > 2000 {
		writeErr(w, 400, "备注最多 2000 个字符")
		return
	}
	results := make([]reviewActionResult, 0, len(req.Items))
	for _, it := range req.Items {
		res := reviewActionResult{RefKey: it.RefKey}
		if err := s.applyReviewAction(r.Context(), action, it, note); err != nil {
			res.Error = err.Error()
		} else {
			res.OK = true
		}
		results = append(results, res)
	}
	okCount := 0
	for _, res := range results {
		if res.OK {
			okCount++
		}
	}
	writeJSON(w, 200, map[string]any{"action": action, "ok": okCount, "failed": len(results) - okCount, "results": results})
}

func (s *Server) applyReviewAction(ctx context.Context, action string, it reviewActionItem, note string) error {
	if !reviewKinds[it.Kind] {
		return fmt.Errorf("未知复核类型 %q", it.Kind)
	}
	if strings.TrimSpace(it.RefKey) == "" {
		return fmt.Errorf("缺少 ref_key")
	}
	switch it.Kind {
	case "check_unverified":
		if it.TaskID <= 0 || it.AssetID <= 0 || it.Item == "" {
			return fmt.Errorf("缺少收尾项 task/asset/check_key")
		}
		row, err := s.m.pg.Src().GetSrcCheck(ctx, it.TaskID, it.AssetID, it.Item)
		if err != nil {
			return err
		}
		if row == nil {
			return fmt.Errorf("收尾项不存在")
		}
		if err := srcmode.ValidateCheck(ctx, s.m.pg.Src(), it.TaskID, s.srcDocForTask(it.TaskID), *row); err != nil {
			return err
		}
		switch action {
		case "accept":
			if err := s.m.pg.Src().ApproveSrcCheck(ctx, it.TaskID, it.AssetID, it.Item, it.RefKey, note); err != nil {
				return err
			}
		case "reject", "retest":
			if it.RefKey != fmt.Sprintf("check_unverified:%d:%d", row.ID, row.UpdatedAt.UnixNano()) {
				return fmt.Errorf("收尾项已变化，请刷新")
			}
			if deleted, err := s.m.pg.Src().DeleteUnverifiedSrcCheck(ctx, *row); err != nil {
				return err
			} else if !deleted {
				return fmt.Errorf("收尾项已变化，未删除")
			}
		}

	case "report_missing", "retest_inconclusive", "dedupe_pending":
		if it.FindingID <= 0 {
			return fmt.Errorf("缺少 finding_id")
		}
	case "budget_stop":
		if it.TaskID <= 0 || it.AssetID <= 0 || strings.TrimSpace(it.Area) == "" {
			return fmt.Errorf("预算停项缺少 task_id/asset_id/area")
		}
	case "suspicious_na":
		if it.TaskID <= 0 || it.AssetID <= 0 {
			return fmt.Errorf("四件套 N/A 项缺少 task_id/asset_id")
		}
	case "ready_stale", "submitted_stale", "accepted_unpaid":
		if it.FindingID <= 0 || it.SubmissionID <= 0 {
			return fmt.Errorf("台账 SLA 项缺少 finding_id/submission_id")
		}
	}
	switch action {
	case "accept":
		switch it.Kind {
		case "report_missing", "retest_inconclusive":
			if it.FindingID <= 0 {
				return fmt.Errorf("缺少 finding_id")
			}
			_, found, _, err := s.m.pg.SetFindingStatusWithNotify(ctx, it.FindingID, db.FindingConfirmed)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("发现 #%d 不存在", it.FindingID)
			}
		case "dedupe_pending":
			if it.SubmissionID > 0 {
				status := db.SrcSubDuplicate
				if _, err := s.m.pg.Src().UpdateSrcSubmission(ctx, it.SubmissionID, db.SrcSubmissionPatch{Status: &status, Actor: "review"}); err != nil {
					return err
				}
			}
		case "ready_stale", "submitted_stale", "accepted_unpaid":
			// 接受 = 认可当前等待状态，只留痕；平台结论仍由平台给。
		}
	case "reject":
		switch it.Kind {
		case "report_missing", "retest_inconclusive":
			if it.FindingID <= 0 {
				return fmt.Errorf("缺少 finding_id")
			}
			_, found, _, err := s.m.pg.SetFindingStatusWithNotify(ctx, it.FindingID, db.FindingFalsePositive)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("发现 #%d 不存在", it.FindingID)
			}
		case "dedupe_pending":
			if it.FindingID <= 0 {
				return fmt.Errorf("缺少 finding_id")
			}
			if err := s.m.pg.Src().SetSrcDedupeOf(ctx, it.FindingID, 0); err != nil {
				return err
			}
		case "budget_stop":
			if _, err := s.m.pg.Src().DeleteSrcCells(ctx, it.TaskID, it.AssetID, it.Area, it.Item); err != nil {
				return err
			}
		case "suspicious_na":
			for _, area := range fourPieceAreas {
				if _, err := s.m.pg.Src().DeleteSrcCells(ctx, it.TaskID, it.AssetID, area, ""); err != nil {
					return err
				}
			}
		case "ready_stale":
			// 打回一个还没提交的 ready 件 = 撤回，不让它继续占着交付队列。
			status := db.SrcSubWithdrawn
			if _, err := s.m.pg.Src().UpdateSrcSubmission(ctx, it.SubmissionID, db.SrcSubmissionPatch{Status: &status, Actor: "review"}); err != nil {
				return err
			}
		case "submitted_stale", "accepted_unpaid":
			// 平台已接手，不能由本地"打回"改写平台结论；只留痕让人去平台跟进。
		}
	case "retest":
		switch it.Kind {
		case "report_missing", "retest_inconclusive", "dedupe_pending":
			if it.FindingID <= 0 {
				return fmt.Errorf("缺少 finding_id")
			}
			if _, _, _, err := s.startFindingRetestCore(ctx, it.FindingID, note); err != nil {
				return err
			}
		case "budget_stop":
			if _, err := s.m.pg.Src().DeleteSrcCells(ctx, it.TaskID, it.AssetID, it.Area, it.Item); err != nil {
				return err
			}
		case "suspicious_na":
			for _, area := range fourPieceAreas {
				if _, err := s.m.pg.Src().DeleteSrcCells(ctx, it.TaskID, it.AssetID, area, ""); err != nil {
					return err
				}
			}
		case "ready_stale", "submitted_stale", "accepted_unpaid":
			if _, _, _, err := s.startFindingRetestCore(ctx, it.FindingID, note); err != nil {
				return err
			}
		}
	}
	if err := s.m.pg.RecordReviewDecision(ctx, it.Kind, it.RefKey, action, note); err != nil {
		return err
	}
	if it.TaskID > 0 {
		if t, ok := s.m.Task(strconv.FormatInt(it.TaskID, 10)); ok {
			s.engine.emitActivity(t, db.Activity{Worker: "system", Kind: "src_gate",
				Summary: fmt.Sprintf("人工复核：%s %s（%s）", reviewKindLabel(it.Kind), action, clipRunesLocal(note, 120))})
		}
	}
	log.Printf("[review] %s %s action=%s", it.Kind, it.RefKey, action)
	return nil
}

func reviewKindLabel(kind string) string {
	switch kind {
	case "report_missing":
		return "报告缺段"
	case "retest_inconclusive":
		return "复验存疑"
	case "dedupe_pending":
		return "疑似重复"
	case "budget_stop":
		return "预算停"
	case "suspicious_na":
		return "四件套全 N/A"
	case "ready_stale":
		return "待提交超 SLA"
	case "submitted_stale":
		return "已提交超 SLA"
	case "accepted_unpaid":
		return "已受理未到账"
	}
	return kind
}
