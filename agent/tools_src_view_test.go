package agent

// src_state 的两种视图（B1）：默认摘要不该带逐格行，过滤后不能丢掉任务级判定。
// 这两个函数是纯函数，不需要 PG，所以把"输出形状"钉在单测里。

import (
	"encoding/json"
	"testing"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

func sampleSnap(assets int, cellsPerAsset int) map[string]any {
	matrix := make([]map[string]any, 0, assets)
	checkRows := make([]map[string]any, 0, assets)
	cells := make([]db.SrcCell, 0, assets*cellsPerAsset)
	gaps := make([]srcmode.Gap, 0, 30)
	for a := 1; a <= assets; a++ {
		rows := make([]map[string]any, 0, cellsPerAsset)
		for i := 0; i < cellsPerAsset; i++ {
			area := "area" + string(rune('a'+i%5))
			rows = append(rows, map[string]any{
				"area": area, "item": "*", "title": area,
				"state": cellStateFor(i),
			})
			st := ""
			if i%2 == 0 {
				st = db.SrcStateClear
			}
			cells = append(cells, db.SrcCell{AssetID: int64(a), Area: area, Item: "*", State: st})
		}
		matrix = append(matrix, map[string]any{
			"asset_id": int64(a), "asset": "a.example", "asset_type": "app",
			"required_areas": []string{"authz"}, "cells": rows,
		})
		checkRows = append(checkRows, map[string]any{
			"asset_id": int64(a), "asset": "a.example", "asset_type": "app",
			"items": []map[string]any{
				{"key": "js_enum", "title": "JS 抽全", "state": "done"},
				{"key": "paths_enum", "title": "路径枚举"},
			},
		})
	}
	for i := 0; i < 30; i++ {
		gaps = append(gaps, srcmode.Gap{AssetID: int64(i%assets + 1), Area: "authz", Kind: srcmode.GapBlank, Priority: 100 - i})
	}
	return map[string]any{
		"mode":       srcmode.ModeSrc,
		"summary":    map[string]any{"gaps_total": 30, "checks_total": assets * 2},
		"finishable": false,
		"doc":        map[string]any{"check_tier": "full"},
		"legend":     "authz=越权",
		"gaps":       gaps,
		"gaps_total": 30,
		"cells":      cells,
		"assets":     matrix,
		"checks":     checkRows,
	}
}

func TestSummaryViewDropsPerCellRows(t *testing.T) {
	snap := sampleSnap(3, 10)
	out := summaryView(snap)

	if _, ok := out["cells"]; ok {
		t.Fatal("摘要视图不该带逐格 cells")
	}
	// 任务级判定必须保留：这是"能不能收"的依据。
	for _, key := range []string{"summary", "finishable", "doc", "legend"} {
		if _, ok := out[key]; !ok {
			t.Fatalf("摘要视图丢了任务级字段 %q", key)
		}
	}
	assets, ok := out["assets"].([]map[string]any)
	if !ok || len(assets) != 3 {
		t.Fatalf("assets 摘要不对：%+v", out["assets"])
	}
	first := assets[0]
	if first["cells_total"] != 10 || first["cells_done"] != 5 {
		t.Fatalf("资产进度不对：%+v", first)
	}
	checks, ok := out["checks"].([]map[string]any)
	if !ok || len(checks) != 3 {
		t.Fatalf("checks 摘要不对：%+v", out["checks"])
	}
	missing, _ := checks[0]["missing"].([]string)
	if len(missing) != 1 || missing[0] != "paths_enum" {
		t.Fatalf("未做项列表不对：%+v", checks[0])
	}
	gaps, _ := out["gaps"].([]srcmode.Gap)
	if len(gaps) != maxSummaryGaps {
		t.Fatalf("摘要 gaps 应截断到 %d，got %d", maxSummaryGaps, len(gaps))
	}
	if out["gaps_truncated"] != true {
		t.Fatal("截断时应标记 gaps_truncated")
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 8*1024 {
		t.Fatalf("3×10 任务的摘要过大：%d 字节", len(raw))
	}
}

func TestFilterSnapshotKeepsTaskVerdict(t *testing.T) {
	snap := sampleSnap(3, 6)
	out := filterSnapshot(snap, 2, "")

	if out["finishable"] != snap["finishable"] || out["summary"] == nil || out["doc"] == nil {
		t.Fatal("过滤丢了任务级判定（summary/finishable/doc 必须保留）")
	}
	cells, ok := out["cells"].([]db.SrcCell)
	if !ok {
		t.Fatalf("cells 类型变了：%T", out["cells"])
	}
	for _, c := range cells {
		if c.AssetID != 2 {
			t.Fatalf("过滤后混入其它资产的格：%+v", c)
		}
	}
	assets, _ := out["assets"].([]map[string]any)
	if len(assets) != 1 || assets[0]["asset_id"] != int64(2) {
		t.Fatalf("assets 过滤不对：%+v", out["assets"])
	}
	checks, _ := out["checks"].([]map[string]any)
	if len(checks) != 1 || checks[0]["asset_id"] != int64(2) {
		t.Fatalf("checks 过滤不对：%+v", out["checks"])
	}
	gaps, _ := out["gaps"].([]srcmode.Gap)
	for _, g := range gaps {
		if g.AssetID != 2 {
			t.Fatalf("gaps 过滤后混入其它资产：%+v", g)
		}
	}
	if out["gaps_total"] != len(gaps) {
		t.Fatalf("gaps_total 应与过滤后一致：%v vs %d", out["gaps_total"], len(gaps))
	}
}

func TestFilterSnapshotByArea(t *testing.T) {
	snap := sampleSnap(2, 10)
	out := filterSnapshot(snap, 1, "areaa")
	cells, _ := out["cells"].([]db.SrcCell)
	if len(cells) == 0 {
		t.Fatal("按面过滤后不该为空")
	}
	for _, c := range cells {
		if c.AssetID != 1 || c.Area != "areaa" {
			t.Fatalf("按面过滤不干净：%+v", c)
		}
	}
}

// cellStateFor 让偶数索引的格有终态（nil → 摘要里算"未做"）。
func cellStateFor(i int) any {
	if i%2 == 0 {
		return db.SrcStateClear
	}
	return nil
}
