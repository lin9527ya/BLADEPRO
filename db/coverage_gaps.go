package db

import (
	"encoding/json"
)

// DefaultDepthAreas 是「一个资产算测过」的最低风险类数。
//
// 取 3 的依据与 BLADE 一致：只终结 1 类通常只是"打了个照面"，3 类是"真的按面测过"
// 与浅测之间的分界，又不至于要求每个子域都跑满整个词表。
const DefaultDepthAreas = 3

// UntouchedScopeAssets 返回本任务（含直接源任务）范围内、但一条意图都没挂过的
// 具体资产。用于穷尽前置门：还有资产没派过意图时，那不是「无路可走」。
//
// 只数 subdomain / app / ip：endpoint 与 service 是挂在主机下的属性，逐个要求派
// 意图会把分母放大到没有意义。total 是真实总数，items 是前 limit 条样本。
func (s *AssetStore) UntouchedScopeAssets(taskID int64, limit int) ([]CoverageAsset, int, error) {
	if s == nil || s.db == nil || taskID <= 0 {
		return nil, 0, nil
	}
	if limit <= 0 {
		limit = 8
	}
	const cte = `
WITH ` + directTaskContextCTE + scopeAssetIDsCTE + `,
touched AS (
  SELECT DISTINCT ea.asset_id
  FROM exploration_anchors ea
  JOIN exploration_nodes en ON en.id=ea.node_id AND en.kind='intent'
  JOIN context_tasks ctx ON ctx.exploration_id=en.exploration_id
)`
	label := `COALESCE(NULLIF(a.url,''), NULLIF(a.domain,''), NULLIF(a.ip,''), NULLIF(a.app_name,''), a.type)`
	var total int
	if err := s.db.QueryRow(cte+`
SELECT count(*) FROM assets a
JOIN scope_asset_ids sc ON sc.id=a.id
WHERE a.type IN ('subdomain','app','ip')
  AND NOT EXISTS (SELECT 1 FROM touched t WHERE t.asset_id=a.id)`, taskID).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}
	rows, err := s.db.Query(cte+`
SELECT a.id, a.type, `+label+`
FROM assets a
JOIN scope_asset_ids sc ON sc.id=a.id
WHERE a.type IN ('subdomain','app','ip')
  AND NOT EXISTS (SELECT 1 FROM touched t WHERE t.asset_id=a.id)
ORDER BY a.id LIMIT $2`, taskID, limit)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	out := make([]CoverageAsset, 0, limit)
	for rows.Next() {
		var it CoverageAsset
		if err := rows.Scan(&it.ID, &it.Type, &it.Label); err != nil {
			return nil, total, err
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// AssetDepthGap 是一个「碰过但只终结了很少几类风险」的资产。
type AssetDepthGap struct {
	AssetID   int64    `json:"asset_id"`
	Type      string   `json:"type"`
	Label     string   `json:"label"`
	DoneAreas []string `json:"done_areas"`
	Missing   []string `json:"missing"`
}

// DepthGaps 返回本任务范围内终结风险类数 < minAreas 的资产。只统计具体资产
// （subdomain/app/endpoint/ip）；root_domain 与 service 不计（聚合/属性，不是测试面）。
//
// 「终结」取 done / negative / out-of-scope 的意图：out-of-scope 是显式判定
// （越界/不适用），不是没碰过，否则被整体判出去的资产会永远显示深度不足。
func (s *ExplorationStore) DepthGaps(taskID int64, minAreas, limit int) ([]AssetDepthGap, int, error) {
	if s == nil || s.db == nil || s.expID <= 0 || taskID <= 0 {
		return nil, 0, nil
	}
	if minAreas <= 0 {
		minAreas = DefaultDepthAreas
	}
	if limit <= 0 {
		limit = 8
	}
	const cte = `
WITH ` + directTaskContextCTE + scopeAssetIDsCTE + `,
terminal AS (
  SELECT DISTINCT ea.asset_id, NULLIF(en.payload->>'risk_area','') AS area
  FROM exploration_anchors ea
  JOIN exploration_nodes en ON en.id=ea.node_id AND en.kind='intent'
  JOIN context_tasks ctx ON ctx.exploration_id=en.exploration_id
  WHERE en.state IN ('done','negative','out-of-scope')
    AND NULLIF(en.payload->>'risk_area','') = ANY($4::text[])
)`
	label := `COALESCE(NULLIF(a.url,''), NULLIF(a.domain,''), NULLIF(a.ip,''), NULLIF(a.app_name,''), a.type)`
	rows, err := s.db.Query(cte+`
SELECT a.id, a.type, `+label+`,
       COALESCE(array_to_json(array_agg(DISTINCT t.area) FILTER (WHERE t.area IS NOT NULL))::text, '[]') AS done_areas,
       count(*) OVER() AS total
FROM assets a
JOIN scope_asset_ids sc ON sc.id=a.id
LEFT JOIN terminal t ON t.asset_id=a.id
WHERE a.type IN ('subdomain','app','endpoint','ip')
GROUP BY a.id, a.type, a.url, a.domain, a.ip, a.app_name
HAVING count(DISTINCT t.area) < $2
ORDER BY a.id LIMIT $3`, taskID, minAreas, limit, RiskAreas)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]AssetDepthGap, 0, limit)
	total := 0
	for rows.Next() {
		var g AssetDepthGap
		var doneRaw string
		if err := rows.Scan(&g.AssetID, &g.Type, &g.Label, &doneRaw, &total); err != nil {
			return nil, total, err
		}
		_ = json.Unmarshal([]byte(doneRaw), &g.DoneAreas)
		seen := make(map[string]bool, len(g.DoneAreas))
		for _, a := range g.DoneAreas {
			seen[a] = true
		}
		for _, a := range RiskAreas {
			if !seen[a] {
				g.Missing = append(g.Missing, a)
			}
		}
		out = append(out, g)
	}
	return out, total, rows.Err()
}
