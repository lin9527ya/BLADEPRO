package server

// P1 提交前独立复验：medium+ 发现准备交付件前，必须有一次 verdict=reproduced
// 的独立复验（finding_retests）。复验由 retester agent 在自己的会话里完成——
// 干净上下文、只按原始证据做最小重放，不复用 worker 的推理过程。
//
// 为什么放在交付准备里：这是"能不能交"的最后一道人类可理解的关口。发现入库时
// 还不知道报告写不写得完；到了准备提交件这一刻，正文与证据都齐了，正好复验。

import (
	"context"
	"fmt"

	"github.com/lin9527ya/bladepro/db"
)

const srcRetestNotes = "SRC 交付前独立复验：只按原始证据做最小重放，验证漏洞是否仍可复现；" +
	"不要扩展测试范围，不要做破坏性操作。结论 reproduced 才能准备提交件。"

// srcRetestExemptStatuses 与交付门的豁免口径一致：这些发现不需要复验。
var srcRetestExemptStatuses = map[string]bool{
	db.FindingRetracted:     true,
	db.FindingFalsePositive: true,
	db.FindingDuplicate:     true,
	db.FindingRuledOut:      true,
	db.FindingIgnored:       true,
	db.FindingRiskAccepted:  true,
}

// srcRetestState 扫描复验记录（ListFindingRetests 按 id DESC 返回）：
// passed=有 completed+reproduced；active=有 pending/running；lastCompleted=最近一条
// completed 的 verdict（用于告诉人"上次为什么没过"）。
func srcRetestState(rows []*db.FindingRetest) (passed, active bool, lastCompleted string) {
	for _, r := range rows {
		if r == nil {
			continue
		}
		switch r.Status {
		case "pending", "running":
			active = true
		case "completed":
			if lastCompleted == "" {
				lastCompleted = r.Verdict
			}
			if r.Verdict == "reproduced" {
				passed = true
			}
		}
	}
	return passed, active, lastCompleted
}

// ensureSrcRetest 返回空串表示已通过；否则返回一段给交付台账 issue 的说明。
// 只在"从未复验过"时自动发起；已有 completed 结论（fixed/inconclusive）不再自动
// 重试，避免 planner 每次准备都烧一轮复验。
func (s *Server) ensureSrcRetest(ctx context.Context, f *db.DBFinding) string {
	if f == nil || srcRetestExemptStatuses[f.Status] {
		return ""
	}
	rows, err := s.m.pg.ListFindingRetests(f.ID)
	if err != nil {
		return "无法读取独立复验记录：" + err.Error()
	}
	passed, active, lastVerdict := srcRetestState(rows)
	if passed {
		return ""
	}
	if active {
		return "已有独立复验在进行中；等结论 reproduced 后再准备交付件"
	}
	if lastVerdict != "" {
		return fmt.Sprintf("上次独立复验结论为 %s，未通过；请人工复核或补充证据后重新发起复验（不会自动重试）", lastVerdict)
	}
	retest, created, _, err := s.startFindingRetestCore(ctx, f.ID, srcRetestNotes)
	if err != nil {
		return "需要独立复验，但自动发起失败：" + err.Error()
	}
	label := "独立复验"
	if retest != nil && retest.ConversationID != nil {
		label = fmt.Sprintf("独立复验（会话 #%d）", *retest.ConversationID)
	}
	if created {
		return "已自动发起" + label + "；等结论 reproduced 后再准备交付件"
	}
	return label + "已在进行中；等结论 reproduced 后再准备交付件"
}
