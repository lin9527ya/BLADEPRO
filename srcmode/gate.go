package srcmode

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/lin9527ya/bladepro/db"
)

// Gap 是收尾门算出来的一个缺口。Kind 说明"缺什么"，而不只是"没测"——
// 一个缺 reason 的 na 和一个从没人碰过的空格，对 planner 是完全不同的两件事。
type Gap struct {
	AssetID   int64  `json:"asset_id"`
	Asset     string `json:"asset"`
	AssetType string `json:"asset_type"`
	Area      string `json:"area"`
	AreaTitle string `json:"area_title,omitempty"`
	Item      string `json:"item"`
	Kind      string `json:"kind"`
	State     string `json:"state,omitempty"`
	// FindingID 只对交付类缺口（report_missing/duplicate）有意义：缺的是"这条发现"的件。
	FindingID int64 `json:"finding_id,omitempty"`
	// EvidenceNodeID 是收尾闸缺口指向的证据节点（做完但数字不达标时用）。
	EvidenceNodeID int64  `json:"evidence_node_id,omitempty"`
	Reason         string `json:"reason,omitempty"` // 缺口说明（为什么不算过）
	Attempts       int    `json:"attempts"`
	Priority       int    `json:"priority"` // 越大越该先补
}

// 缺口种类。
const (
	GapBlank             = "blank"              // 没有终态行：这片根本没测
	GapMissingReason     = "missing_reason"     // na/budget_stop 没写原因
	GapMissingEvidence   = "missing_evidence"   // tested_clear 没证据也没说明
	GapMissingFinding    = "missing_finding"    // tested_found 没挂 finding
	GapBudgetStopBlocked = "budget_stop"        // budget_stop 而本次不允许它放行
	GapUnknownAssetType  = "unknown_asset_type" // 资产类型没有必做面定义（配置漏了）
	GapReportMissing     = "report_missing"     // medium+ 发现没有齐备的可提交件
	GapDuplicate         = "duplicate"          // 疑似重复未处理（dedupe_of 挂着但没定案）
	GapMissingCheck      = "missing_check"      // 收尾闸没做/没做完（JS 抽全、路径枚举…）
	// GapNoAssets 是"分母为 0"：任务范围内没有任何站点资产。SRC 的完成判定以
	// 资产 × 必做面为分母，分母为 0 不是完成，而是范围/资产没准备好（keyword 只是
	// 给 agent 看的提示，不认领资产，见 db/task_scope.go）。
	GapNoAssets = "no_assets"
)

