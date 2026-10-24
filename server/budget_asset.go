package server

// 资产级 token 预算：llm_usage 按意图主资产归因（asset_id），单个资产的
// 输入+输出达到 mode_doc.asset_token_budget 后：
//
//  1. planner 的 add_intent 对该资产直接拒绝（agent 侧同一条数据）；
//  2. 引擎把该资产未完成的必做格自动标成 budget_stop，并投递一次提示；
//  3. 预算转向其他资产，而不是整任务收尾（任务级预算仍由 budget.go 管）。
//
// 自动标 budget_stop 是幂等的：worker 后续写回的新结论会覆盖它（ON CONFLICT），
// 下一轮扫描也不会重复处理同一个 (task, asset)。

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

const (
	settingAssetTokenBudget = "src_asset_token_budget"
	settingAssetTimeBudget  = "src_asset_time_budget_minutes"
	// 资产预算扫描节流：llm_usage 是热表，每 15s 一次足够及时且不增加明显负担。
	assetBudgetSweepInterval = 15 * time.Second
	maxAssetTokenBudgetValue = int64(1_000_000_000_000)
)

// srcAssetTokenBudget resolves the global per-asset token budget (0 = off).
func (s *Server) srcAssetTokenBudget() int64 {
	return parseTokenBudgetSetting(s.m.pg, settingAssetTokenBudget)
}

// srcAssetTimeBudgetMinutes resolves the global per-asset wall-clock budget
// (minutes, 0 = off).
func (s *Server) srcAssetTimeBudgetMinutes() int {
	pg := s.m.pg
	if pg == nil {
		return 0
	}
	v, ok, err := pg.GetSetting(settingAssetTimeBudget)
	if err != nil || !ok {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 0
	}
	const max = 30 * 24 * 60
	if n > max {
		n = max
	}
	return n
}

