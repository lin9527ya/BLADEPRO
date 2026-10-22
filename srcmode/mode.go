// Package srcmode 实现"SRC 挖掘"这一任务模式（tasks.mode='src'）。
//
// 设计取舍（见 docs/SRC挖掘agent改造方案.md）：不做 fork、不做通用插件内核，
// 而是把 SRC 当成一套**运行配置**——规划层按"补格"派意图、判定层用显式覆盖格
// 做收尾硬门、交付层出可提交件。本包只负责其中的"模式定义 + 判定"两件事，
// 刻意不 import server（避免循环依赖）：任务字段由调用方传入。
package srcmode

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ModeSrc 是 SRC 挖掘的模式标识；tasks.mode=” 表示普通渗透（行为与改造前一致）。
const ModeSrc = "src"

// 预算默认值与硬上限。上限存在的意义是防止一条 mode_doc 把任务变成无限循环。
const (
	DefaultMaxAttemptsPerCell = 2
	DefaultMaxIntentsPerAsset = 12
	maxAttemptsLimit          = 10
	maxIntentsPerAssetLimit   = 500
	DocVersion                = 1
)

// ModeDoc 是任务的模式快照（tasks.mode_doc）。创建时解析、运行中只读——
// 与 ExplorationPolicy 同一套快照语义：改全局默认不影响已建任务。
type ModeDoc struct {
	DocVersion int    `json:"doc_version"`
	Mode       string `json:"mode"`
	// Platform / Program 只是提交台账与报告备注用的标签（P2 起真正使用）。
	Platform string `json:"platform,omitempty"`
	Program  string `json:"program,omitempty"`
	// ProgramNote 是创建任务时按平台项目生成的一行规则说明（P3）：平台、限额、
	// 时间窗、范围说明。它进提示词，让 agent 从一开始就知道这套边界。
	ProgramNote string `json:"program_note,omitempty"`
	// RequiredAreas 覆盖"资产类型 → 必做风险面"的默认映射；只允许收窄/新增已知类型。
	RequiredAreas map[string][]string `json:"required_areas,omitempty"`
	// AllowBudgetStop=true 时 budget_stop 也算终态（放行收尾），但报告备注必须写明缺口。
	AllowBudgetStop bool `json:"allow_budget_stop"`
	// MaxAttemptsPerCell 是同一格的尝试上限；超过必须 budget_stop 并写原因。
	MaxAttemptsPerCell int `json:"max_attempts_per_cell"`
	// MaxIntentsPerAsset 防止单个资产吃掉整任务的意图预算。
	MaxIntentsPerAsset int `json:"max_intents_per_asset"`
	// HardItems 是"必须逐个点亮"的细分项（如 paths/enum-200）；不写则每个面只需 '*'。
	HardItems []string `json:"hard_items,omitempty"`
	// SkipReportGate 关掉"medium+ 发现必须齐备可提交件"这条收尾门（默认**要求**）。
	// 只给"先跑覆盖、交付另开任务"的用法留出口：关掉后任务可以在报告没写好时收尾。
	SkipReportGate bool `json:"skip_report_gate,omitempty"`
}

// DefaultDoc 返回 SRC 模式的默认快照。
func DefaultDoc() ModeDoc {
	return ModeDoc{
		DocVersion:         DocVersion,
		Mode:               ModeSrc,
		AllowBudgetStop:    false,
		MaxAttemptsPerCell: DefaultMaxAttemptsPerCell,
		MaxIntentsPerAsset: DefaultMaxIntentsPerAsset,
	}
}