// Report 是一次收尾门判定的完整结果，同时供 agent 的 src_state 与 UI 使用。
type Report struct {
	TaskID     int64 `json:"task_id"`
	Assets     int   `json:"assets"`      // 参与判定的范围内站点资产数
	CellsTotal int   `json:"cells_total"` // 应有格数 = Σ 资产 × 必做面
	// CellsTerminal 是有终态的格数；CellsMeasured 只算 tested_*（真测过的）。
	CellsTerminal int `json:"cells_terminal"`
	CellsMeasured int `json:"cells_measured"`
	// 交付面：medium+ 发现数 / 其中报告已齐备的条数（SkipReportGate 时不统计）。
	FindingsMediumPlus int `json:"findings_medium_plus"`
	ReportsReady       int `json:"reports_ready"`
	// InheritedCells / InheritedChecks 是终态里来自历史任务的部分（创建任务时可选
	// 继承），用于 UI/提示区分"本轮实测"与"继承结论"。
	InheritedCells  int `json:"inherited_cells,omitempty"`
	InheritedChecks int `json:"inherited_checks,omitempty"`
	// 收尾闸（技能的 site-close-checklist）：应有项 / 已终态项 / 按项统计。
	ChecksTotal int            `json:"checks_total"`
	ChecksDone  int            `json:"checks_done"`
	ChecksByKey map[string]int `json:"checks_by_key,omitempty"` // key → 缺口数
	// FocusAsset 是当前应集中打的资产（技能"一种子闭环"：一个 host 挖完再换）；
	// 只影响排序与提示，不新增阻塞条件。
	FocusAssetID int64  `json:"focus_asset_id,omitempty"`
	FocusAsset   string `json:"focus_asset,omitempty"`
	// SuspiciousNA 是"四件套（注入/SSRF/XSS/RCE）全是 na"的资产数：不阻塞，但要人复核。
	SuspiciousNA int            `json:"suspicious_na,omitempty"`
	GapsTotal    int            `json:"gaps_total"`
	GapsByArea   map[string]int `json:"gaps_by_area"`
	// Gaps 按优先级降序，最多 50 条（完整清单走 UI 的矩阵）。
	Gaps []Gap `json:"gaps"`
	// Waived 是 allow_budget_stop=true 时被放行的 budget_stop 格（要在报告备注里写明）。
	Waived []Gap `json:"waived,omitempty"`
	// Finishable 是收尾门的结论：true = 可以落 done。
	Finishable bool `json:"finishable"`
	// BlockedReason 是给 planner/人看的一句话（Finishable=false 时非空）。
	BlockedReason string `json:"blocked_reason,omitempty"`
	// AssetYield 是每个资产的终态/发现统计（用于 ROI 止损展示）。
	AssetYield []AssetYield `json:"asset_yield,omitempty"`
	// ColdAssets 是"已测若干格仍零发现"的资产：只降权/提示，不阻塞收尾。
	ColdAssets []int64 `json:"cold_assets,omitempty"`
	// ReconPending 是还没做完清点（js_enum / paths_enum）的 app/service 资产。
	// 侦察不是收尾才能做的杂项：先清点再打 payload，才能知道到底有哪些路径/JS
	// 入口。它不新增阻塞（收尾门照样会报缺口），但 planner 应优先把它清零。
	ReconPending []ReconAsset `json:"recon_pending,omitempty"`
}

// ReconAsset 是一个待完成侦察的资产及其缺的清点项。
type ReconAsset struct {
	AssetID int64    `json:"asset_id"`
	Asset   string   `json:"asset"`
	Missing []string `json:"missing"`
}

// AssetYield 是一个资产的产出画像；Cold 表示已投入若干终态格但仍零发现。
type AssetYield struct {
	AssetID  int64  `json:"asset_id"`
	Asset    string `json:"asset"`
	Terminal int    `json:"terminal"`
	Found    int    `json:"found"`
	Tier     string `json:"tier,omitempty"`
	Cold     bool   `json:"cold,omitempty"`
}

// coldAssetTerminal 是判定"冷资产"的终态格数量门槛。
const coldAssetTerminal = 4

// assetYield 统计一个资产的终态/发现；已投入 ≥4 个终态格仍零发现 → 冷资产。
func assetYield(asset db.SrcAsset, cells []db.SrcCell) AssetYield {
	st := AssetYield{AssetID: asset.ID, Asset: asset.Label, Tier: asset.Tier}
	for _, c := range cells {
		switch c.State {
		case db.SrcStateFound:
			st.Terminal++
			st.Found++
		case db.SrcStateClear, db.SrcStateNA, db.SrcStateBudgetStop:
			st.Terminal++
		}
	}
	st.Cold = st.Terminal >= coldAssetTerminal && st.Found == 0
	return st
}

const maxReportGaps = 50

