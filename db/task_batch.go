package db

// 批量扇出的资产解析：把「关联企业 / 来源任务 / 显式资产 id」并成一份去重清单，
// 供上层按 host/根域分片。分片本身不在这里做——那是业务策略，DB 只负责把资产取全。

import (
	"context"
	"fmt"
)

// BatchAsset is the minimal asset shape shard planning needs.
type BatchAsset struct {
	ID         int64  `json:"id"`
	Type       string `json:"type"`
	Domain     string `json:"domain,omitempty"`
	URL        string `json:"url,omitempty"`
	IP         string `json:"ip,omitempty"`
	AppName    string `json:"app_name,omitempty"`
	RootDomain string `json:"root_domain,omitempty"`
	CompanyID  int64  `json:"company_id,omitempty"`
}

// ResolveBatchAssets returns the union of assets selected by company scope, source
// task links and explicit ids. types (optional) narrows by asset type. The limit is
// enforced in SQL; truncated=true means there were more rows than limit.
func (d *DB) ResolveBatchAssets(ctx context.Context, companyIDs, sourceTaskIDs, assetIDs []int64, types []string, limit int) ([]BatchAsset, bool, error) {
	if d == nil {
		return nil, false, fmt.Errorf("database unavailable")
	}
	if len(companyIDs) == 0 && len(sourceTaskIDs) == 0 && len(assetIDs) == 0 {
		return []BatchAsset{}, false, nil
	}
	if limit <= 0 || limit > 50000 {
		limit = 50000
	}
	if companyIDs == nil {
		companyIDs = []int64{}
	}
	if sourceTaskIDs == nil {
		sourceTaskIDs = []int64{}
	}
	if assetIDs == nil {
		assetIDs = []int64{}
	}
	if types == nil {
		types = []string{}
	}
	rows, err := d.QueryContext(ctx, `
SELECT DISTINCT a.id, a.type,
       COALESCE(a.domain,''), COALESCE(a.url,''), COALESCE(a.ip,''),
       COALESCE(a.app_name,''), COALESCE(a.root_domain,''), COALESCE(a.company_id,0)
FROM assets a
WHERE (
      a.id = ANY($1::bigint[])
      OR a.company_id = ANY($2::bigint[])
      OR EXISTS (
          SELECT 1 FROM task_asset_links l
          WHERE l.task_id = ANY($3::bigint[]) AND l.asset_id = a.id
      )
  )
  AND (cardinality($4::text[]) = 0 OR a.type = ANY($4::text[]))
ORDER BY a.id
LIMIT $5`, assetIDs, companyIDs, sourceTaskIDs, types, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []BatchAsset{}
	for rows.Next() {
		var a BatchAsset
		if err := rows.Scan(&a.ID, &a.Type, &a.Domain, &a.URL, &a.IP, &a.AppName, &a.RootDomain, &a.CompanyID); err != nil {
			return nil, false, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(out) > limit
	if truncated {
		out = out[:limit]
	}
	return out, truncated, nil
}

// FilterBatchAssets applies the "only new / only untested" selectors:
//   - onlyNew: asset is not linked to any existing SRC task (task_asset_links);
//   - onlyUntested: asset has no terminal cell in any existing SRC task.
//
// Both are coarse, cross-task filters by design: a task-level area budget cannot
// be known before the new task's scope exists. Returns filtered assets in the
// original order and the number of skipped assets.
func (d *DB) FilterBatchAssets(ctx context.Context, assets []BatchAsset, onlyNew, onlyUntested bool) ([]BatchAsset, int, error) {
	if d == nil || len(assets) == 0 {
		return assets, 0, nil
	}
	if !onlyNew && !onlyUntested {
		return assets, 0, nil
	}
	ids := make([]int64, 0, len(assets))
	for _, a := range assets {
		ids = append(ids, a.ID)
	}
	rows, err := d.QueryContext(ctx, `
SELECT a.id
FROM assets a
WHERE a.id = ANY($1::bigint[])
  AND ($2::boolean = false OR NOT EXISTS (
      SELECT 1 FROM task_asset_links l JOIN tasks t ON t.id=l.task_id
      WHERE l.asset_id=a.id AND t.mode='src' AND t.deleted_at IS NULL))
  AND ($3::boolean = false OR NOT EXISTS (
      SELECT 1 FROM src_cells c JOIN tasks t ON t.id=c.task_id
      WHERE c.asset_id=a.id AND t.mode='src' AND t.deleted_at IS NULL))`, ids, onlyNew, onlyUntested)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	keep := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, 0, err
		}
		keep[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	out := make([]BatchAsset, 0, len(assets))
	for _, a := range assets {
		if keep[a.ID] {
			out = append(out, a)
		}
	}
	return out, len(assets) - len(out), nil
}