// Resolve 校验并解析一份模式文档，返回要落库的快照（JSON）。mode="" 表示普通
// 渗透，返回 nil 快照且不报错；未知 mode、未知 area、越界预算一律拒绝——模式是
// 创建时定死的，宁可在创建页报错，也不要在运行到一半才发现配置写错。
func Resolve(mode string, raw json.RawMessage) (json.RawMessage, ModeDoc, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return nil, ModeDoc{}, nil
	}
	if mode != ModeSrc {
		return nil, ModeDoc{}, fmt.Errorf("未知任务模式：%s", mode)
	}
	doc := DefaultDoc()
	if len(strings.TrimSpace(string(raw))) > 0 && string(raw) != "null" {
		// 未知字段忽略：模式文档会随版本增加字段，旧客户端传来的多余键不该让创建失败。
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, ModeDoc{}, fmt.Errorf("模式配置解析失败：%w", err)
		}
	}
	doc.Mode = ModeSrc
	doc.DocVersion = DocVersion
	if doc.MaxAttemptsPerCell <= 0 {
		doc.MaxAttemptsPerCell = DefaultMaxAttemptsPerCell
	}
	if doc.MaxIntentsPerAsset <= 0 {
		doc.MaxIntentsPerAsset = DefaultMaxIntentsPerAsset
	}
	if doc.MaxAttemptsPerCell > maxAttemptsLimit {
		return nil, ModeDoc{}, fmt.Errorf("max_attempts_per_cell 不能超过 %d", maxAttemptsLimit)
	}
	if doc.MaxIntentsPerAsset > maxIntentsPerAssetLimit {
		return nil, ModeDoc{}, fmt.Errorf("max_intents_per_asset 不能超过 %d", maxIntentsPerAssetLimit)
	}
	for assetType, areas := range doc.RequiredAreas {
		if !IsAssetType(assetType) {
			return nil, ModeDoc{}, fmt.Errorf("未知资产类型：%s", assetType)
		}
		for _, area := range areas {
			if !IsArea(area) {
				return nil, ModeDoc{}, fmt.Errorf("未知风险面：%s（资产类型 %s）", area, assetType)
			}
		}
		if len(areas) == 0 {
			return nil, ModeDoc{}, fmt.Errorf("资产类型 %s 的必做面不能为空", assetType)
		}
	}
	for _, item := range doc.HardItems {
		if _, _, err := SplitItem(item); err != nil {
			return nil, ModeDoc{}, err
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, ModeDoc{}, err
	}
	return out, doc, nil
}

// Parse 读取一份已落库的模式快照。解析失败或为空时返回零值 ModeDoc——调用方用
// doc.IsSrc() 判断，坏快照只会退化成"普通渗透"，不会让任务跑不起来。
func Parse(raw json.RawMessage) ModeDoc {
	doc := DefaultDoc()
	if len(strings.TrimSpace(string(raw))) == 0 || string(raw) == "null" {
		return doc
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return DefaultDoc()
	}
	if doc.MaxAttemptsPerCell <= 0 {
		doc.MaxAttemptsPerCell = DefaultMaxAttemptsPerCell
	}
	if doc.MaxIntentsPerAsset <= 0 {
		doc.MaxIntentsPerAsset = DefaultMaxIntentsPerAsset
	}
	return doc
}

// IsSrc reports whether this snapshot describes the SRC mode.
func (d ModeDoc) IsSrc() bool { return d.Mode == ModeSrc }

// RequiredFor 返回某资产类型的必做风险面（mode_doc 覆盖优先，否则用 taxonomy 默认）。
func (d ModeDoc) RequiredFor(assetType string) []string {
	if areas, ok := d.RequiredAreas[assetType]; ok && len(areas) > 0 {
		return areas
	}
	return DefaultRequiredAreas(assetType)
}

// HardItemsFor 返回某风险面下必须逐个点亮的细分项（可能为空）。
func (d ModeDoc) HardItemsFor(area string) []string {
	var out []string
	for _, item := range d.HardItems {
		a, i, err := SplitItem(item)
		if err != nil || a != area {
			continue
		}
		out = append(out, i)
	}
	sort.Strings(out)
	return out
}

// SplitItem 拆分 "area/item" 形式的硬性细分项。
func SplitItem(raw string) (area, item string, err error) {
	raw = strings.TrimSpace(raw)
	area, item, ok := strings.Cut(raw, "/")
	area, item = strings.TrimSpace(area), strings.TrimSpace(item)
	if !ok || area == "" || item == "" {
		return "", "", fmt.Errorf("hard_items 项必须写成 area/item：%q", raw)
	}
	if !IsArea(area) {
		return "", "", fmt.Errorf("未知风险面：%s", area)
	}
	if !ItemAllowed(area, item) {
		return "", "", fmt.Errorf("风险面 %s 没有 %s 这个细分项", area, item)
	}
	return area, item, nil
}