// CheckFinish 是 SRC 模式的收尾门：把"范围内资产 × 必做面"逐个对账，返回缺口。
// 只读、无副作用；有缺口时调用方（engine）负责拒绝 done 并注入提示。
func CheckFinish(ctx context.Context, store *db.SrcStore, taskID int64, doc ModeDoc) (Report, error) {
	rep := Report{TaskID: taskID, GapsByArea: map[string]int{}, Gaps: []Gap{}}
	if store == nil || taskID <= 0 {
		// 没有 SRC 存储（单测 / 未接线）时不臆造缺口：放行由调用方决定。
		rep.Finishable = true
		return rep, nil
	}
	assets, err := store.InScopeSrcAssets(ctx, taskID)
	if err != nil {
		return rep, err
	}
	cellsByAsset, err := store.SrcCellsByAsset(ctx, taskID)
	if err != nil {
		return rep, err
	}
	checksByAsset, err := store.SrcChecksByAsset(ctx, taskID)
	if err != nil {
		return rep, err
	}
	rep.Assets = len(assets)
	gaps := make([]Gap, 0, 16)
	waived := make([]Gap, 0, 4)
	// ROI 止损画像：终态格多、发现为零的资产降权（但仍是缺口，不会静默放行）。
	yieldByAsset := make(map[int64]AssetYield, len(assets))
	for _, asset := range assets {
		st := assetYield(asset, cellsByAsset[asset.ID])
		yieldByAsset[asset.ID] = st
		rep.AssetYield = append(rep.AssetYield, st)
		if st.Cold {
			rep.ColdAssets = append(rep.ColdAssets, asset.ID)
		}
	}
	if len(assets) == 0 {
		// fail-closed：没有可测资产时必须留一条缺口，不能因为"循环没跑"就 Finishable=true。
		gaps = append(gaps, Gap{
			Asset: "任务范围", AssetType: "scope", Area: "scope", AreaTitle: "范围", Item: "*",
			Kind:     GapNoAssets,
			Reason:   "任务范围内没有可测站点资产：检查 task_scope、资产导入与公司范围（keyword 仅作提示，不认领资产）",
			Priority: 100,
		})
	}

	for _, asset := range assets {
		areas := doc.RequiredFor(asset.Type)
		if len(areas) == 0 {
			// 该类型没有必做面定义：配置漏了比"没测"更该被看见，所以报缺口。
			gaps = append(gaps, Gap{
				AssetID: asset.ID, Asset: asset.Label, AssetType: asset.Type,
				Area: "*", Item: "*", Kind: GapUnknownAssetType,
				Reason: "该资产类型没有必做面定义", Priority: AssetValue(asset.Type) * 10,
			})
			continue
		}
		assetMult := AssetTierMultiplier(asset.Tier)
		if st := yieldByAsset[asset.ID]; st.Cold {
			assetMult *= 0.3
		}
		byArea := map[string]map[string]db.SrcCell{}
		for _, cell := range cellsByAsset[asset.ID] {
			if byArea[cell.Area] == nil {
				byArea[cell.Area] = map[string]db.SrcCell{}
			}
			byArea[cell.Area][cell.Item] = cell
		}
		for _, area := range areas {
			// 一格的计数单位是"必做项"：有 hard_items 时该面要点亮多项，否则只有 '*'
			// 一项。用 area 计数会让 cells_total 明显小于实际待办，UI 与提示词都会失真。
			items, cells := requiredCells(area, doc, byArea[area])
			rep.CellsTotal += len(items)
			for _, item := range items {
				cell, ok := cells[item]
				if !ok {
					gaps = append(gaps, Gap{
						AssetID: asset.ID, Asset: asset.Label, AssetType: asset.Type,
						Area: area, AreaTitle: AreaTitle(area), Item: item, Kind: GapBlank,
						Reason: "未测（没有终态）", Priority: cellPriority(asset.Type, area, 0, doc, assetMult),
					})
					continue
				}
				rep.CellsTerminal++
				if cell.InheritedFromTaskID > 0 {
					rep.InheritedCells++
				}
				if cell.State == db.SrcStateFound || cell.State == db.SrcStateClear {
					rep.CellsMeasured++
				}
				if g, bad := cellGap(asset, area, cell, doc, assetMult); bad {
					gaps = append(gaps, g)
					continue
				}
				if cell.State == db.SrcStateBudgetStop {
					waived = append(waived, Gap{
						AssetID: asset.ID, Asset: asset.Label, AssetType: asset.Type,
						Area: area, AreaTitle: AreaTitle(area), Item: cell.Item,
						Kind: GapBudgetStopBlocked, State: cell.State, Reason: cell.Reason,
						Attempts: cell.Attempts, Priority: cellPriority(asset.Type, area, cell.Attempts, doc, assetMult),
					})
				}
			}
		}

		// 四件套全 N/A：不阻塞（na_code 已保证有依据），但要让复核的人看见——
		// 技能的原话是"四件套全 N/A 原因逐条写清，并满足 N/A 条件"。
		if fourPieceAllNA(areas, byArea) {
			rep.SuspiciousNA++
		}

		// 收尾闸：面之外的第二层。缺项 = 没测，不是"测了没有"。
		if !doc.SkipCheckGate && doc.Tier() != CheckTierOff {
			if missing := reconMissing(asset, checksByAsset[asset.ID], doc); len(missing) > 0 {
				rep.ReconPending = append(rep.ReconPending, ReconAsset{AssetID: asset.ID, Asset: asset.Label, Missing: missing})
			}
			for _, def := range ChecksFor(asset.Type) {
				rep.ChecksTotal++
				chk, ok := checksByAsset[asset.ID][def.Key]
				if !ok {
					addCheckGap(&gaps, &rep, asset, def, GapMissingCheck, "", "未做："+def.Hint, 0, assetMult)
					continue
				}
				rep.ChecksDone++
				if chk.InheritedFromTaskID > 0 {
					rep.InheritedChecks++
				}
				switch chk.State {
				case db.SrcCheckDone:
					if ok, why := checkSatisfied(def, doc, chk.Metrics); !ok {
						addCheckGap(&gaps, &rep, asset, def, GapMissingCheck, chk.State, "数字不达标："+why, chk.EvidenceNodeID, assetMult)
					}
				case db.SrcCheckNA:
					// 写入侧已校验理由码；N/A 是终态，不再报缺口。
				case db.SrcCheckBudgetStop:
					if !doc.AllowBudgetStop {
						addCheckGap(&gaps, &rep, asset, def, GapBudgetStopBlocked, chk.State, chk.Reason, chk.EvidenceNodeID, assetMult)
					}
				}
			}
		}
	}

	// 交付面：格清零只是"测完了"，可提交件齐备才算"能交"。两件事分开算、分开报，
	// 因为处理它们的人（planner 派意图 / reporter 写报告）不是同一个人。
	// 注意顺序：交付缺口必须在排序**之前**并入，否则会被 50 条上限挤出去——那等于
	// "看不见的缺口"，比没有这道门更糟。
	if !doc.SkipReportGate {
		deliveryGaps, mediumPlus, ready := deliveryGapsFor(ctx, store, taskID)
		rep.FindingsMediumPlus, rep.ReportsReady = mediumPlus, ready
		gaps = append(gaps, deliveryGaps...)
	}
	// 当前该集中打的资产（只影响排序与提示，不阻塞）。
	rep.FocusAssetID, rep.FocusAsset = focusAsset(gaps)
	sort.SliceStable(gaps, func(i, j int) bool {
		if gaps[i].Priority != gaps[j].Priority {
			return gaps[i].Priority > gaps[j].Priority
		}
		if gaps[i].AssetID != gaps[j].AssetID {
			return gaps[i].AssetID < gaps[j].AssetID
		}
		if gaps[i].Area != gaps[j].Area {
			return gaps[i].Area < gaps[j].Area
		}
		return gaps[i].Item < gaps[j].Item
	})
	for _, g := range gaps {
		rep.GapsByArea[g.Area]++
	}
	rep.GapsTotal = len(gaps)
	rep.Gaps = gaps
	if len(gaps) > maxReportGaps {
		rep.Gaps = gaps[:maxReportGaps]
	}

	// budget_stop 的处置：本次不允许放行时，把放行候选也算成缺口（否则它等于白测完）。
	if len(waived) > 0 && !doc.AllowBudgetStop {
		for _, w := range waived {
			w.Kind = GapBudgetStopBlocked
			w.Reason = fmt.Sprintf("budget_stop 不放行（allow_budget_stop=false）：%s", w.Reason)
			if w.Reason == "" {
				w.Reason = "budget_stop 不放行（allow_budget_stop=false）"
			}
			rep.GapsByArea[w.Area]++
		}
		rep.GapsTotal += len(waived)
		merged := append(append([]Gap{}, gaps...), waived...)
		sort.SliceStable(merged, func(i, j int) bool { return merged[i].Priority > merged[j].Priority })
		rep.Gaps = merged
		if len(rep.Gaps) > maxReportGaps {
			rep.Gaps = rep.Gaps[:maxReportGaps]
		}
		waived = nil
	}
	rep.Waived = waived

	rep.Finishable = rep.GapsTotal == 0
	if !rep.Finishable {
		if rep.Assets == 0 {
			rep.BlockedReason = "任务范围内没有可测站点资产：检查 task_scope 与资产导入"
		} else {
			rep.BlockedReason = fmt.Sprintf("还有 %d 个覆盖缺口未清零（共 %d 格，已终态 %d）",
				rep.GapsTotal, rep.CellsTotal, rep.CellsTerminal)
		}
	}
	return rep, nil
}

