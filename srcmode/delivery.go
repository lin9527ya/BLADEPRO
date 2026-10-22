package srcmode

import (
	"context"
	"fmt"

	"github.com/lin9527ya/bladepro/db"
)

// 交付面的缺口（P2，方案文档 §6.4 第 3/4 条）。
//
// 覆盖格清零回答"测完了吗"，这一层回答"交得出去吗"：medium+ 的每条发现都要有
// 齐备的可提交件（正文 + 合规证据），疑似重复要先定案。两层分开算，是因为处理
// 它们的人不同——补格是 planner 派意图，写报告是 reporter/planner 写正文。

// DeliveryArea 是交付类缺口在矩阵里的伪风险面名（UI 与提示词都按它分组）。
const DeliveryArea = "delivery"

// exemptFindingStatuses 是"不需要交付"的发现状态：被推翻的结论不该进台账。
var exemptFindingStatuses = map[string]bool{
	db.FindingRetracted:     true,
	db.FindingFalsePositive: true,
	db.FindingDuplicate:     true,
	db.FindingRuledOut:      true,
	db.FindingIgnored:       true,
	db.FindingRiskAccepted:  true,
}

// submissionDelivered 是"这条已经有件了"的台账状态。
var submissionDelivered = map[string]bool{
	db.SrcSubReady:     true,
	db.SrcSubSubmitted: true,
	db.SrcSubAccepted:  true,
	db.SrcSubPaid:      true,
	db.SrcSubDuplicate: true,
}

// deliveryGapsFor 返回交付面缺口，并顺带统计 medium+ 发现数与报告已齐备数。
func deliveryGapsFor(ctx context.Context, store *db.SrcStore, taskID int64) (gaps []Gap, mediumPlus, ready int) {
	if store == nil || taskID <= 0 {
		return nil, 0, 0
	}
	findings, err := store.SrcDeliveryFindings(ctx, taskID)
	if err != nil {
		// 交付面查不动时报一条缺口，而不是假装没问题：静默放行等于没有这道门。
		return []Gap{{
			Area: DeliveryArea, AreaTitle: "交付", Item: "query", Kind: GapReportMissing,
			Reason: "读取交付台账失败：" + err.Error(), Priority: 50,
		}}, 0, 0
	}
	for _, f := range findings {
		if !isMediumPlus(f.Severity) || exemptFindingStatuses[f.Status] {
			continue
		}
		mediumPlus++
		if f.ReportReady || submissionDelivered[f.SubmissionStatus] {
			ready++
		} else {
			gaps = append(gaps, Gap{
				Asset: f.Title, AssetType: "finding", Area: DeliveryArea, AreaTitle: "交付",
				Item: "report", Kind: GapReportMissing, FindingID: f.FindingID,
				State: f.SubmissionStatus,
				Reason: fmt.Sprintf("发现 #%d（%s）还没有齐备的可提交件：调 src_prepare_submission 写全描述/危害/修复并过证据合规",
					f.FindingID, severityLabelCN(f.Severity)),
				Priority: severityPriority(f.Severity),
			})
		}
		if f.DedupeOf > 0 {
			switch f.SubmissionStatus {
			case db.SrcSubDuplicate, db.SrcSubWithdrawn:
				// 已定案：重复的不交，撤回的不交。
			default:
				gaps = append(gaps, Gap{
					Asset: f.Title, AssetType: "finding", Area: DeliveryArea, AreaTitle: "交付",
					Item: "duplicate", Kind: GapDuplicate, FindingID: f.FindingID,
					Reason: fmt.Sprintf("发现 #%d 疑似与 #%d 是同一个洞：确认重复就把台账标 duplicate，不是同一个就解除 dedupe_of",
						f.FindingID, f.DedupeOf),
					Priority: severityPriority(f.Severity) + 5,
				})
			}
		}
	}
	return gaps, mediumPlus, ready
}

// severityPriority 让严重度高的发现先被补件（交付优先级）。
func severityPriority(severity string) int {
	switch severity {
	case "critical":
		return 40
	case "high":
		return 30
	case "medium":
		return 20
	}
	return 10
}

func severityLabelCN(severity string) string {
	switch severity {
	case "critical":
		return "严重"
	case "high":
		return "高危"
	case "medium":
		return "中危"
	case "low":
		return "低危"
	}
	return severity
}
