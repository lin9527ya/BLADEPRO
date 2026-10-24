package server

// ROI 先验：把台账的历史结果按「受控漏洞类型 → src 风险面」聚合成补格优先级乘子。
// 只在创建任务时算一次，写进 mode_doc.area_roi 快照（与其它规则快照同一语义）。

import (
	"context"
	"math"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/report"
)

// srcSettledStatuses 是参与 ROI 的台账状态：draft 还在准备中，不计入分母。
var srcSettledStatuses = map[string]bool{
	"ready": true, "submitted": true, "accepted": true,
	"rejected": true, "duplicate": true, "paid": true,
}

// srcAreaForVulnClass 把受控漏洞类型映射到 srcmode 的风险面；映射不了返回空。
func srcAreaForVulnClass(class string) string {
	switch class {
	case "sql_injection", "nosql_injection":
		return "injection"
	case "command_injection", "deserialization", "xxe":
		return "rce"
	case "authorization", "unauth":
		return "authz"
	case "xss":
		return "xss"
	case "ssrf":
		return "ssrf"
	case "upload", "path_traversal":
		return "upload"
	case "logic", "business_logic", "race_condition":
		return "logic"
	case "session", "jwt", "oauth", "default_credentials":
		return "session"
	case "information_disclosure":
		return "cred"
	}
	return ""
}

// srcAreaROIFromCounts 是纯判定核：accepted/paid 记 1 分、ready 记 0.5、
// rejected/duplicate 记 -0.25；按已结算台账求产出率，再映射到 0.5–1.5 乘子。
func srcAreaROIFromCounts(counts []db.SrcClassStatus) map[string]float64 {
	type acc struct {
		total, accepted, ready, bad int
	}
	byArea := map[string]*acc{}
	for _, c := range counts {
		if !srcSettledStatuses[c.Status] {
			continue
		}
		area := srcAreaForVulnClass(report.CanonicalVulnClass(c.VulnClass))
		if area == "" {
			continue
		}
		a := byArea[area]
		if a == nil {
			a = &acc{}
			byArea[area] = a
		}
		a.total += c.Count
		switch c.Status {
		case "accepted", "paid":
			a.accepted += c.Count
		case "ready":
			a.ready += c.Count
		case "rejected", "duplicate":
			a.bad += c.Count
		}
	}
	out := make(map[string]float64, len(byArea))
	for area, a := range byArea {
		if a.total <= 0 {
			continue
		}
		score := float64(a.accepted) + 0.5*float64(a.ready) - 0.25*float64(a.bad)
		if score < 0 {
			score = 0
		}
		mult := 0.5 + score/float64(a.total)
		if mult < 0.5 {
			mult = 0.5
		}
		if mult > 1.5 {
			mult = 1.5
		}
		out[area] = math.Round(mult*100) / 100
	}
	return out
}

// srcAreaROI 取项目维度历史；该项目已结算台账不足 5 条时回落到全局，避免小样本噪声。
func (s *Server) srcAreaROI(ctx context.Context, programID int64) map[string]float64 {
	store := s.m.PG().Src()
	counts, err := store.ListSrcClassStatusCounts(ctx, programID)
	if err != nil {
		return nil
	}
	if programID > 0 && srcSettledTotal(counts) < 5 {
		if global, gerr := store.ListSrcClassStatusCounts(ctx, 0); gerr == nil {
			counts = global
		}
	}
	if srcSettledTotal(counts) == 0 {
		return nil
	}
	return srcAreaROIFromCounts(counts)
}

func srcSettledTotal(counts []db.SrcClassStatus) int {
	n := 0
	for _, c := range counts {
		if srcSettledStatuses[c.Status] {
			n += c.Count
		}
	}
	return n
}