// requiredCells 决定某个面需要哪些终态行：有 hard_items 时逐个 item，否则只要 '*'。
// 已经存在的行全部返回，方便调用方复用（含不在必做清单里的 item）。
func requiredCells(area string, doc ModeDoc, cells map[string]db.SrcCell) ([]string, map[string]db.SrcCell) {
	items := doc.HardItemsFor(area)
	if len(items) == 0 {
		items = []string{AreaAll}
	}
	return items, cells
}

// cellGap 把一个终态行判成"过"还是"缺什么"。
func cellGap(asset db.SrcAsset, area string, cell db.SrcCell, doc ModeDoc, assetMult float64) (Gap, bool) {
	base := Gap{
		AssetID: asset.ID, Asset: asset.Label, AssetType: asset.Type,
		Area: area, AreaTitle: AreaTitle(area), Item: cell.Item,
		State: cell.State, Attempts: cell.Attempts,
		Priority: cellPriority(asset.Type, area, cell.Attempts, doc, assetMult),
	}
	switch cell.State {
	case db.SrcStateNA, db.SrcStateBudgetStop:
		if strings.TrimSpace(cell.Reason) == "" {
			base.Kind = GapMissingReason
			base.Reason = "终态缺原因"
			return base, true
		}
	case db.SrcStateClear:
		if cell.EvidenceNodeID == 0 {
			// 继承格：证据留在源任务，本任务没有自己的 evidence_node 是正常的。只要
			// 继承来源与说明都在，就不算缺证据；重测会覆盖并清掉 inherited 标记。
			if cell.InheritedFromTaskID > 0 && strings.TrimSpace(cell.Reason) != "" {
				break
			}
			if !doc.AllowReasonOnlyClear {
				base.Kind = GapMissingEvidence
				base.Reason = "tested_clear 缺证据节点（项目未允许只写说明）"
				return base, true
			}
			if strings.TrimSpace(cell.Reason) == "" {
				base.Kind = GapMissingEvidence
				base.Reason = "tested_clear 缺证据节点，也没写说明"
				return base, true
			}
		}
	case db.SrcStateFound:
		if cell.FindingID == 0 {
			base.Kind = GapMissingFinding
			base.Reason = "tested_found 没挂 finding"
			return base, true
		}
	}
	return base, false
}