// parseTokenBudgetSetting reads a non-negative token budget setting.
func parseTokenBudgetSetting(pg *db.DB, key string) int64 {
	if pg == nil {
		return 0
	}
	v, ok, err := pg.GetSetting(key)
	if err != nil || !ok {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// checkAssetTokenBudgets runs from the deadline coordinator. It is a no-op for
// non-SRC tasks and when the mode snapshot has no asset budget.
func (e *Engine) checkAssetTokenBudgets(ctx context.Context, t *Task) {
	if e == nil || e.m == nil || e.m.pg == nil || t == nil || strings.TrimSpace(t.Mode) != srcmode.ModeSrc {
		return
	}
	doc := srcmode.Parse(t.ModeDoc)
	if doc.AssetTokenBudget <= 0 {
		return
	}
	taskID := tidOfTask(t)
	if taskID <= 0 {
		return
	}
	exhausted, err := e.m.pg.Src().AssetsOverTokenBudget(ctx, taskID, doc.AssetTokenBudget)
	if err != nil {
		log.Printf("[budget] task %s 读取资产预算失败: %v", t.ID, err)
		return
	}
	for _, usage := range exhausted {
		key := t.ID + "|" + strconv.FormatInt(usage.AssetID, 10)
		if _, loaded := e.assetBudgetDone.LoadOrStore(key, true); loaded {
			continue
		}
		reason := fmt.Sprintf("资产 token 预算耗尽（%d/%d），未完成项自动收口为 budget_stop", usage.Tokens, doc.AssetTokenBudget)
		marked, err := markAssetStop(ctx, e.m.pg.Src(), taskID, doc, usage.AssetID, reason)
		if err != nil {
			e.assetBudgetDone.Delete(key) // 下一轮重试；不要吞掉一次瞬时 DB 错误
			log.Printf("[budget] task %s 资产 #%d 预算收口失败: %v", t.ID, usage.AssetID, err)
			continue
		}
		e.assetBudgetNotice(t, usage, doc, marked)
	}
}

// markAssetStop writes budget_stop for every still-blank required cell of one
// asset. Existing terminal cells (including a worker's fresh result) are left
// untouched; a worker that finishes later overwrites its own cell normally.
func markAssetStop(ctx context.Context, store *db.SrcStore, taskID int64, doc srcmode.ModeDoc, assetID int64, reason string) (int, error) {
	asset, err := store.SrcAssetInScope(ctx, taskID, assetID)
	if err != nil {
		return 0, err
	}
	if asset == nil {
		return 0, nil // 资产已移出范围：不再为它写格
	}
	marked := 0
	for _, area := range doc.RequiredFor(asset.Type) {
		items := []string{srcmode.AreaAll}
		if hard := doc.HardItemsFor(area); len(hard) > 0 {
			items = hard
		}
		for _, item := range items {
			existing, err := store.GetSrcCell(ctx, taskID, assetID, area, item)
			if err != nil {
				return marked, err
			}
			if existing != nil {
				continue // 已有终态（包括刚跑完的 worker 结果）：不改写
			}
			if _, err := store.UpsertSrcCell(ctx, &db.SrcCell{
				TaskID: taskID, AssetID: assetID, Area: area, Item: item,
				State: db.SrcStateBudgetStop, Reason: reason, UpdatedBy: "budget",
			}); err != nil {
				return marked, err
			}
			marked++
		}
	}
	return marked, nil
}

// checkAssetTimeBudgets is the wall-clock sibling of checkAssetTokenBudgets:
// once an asset has been worked on for longer than the per-asset budget, its
// untouched cells are closed as budget_stop and the planner is told to move on.
// Time is measured from the asset's first attributed LLM call, so assets that
// were never worked on are never "exhausted".
func (e *Engine) checkAssetTimeBudgets(ctx context.Context, t *Task) {
	if e == nil || e.m == nil || e.m.pg == nil || t == nil || strings.TrimSpace(t.Mode) != srcmode.ModeSrc {
		return
	}
	doc := srcmode.Parse(t.ModeDoc)
	if doc.AssetTimeBudgetMinutes <= 0 {
		return
	}
	taskID := tidOfTask(t)
	if taskID <= 0 {
		return
	}
	exhausted, err := e.m.pg.Src().AssetsOverTimeBudget(ctx, taskID, doc.AssetTimeBudgetMinutes)
	if err != nil {
		log.Printf("[budget] task %s 读取资产时间预算失败: %v", t.ID, err)
		return
	}
	for _, usage := range exhausted {
		key := "time|" + t.ID + "|" + strconv.FormatInt(usage.AssetID, 10)
		if _, loaded := e.assetTimeDone.LoadOrStore(key, true); loaded {
			continue
		}
		reason := fmt.Sprintf("资产时间预算耗尽（已运行 %d 分钟 / 上限 %d 分钟），未完成项自动收口为 budget_stop",
			usage.UsedMinutes, doc.AssetTimeBudgetMinutes)
		marked, err := markAssetStop(ctx, e.m.pg.Src(), taskID, doc, usage.AssetID, reason)
		if err != nil {
			e.assetTimeDone.Delete(key)
			log.Printf("[budget] task %s 资产 #%d 时间预算收口失败: %v", t.ID, usage.AssetID, err)
			continue
		}
		text := fmt.Sprintf("⏱️【资产时间预算耗尽】资产 #%d 已投入 %d 分钟（上限 %d）：系统已把该资产 %d 个未完成格标为 budget_stop。把预算让给其他资产（focus_asset），不要在同一资产上继续加码。",
			usage.AssetID, usage.UsedMinutes, doc.AssetTimeBudgetMinutes, marked)
		if !doc.AllowBudgetStop {
			text += " 当前任务不允许 budget_stop 放行，收尾门仍会把这些格报成缺口：需要人工放行或提高 asset_time_budget_minutes。"
		}
		e.emitActivity(t, db.Activity{Worker: "system", Kind: "system",
			Summary: fmt.Sprintf("资产 #%d 时间预算耗尽（%d/%d 分钟），已收口 %d 格",
				usage.AssetID, usage.UsedMinutes, doc.AssetTimeBudgetMinutes, marked),
			Detail: text})
		t.trigMu.Lock()
		t.pendingTriggers = append(t.pendingTriggers, agent.TriggerEvent{Kind: "hint", Hints: []string{text}})
		t.trigMu.Unlock()
		t.Notify()
		log.Printf("[budget] task %s 资产 #%d 时间预算耗尽 (%d/%d 分钟)，已收口 %d 格",
			t.ID, usage.AssetID, usage.UsedMinutes, doc.AssetTimeBudgetMinutes, marked)
	}
}

// assetBudgetNotice tells the planner once per exhausted asset, and records an
// activity so a human reviewing the timeline sees why the asset stopped.
func (e *Engine) assetBudgetNotice(t *Task, usage db.AssetTokenUsage, doc srcmode.ModeDoc, marked int) {
	text := fmt.Sprintf("🛑【资产预算耗尽】资产 #%d 已用 %d/%d token：系统已把该资产 %d 个未完成格标为 budget_stop，新的意图会被拒绝。把预算让给其他资产（focus_asset），不要在同一资产上换措辞重派。",
		usage.AssetID, usage.Tokens, doc.AssetTokenBudget, marked)
	if !doc.AllowBudgetStop {
		text += " 当前任务不允许 budget_stop 放行，收尾门仍会把这些格报成缺口：需要人工放行或提高 asset_token_budget。"
	}
	e.emitActivity(t, db.Activity{Worker: "system", Kind: "system",
		Summary: fmt.Sprintf("资产 #%d token 预算耗尽（%d/%d），已收口 %d 格", usage.AssetID, usage.Tokens, doc.AssetTokenBudget, marked),
		Detail:  text})
	t.trigMu.Lock()
	t.pendingTriggers = append(t.pendingTriggers, agent.TriggerEvent{Kind: "hint", Hints: []string{text}})
	t.trigMu.Unlock()
	t.Notify()
	log.Printf("[budget] task %s 资产 #%d 预算耗尽 (%d/%d)，已收口 %d 格", t.ID, usage.AssetID, usage.Tokens, doc.AssetTokenBudget, marked)
}
