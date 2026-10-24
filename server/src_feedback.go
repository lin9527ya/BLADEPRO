package server

// P1 平台结果回流：把台账的受理/驳回结果变成下一次任务的先验。
//
// 只做两件事：
//   1. 任务创建时把该项目的历史成绩与驳回首因写进 ProgramNote（静态快照，进提示词）；
//   2. 台账进入 accepted/rejected 时同步打法库反馈（worked/failed），受理的沉淀卡片。
// 不做平台 API 自动回填，也不改已经跑着的任务快照。

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/lin9527ya/bladepro/db"
)

// srcVerdictReasonLabels 是给提示词看的中文说明。
var srcVerdictReasonLabels = map[string]string{
	db.SrcVerdictNotReproducible:  "无法复现",
	db.SrcVerdictDuplicate:        "已重复",
	db.SrcVerdictInsufficient:     "信息不全",
	db.SrcVerdictNotVulnerability: "非漏洞/不接收",
	db.SrcVerdictOutOfScope:       "超出范围",
	db.SrcVerdictOther:            "其他",
}

// srcVerdictAdvice 把驳回首因翻成一句可执行动作。
func srcVerdictAdvice(code string) string {
	switch code {
	case db.SrcVerdictNotReproducible:
		return "提交前必须过独立复验，保留可重放的整包证据。"
	case db.SrcVerdictDuplicate:
		return "交付前用 src_dedupe 查重，确认不是已报过的洞。"
	case db.SrcVerdictInsufficient:
		return "正文/证据要写全：影响、复现步骤、修复建议、证明截图。"
	case db.SrcVerdictNotVulnerability:
		return "先确认平台收不收这一类，别把信息级/预期行为当漏洞交。"
	case db.SrcVerdictOutOfScope:
		return "提交前核对 program 范围，排除资产不要交。"
	}
	return ""
}

// programFeedbackNote 生成一行历史反馈；没有已提交记录时返回空串。
func (s *Server) programFeedbackNote(ctx context.Context, programID int64) string {
	fb, err := s.m.PG().Src().SrcProgramFeedback(ctx, programID)
	if err != nil || fb.Submitted == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "【历史成绩】已交 %d，受理 %d，驳回 %d，重复 %d。",
		fb.Submitted, fb.Accepted, fb.Rejected, fb.Duplicate)
	if len(fb.Reasons) > 0 {
		// 按次数降序，次数相同按码排序，保证快照稳定。
		type kv struct {
			code string
			n    int
		}
		items := make([]kv, 0, len(fb.Reasons))
		for code, n := range fb.Reasons {
			items = append(items, kv{code, n})
		}
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].n != items[j].n {
				return items[i].n > items[j].n
			}
			return items[i].code < items[j].code
		})
		parts := make([]string, 0, 3)
		for i, it := range items {
			if i >= 3 {
				break
			}
			label := srcVerdictReasonLabels[it.code]
			if label == "" {
				label = it.code
			}
			parts = append(parts, fmt.Sprintf("%s×%d", label, it.n))
		}
		b.WriteString("驳回原因：" + strings.Join(parts, "、") + "。")
		if advice := srcVerdictAdvice(items[0].code); advice != "" {
			b.WriteString("优先规避：" + advice)
		}
	}
	return b.String()
}

// srcVerdictNote 是台账更新时写给打法库的反馈说明。
func srcVerdictNote(sub *db.SrcSubmission) string {
	if sub == nil {
		return ""
	}
	label := srcVerdictReasonLabels[sub.VerdictReason]
	if label == "" {
		label = strings.TrimSpace(sub.VerdictReason)
	}
	parts := []string{"平台 " + sub.Status}
	if label != "" {
		parts = append(parts, "原因："+label)
	}
	if n := strings.TrimSpace(sub.Note); n != "" {
		parts = append(parts, n)
	}
	return strings.Join(parts, "；")
}