// cellPriority 是补格优先级：资产价值 × 面权重 ÷ (已尝试次数 + 1)。
// 用整数除法就够了——它只用来排序，不需要精确。
func cellPriority(assetType, area string, attempts int, doc ModeDoc, assetMult float64) int {
	if attempts < 0 {
		attempts = 0
	}
	if assetMult <= 0 {
		assetMult = 1
	}
	roi := doc.AreaROI[strings.TrimSpace(area)]
	if roi <= 0 {
		roi = 1
	}
	if roi < 0.1 {
		roi = 0.1
	}
	if roi > 5 {
		roi = 5
	}
	base := float64(AssetValue(assetType)*AreaWeight(area)) * roi * assetMult
	if base <= 0 {
		return 0
	}
	p := int(math.Round(base / float64(attempts+1)))
	if p < 1 {
		p = 1
	}
	return p
}

// addCheckGap 记一条收尾闸缺口（统一填 Area/权重/计数）。
// reconCheckKeys 是"先清点再打"的前置收尾项。顺序即建议执行顺序。
var reconCheckKeys = []string{"js_enum", "paths_enum"}

// reconMissing returns the reconnaissance checks that are not yet done for this
// asset. Only app/service carry them (see CheckDefs); endpoint/IP/domain assets
// return nil so the planner is never told to "go enumerate JS" on a bare host.
func reconMissing(asset db.SrcAsset, checks map[string]db.SrcCheck, doc ModeDoc) []string {
	if asset.Type != "app" && asset.Type != "service" {
		return nil
	}
	if doc.SkipCheckGate || doc.Tier() == CheckTierOff {
		return nil
	}
	var missing []string
	for _, key := range reconCheckKeys {
		chk, ok := checks[key]
		if !ok || chk.State != db.SrcCheckDone {
			missing = append(missing, key)
			continue
		}
		def, ok := FindCheck(key)
		if !ok {
			continue
		}
		if satisfied, _ := checkSatisfied(def, doc, chk.Metrics); !satisfied {
			missing = append(missing, key)
		}
	}
	return missing
}

