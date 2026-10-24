package server

// 台账结果 → 打法库反馈（P1 平台结果回流的一部分）。
//
// accepted：把这条已受理的发现沉淀成"(指纹, 漏洞类型)"卡片，并记一次 worked；
// rejected：不新建卡片，只给同技术栈 + 同类型的既有卡片记 failed（打法可能不适用）。
// duplicate 不记失败——洞是真的，只是别人先报了，失败会污染打法成功率。

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/report"
)

func (s *Server) syncPlaybookForVerdict(ctx context.Context, sub *db.SrcSubmission) {
	if sub == nil || (sub.Status != db.SrcSubAccepted && sub.Status != db.SrcSubRejected) {
		return
	}
	f, err := s.m.PG().GetFinding(sub.FindingID)
	if err != nil || f == nil {
		log.Printf("[src-feedback] 读发现 #%d 失败: %v", sub.FindingID, err)
		return
	}
	assets := s.resolveAssetIDs(f.AssetIDs)
	fp := playbookFingerprintForAssets(assets, f.AssetIDs)
	if !hasPlaybookSignal(fp) {
		return
	}
	class := report.CanonicalVulnClass(f.VulnClass)
	if class == "" {
		return
	}
	store := s.m.PG().Assets()
	note := srcVerdictNote(sub)

	if sub.Status == db.SrcSubRejected {
		matches, err := store.MatchPlaybooks(fp, 8)
		if err != nil {
			log.Printf("[src-feedback] 匹配打法失败（发现 #%d）: %v", f.ID, err)
			return
		}
		for _, m := range matches {
			if report.CanonicalVulnClass(m.Playbook.TechniqueKey) != class {
				continue
			}
			if _, err := store.RecordPlaybookFeedback(m.ID, "failed", note); err != nil {
				log.Printf("[src-feedback] 记 failed 失败（playbook #%d）: %v", m.ID, err)
			}
		}
		return
	}

	// accepted：沉淀/更新卡片，并记 worked。
	steps := strings.TrimSpace(sub.ReportMD)
	if steps == "" {
		steps = strings.TrimSpace(f.Report)
	}
	if steps == "" {
		steps = strings.TrimSpace(f.Evidence)
	}
	if steps == "" {
		steps = fmt.Sprintf("平台已受理；复现证据见发现 #%d。", f.ID)
	}
	title := firstNonEmpty(strings.TrimSpace(sub.Title), strings.TrimSpace(f.Name), f.VulnClass)
	summary := strings.TrimSpace(f.Summary)
	if summary == "" {
		summary = fmt.Sprintf("平台受理的 %s（发现 #%d）", class, f.ID)
	}
	sourceNode := int64(0)
	if f.NodeID != nil {
		sourceNode = *f.NodeID
	}
	card, _, err := store.UpsertPlaybook(db.Playbook{
		Fingerprint:  fp,
		TechniqueKey: class,
		Title:        title,
		Summary:      clipRunes(summary, 200),
		Steps:        steps,
		Evidence:     f.Evidence,
		Confidence:   "high",
		SourceTaskID: strconv.FormatInt(sub.TaskID, 10),
		SourceNodeID: sourceNode,
	})
	if err != nil {
		log.Printf("[src-feedback] 沉淀打法失败（发现 #%d）: %v", f.ID, err)
		return
	}
	if card != nil {
		if _, err := store.RecordPlaybookFeedback(card.ID, "worked", note); err != nil {
			log.Printf("[src-feedback] 记 worked 失败（playbook #%d）: %v", card.ID, err)
		}
	}
}

// playbookFingerprintForAssets 按资产 id 顺序聚合指纹（顺序稳定，结果可复现）。
func playbookFingerprintForAssets(assets map[int64]*db.Asset, ids []int64) db.PlaybookFingerprint {
	var fp db.PlaybookFingerprint
	for _, id := range ids {
		a := assets[id]
		if a == nil {
			continue
		}
		fp.Technologies = append(fp.Technologies, a.Technologies...)
		if fp.FaviconMMH3 == "" {
			fp.FaviconMMH3 = a.FaviconMMH3
		}
		if fp.ServiceName == "" {
			fp.ServiceName = a.ServiceName
		}
		if fp.Port == 0 && a.Port != nil {
			fp.Port = *a.Port
		}
		if fp.PageTitle == "" {
			fp.PageTitle = a.PageTitle
		}
	}
	return fp.Normalize()
}

func hasPlaybookSignal(fp db.PlaybookFingerprint) bool {
	return len(fp.Technologies) > 0 || fp.FaviconMMH3 != "" || fp.ServiceName != "" || fp.PageTitle != ""
}
