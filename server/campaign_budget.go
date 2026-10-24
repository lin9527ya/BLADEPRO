package server

// 战役级 token 预算：同一 campaign 下的所有分片任务累计用量达到上限后，
// 每个仍在跑的任务在自己的 deadline coordinator 里进入收尾时序（停派/领新
// 意图 → drain → 终局判定）。通知按 campaign 只发一次，避免 N 个分片刷 N 条。

import (
	"context"
	"fmt"
	"log"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/notify"
)

// checkCampaignBudget returns true when it settled the task because the campaign
// budget is exhausted. Non-campaign tasks / no budget return false immediately.
func (e *Engine) checkCampaignBudget(ctx context.Context, t *Task) bool {
	if e == nil || e.m == nil || e.m.pg == nil || t == nil {
		return false
	}
	taskID := tidOfTask(t)
	if taskID <= 0 {
		return false
	}
	campaignID, budget, used, err := e.m.pg.CampaignBudgetForTask(ctx, taskID)
	if err != nil {
		log.Printf("[budget] task %s 读取战役预算失败: %v", t.ID, err)
		return false
	}
	if campaignID <= 0 || budget <= 0 || used < budget {
		return false
	}
	key := fmt.Sprint(campaignID)
	if _, loaded := e.campaignBudgetDone.LoadOrStore(key, true); !loaded {
		text := fmt.Sprintf("战役 #%d 的 token 预算已耗尽（%d/%d）：所属任务将逐个进入收尾，停止新探索。",
			campaignID, used, budget)
		e.emitActivity(t, db.Activity{Worker: "system", Kind: "system",
			Summary: fmt.Sprintf("战役 #%d 预算耗尽（%d/%d），任务进入收尾", campaignID, used, budget),
			Detail:  text})
		e.notifyTaskAlert(t, notify.EventSrcBudget, "战役 token 预算耗尽", text, "high")
		log.Printf("[budget] campaign #%d exhausted (%d/%d), settling task %s", campaignID, used, budget, t.ID)
	}
	e.settleTask(ctx, t, "战役 token 预算耗尽", "timeout")
	return true
}
