package server

// SRC 任务级告警的写入口：收尾门拒绝、策略封死、预算耗尽、疑似重复、提交结果、
// SLA 超时。它们与漏洞通知共用 notification_events/deliveries，但 finding_id=0，
// 渲染成任务告警而不是漏洞卡片（见 notify.IsTaskEvent / Item.Event*）。
//
// 写入永不阻断主流程：通知是便利功能，任务状态/台账才是产品本身。失败只记日志。

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/notify"
)

// notifyTaskAlert records one task-scoped notification event. findingID may be 0;
// taskID 必填（没有任务的告警无法回链，也不该产生）。
func notifyTaskAlert(ctx context.Context, pg *db.DB, kind string, taskID, findingID int64, title, body, severity string) {
	if pg == nil || taskID <= 0 || !notify.IsTaskEvent(kind) {
		return
	}
	if strings.TrimSpace(title) == "" {
		title = notify.TaskEventLabel(kind)
	}
	snap := notify.Snapshot{
		Kind: kind, TaskID: taskID, FindingID: findingID,
		Title: title, Body: body, Severity: severity, Summary: body,
	}
	if _, err := pg.AddNotificationEvent(ctx, kind, findingID, snap); err != nil {
		log.Printf("[notify] 写入任务告警失败 task=%d kind=%s: %v", taskID, kind, err)
	}
}

// notifyTaskAlertFromTask is the convenience form when a *Task is at hand.
func (e *Engine) notifyTaskAlert(t *Task, kind, title, body, severity string) {
	if e == nil || e.m == nil || t == nil {
		return
	}
	notifyTaskAlert(context.Background(), e.m.PG(), kind, tidOfTask(t), 0, title, body, severity)
}

// budgetAlertText keeps task-budget messages actionable in the IM card.
func budgetAlertText(stage string, used, budget int64) (title, severity string) {
	title = fmt.Sprintf("Token 预算 %s（%d/%d）", stage, used, budget)
	severity = "medium"
	switch stage {
	case "critical":
		severity = "high"
	case "exhausted":
		severity = "high"
	}
	return title, severity
}

const (
	// srcSLAAlertInterval is how often overdue ledger items are scanned. SLA is
	// measured in days, so 6 hours is already far more frequent than needed; the
	// per-item cooldown below prevents repeats.
	srcSLAAlertInterval = 6 * time.Hour
	srcSLAAlertCooldown = 72 * time.Hour
	srcSLADefaultDays   = 7
)

var srcSLAAlertKinds = map[string]bool{
	"ready_stale":     true,
	"submitted_stale": true,
	"accepted_unpaid": true,
}

// runSrcSLAAlerts periodically pushes overdue ledger items to channels that opted
// into task alerts. It writes through the same notification_events pipeline as all
// other alerts; ClaimSrcAlert gives each item a 72h cooldown.
func (s *Server) runSrcSLAAlerts(ctx context.Context) {
	if s == nil || s.m == nil || s.m.pg == nil {
		return
	}
	// First scan is delayed so startup does not compete with migrations/seeding.
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	t := time.NewTicker(srcSLAAlertInterval)
	defer t.Stop()
	for {
		s.scanSrcSLAAlerts(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) scanSrcSLAAlerts(ctx context.Context) {
	items, err := s.m.pg.ReviewQueue(ctx, 200, srcSLADefaultDays)
	if err != nil {
		log.Printf("[src-alert] 读取 SLA 队列失败: %v", err)
		return
	}
	for _, it := range items {
		if !srcSLAAlertKinds[it.Kind] || it.TaskID <= 0 {
			continue
		}
		key := fmt.Sprintf("sla:%s:%d:%d:%d", it.Kind, it.TaskID, it.FindingID, it.SubmissionID)
		ok, err := s.m.pg.ClaimSrcAlert(ctx, key, srcSLAAlertCooldown)
		if err != nil || !ok {
			continue
		}
		label := reviewKindLabel(it.Kind)
		body := fmt.Sprintf("任务：%s\n事项：%s", it.TaskLabel, label)
		if it.Title != "" {
			body += "\n发现：" + it.Title
		}
		if it.Reason != "" {
			body += "\n说明：" + it.Reason
		}
		notifyTaskAlert(ctx, s.m.PG(), notify.EventSrcSLA, it.TaskID, it.FindingID,
			"SLA 超时："+label, body, "medium")
	}
}