func addCheckGap(gaps *[]Gap, rep *Report, asset db.SrcAsset, def CheckDef, kind, state, reason string, evidence int64, assetMult float64) {
	if rep.ChecksByKey == nil {
		rep.ChecksByKey = map[string]int{}
	}
	rep.ChecksByKey[def.Key]++
	priority := checkPriority(asset.Type, def, 0)
	if assetMult > 0 {
		priority = int(math.Round(float64(priority) * assetMult))
	}
	if kind == GapBudgetStopBlocked {
		priority += 5
	}
	g := Gap{
		AssetID: asset.ID, Asset: asset.Label, AssetType: asset.Type,
		Area: ClosureArea, AreaTitle: "收尾闸", Item: def.Key, Kind: kind,
		State: state, Reason: reason, Priority: priority, EvidenceNodeID: evidence,
	}
	*gaps = append(*gaps, g)
}

// fourPieceAllNA 判断一个资产的注入/SSRF/XSS/RCE 是否"全是 N/A"。
// 只在四项都是必做面时才判定——否则"没要求"会被误读成"全 N/A"。
func fourPieceAllNA(areas []string, byArea map[string]map[string]db.SrcCell) bool {
	for _, a := range []string{"injection", "ssrf", "xss", "rce"} {
		if !containsStr(areas, a) {
			return false
		}
	}
	for _, a := range []string{"injection", "ssrf", "xss", "rce"} {
		cell, ok := byArea[a][AreaAll]
		if !ok || cell.State != db.SrcStateNA {
			return false
		}
	}
	return true
}

// focusAsset 选出"当前最该集中打的资产"（技能的一种子闭环：一host挖完再换）。
// 只用于排序与提示，不改变门的判定。
func focusAsset(gaps []Gap) (int64, string) {
	sum := map[int64]int{}
	label := map[int64]string{}
	for _, g := range gaps {
		if g.AssetID <= 0 {
			continue // 交付类缺口没有资产
		}
		sum[g.AssetID] += g.Priority
		if g.Asset != "" {
			label[g.AssetID] = g.Asset
		}
	}
	best, bestSum := int64(0), 0
	for id, s := range sum {
		if s > bestSum || (s == bestSum && (best == 0 || id < best)) {
			best, bestSum = id, s
		}
	}
	return best, label[best]
}

// ValidateCell 在写库前校验一条终态：结构、终态语义、任务内证据归属。
// 返回的错误是给人看的中文句子（会原样回给 agent）。
func ValidateCell(ctx context.Context, store *db.SrcStore, taskID int64, doc ModeDoc, cell db.SrcCell) error {
	if !IsArea(cell.Area) {
		return fmt.Errorf("未知风险面 %q（合法值：%s）", cell.Area, AreaLegend())
	}
	if !ItemAllowed(cell.Area, cell.Item) {
		return fmt.Errorf("风险面 %s 没有细分项 %q；整组终态请用 %q", cell.Area, cell.Item, AreaAll)
	}
	if !db.IsSrcState(cell.State) {
		return fmt.Errorf("未知终态 %q（合法值：tested_found / tested_clear / na / budget_stop）", cell.State)
	}
	switch cell.State {
	case db.SrcStateFound:
		if cell.FindingID <= 0 {
			return fmt.Errorf("tested_found 必须带 finding_id（先 report_finding 再把 id 写进格）")
		}
	case db.SrcStateClear:
		if cell.EvidenceNodeID == 0 {
			if !doc.AllowReasonOnlyClear {
				return fmt.Errorf("tested_clear 必须挂 evidence_node_id（一条 fact 节点）；只有项目规则 allow_reason_only_clear=true 时才允许只写说明")
			}
			if strings.TrimSpace(cell.Reason) == "" {
				return fmt.Errorf("tested_clear 允许只写说明，但说明不能为空")
			}
		}
	case db.SrcStateNA:
		if strings.TrimSpace(cell.Reason) == "" {
			return fmt.Errorf("na 必须写 reason")
		}
		// N/A 不是自由文本：技能的口径是"只回请登录且没有业务字段才可 N/A"，
		// 所以理由必须落在该风险面允许的码里——把"没这个面"和"没测"分开。
		code := strings.TrimSpace(cell.NACode)
		if code == "" {
			return fmt.Errorf("na 必须填 na_code（可选：%s）；填不出来的说明还没测，不能记 N/A",
				strings.Join(AreaNACodes(cell.Area), " / "))
		}
		if !NACodeAllowed(cell.Area, code) {
			return fmt.Errorf("%s 面不接受 na_code=%q（可选：%s）", cell.Area, code,
				strings.Join(AreaNACodes(cell.Area), " / "))
		}
		if code == "platform_exempt" && !doc.PlatformExemptAllowed(cell.Area) {
			return fmt.Errorf("platform_exempt 需要 program 的 exempt_areas 明确列出 %s 面；当前任务快照没有这条豁免", cell.Area)
		}
	case db.SrcStateBudgetStop:
		if strings.TrimSpace(cell.Reason) == "" {
			return fmt.Errorf("budget_stop 必须写 reason")
		}
	}
	if store == nil {
		return nil
	}
	asset, err := store.SrcAssetInScope(ctx, taskID, cell.AssetID)
	if err != nil {
		return err
	}
	if asset == nil {
		return fmt.Errorf("资产 %d 不在本任务测试范围内，不能点格", cell.AssetID)
	}
	required := doc.RequiredFor(asset.Type)
	if len(required) == 0 {
		return fmt.Errorf("资产类型 %s 没有必做面定义，无法判定该格", asset.Type)
	}
	if cell.EvidenceNodeID > 0 {
		ok, err := store.NodeBelongsToTask(ctx, taskID, cell.EvidenceNodeID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("evidence_node_id=%d 不是本任务的节点", cell.EvidenceNodeID)
		}
	}
	if cell.FindingID > 0 {
		ok, err := store.FindingBelongsToTask(ctx, taskID, cell.FindingID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("finding_id=%d 不是本任务的漏洞", cell.FindingID)
		}
	}
	// 同一格的尝试上限：超过就必须以 budget_stop 收口，禁止无限重试。
	prev, err := store.GetSrcCell(ctx, taskID, cell.AssetID, cell.Area, cell.Item)
	if err != nil {
		return err
	}
	if prev != nil && prev.Attempts >= doc.MaxAttemptsPerCell &&
		cell.State != db.SrcStateBudgetStop && cell.State != db.SrcStateNA {
		return fmt.Errorf("该格已尝试 %d 次（上限 %d），请以 budget_stop 收口并写清原因，不要再重试",
			prev.Attempts, doc.MaxAttemptsPerCell)
	}
	return nil
}
