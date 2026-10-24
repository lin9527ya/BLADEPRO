package server

// 批量任务扇出：把一个范围按 host / 根域名 / 数量切成互不重叠的分片，每片建一个
// 任务并归入同一 campaign。分片范围用精确的 subdomain/root_domain/ip scope 表达，
// 不再把整个企业范围灌进每个任务（那会让 500 个子域仍然挤在一个任务里）。
//
// 并发仍由全局任务并发上限与每任务 worker 数控制；本接口只负责"切得开、建得快、
// 能统一看"。

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lin9527ya/bladepro/db"
)

const (
	batchDefaultAssetsPerTask = 20
	batchDefaultMaxShards     = 100
	batchMaxShards            = 500
	batchDefaultMaxAssets     = 5000
	batchMaxAssets            = 50000
)

type createTaskBatchReq struct {
	createTaskReq
	NamePrefix    string   `json:"name_prefix,omitempty"`
	AssetIDs      []int64  `json:"asset_ids,omitempty"`
	AssetTypes    []string `json:"asset_types,omitempty"`
	ShardBy       string   `json:"shard_by,omitempty"` // host（默认）| root_domain | count
	AssetsPerTask int      `json:"assets_per_task,omitempty"`
	MaxShards     int      `json:"max_shards,omitempty"`
	MaxAssets     int      `json:"max_assets,omitempty"`
	CampaignName  string   `json:"campaign_name,omitempty"`
	// CampaignTokenBudget 是战役级 token 上限（0=不限）：跨分片累计，达到后
	// 各分片任务在其 deadline coordinator 里收尾。与每个分片的 token_budget 不同。
	CampaignTokenBudget int64 `json:"campaign_token_budget,omitempty"`
	DryRun              bool  `json:"dry_run,omitempty"`
	OnlyNewAssets       bool  `json:"only_new_assets,omitempty"`
	OnlyUntestedAssets  bool  `json:"only_untested_assets,omitempty"`
}

type batchShardPlan struct {
	Assets []db.BatchAsset
	Scope  []db.TaskScopeInput
	Hosts  []string
}

type batchShardResult struct {
	Index  int      `json:"index"`
	TaskID string   `json:"task_id,omitempty"`
	Name   string   `json:"name"`
	Assets int      `json:"assets"`
	Hosts  []string `json:"hosts,omitempty"`
	Status string   `json:"status"`
	Error  string   `json:"error,omitempty"`
}

func (s *Server) createTaskBatch(w http.ResponseWriter, r *http.Request) {
	var req createTaskBatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	if perr := normalizeBatchReq(&req); perr != nil {
		writeErr(w, perr.code, perr.msg)
		return
	}
	prep, assets, plans, skipped, perr := s.prepareBatch(r.Context(), req)
	if perr != nil {
		writeErr(w, perr.code, perr.msg)
		return
	}
	if req.DryRun {
		base := batchBaseName(req, prep)
		shards := make([]batchShardResult, 0, len(plans))
		for i, p := range plans {
			shards = append(shards, batchShardResult{
				Index: i, Name: batchShardName(base, i, len(plans)), Assets: len(p.Assets), Hosts: p.Hosts, Status: "planned",
			})
		}
		writeJSON(w, 200, map[string]any{
			"dry_run": true, "shards": shards,
			"totals": map[string]any{"assets": len(assets), "shards": len(plans), "skipped": skipped},
		})
		return
	}
	campaign, results, created, err := s.persistBatch(r.Context(), req, prep, plans, len(assets), skipped)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	status := http.StatusCreated
	if created == 0 {
		status = http.StatusInternalServerError
	}
	resp := map[string]any{
		"campaign": campaign,
		"shards":   results,
		"totals": map[string]any{
			"assets": len(assets), "shards": len(plans), "created": created,
			"failed": len(plans) - created, "skipped": skipped,
		},
	}
	writeJSON(w, status, resp)
}

// normalizeBatchReq applies shard defaults and the selector guard. Shared by the
// HTTP handler and schedule-triggered batch fan-out.
func normalizeBatchReq(req *createTaskBatchReq) *taskCreateErr {
	req.ShardBy = strings.ToLower(strings.TrimSpace(req.ShardBy))
	if req.ShardBy == "" {
		req.ShardBy = "host"
	}
	switch req.ShardBy {
	case "host", "root_domain":
	default:
		return taskErr(400, "shard_by 只能是 host / root_domain")
	}
	if req.AssetsPerTask <= 0 {
		req.AssetsPerTask = batchDefaultAssetsPerTask
	}
	if req.AssetsPerTask > 500 {
		req.AssetsPerTask = 500
	}
	if req.MaxShards <= 0 {
		req.MaxShards = batchDefaultMaxShards
	}
	if req.MaxShards > batchMaxShards {
		req.MaxShards = batchMaxShards
	}
	if req.MaxAssets <= 0 {
		req.MaxAssets = batchDefaultMaxAssets
	}
	if req.MaxAssets > batchMaxAssets {
		req.MaxAssets = batchMaxAssets
	}
	if len(req.CompanyIDs) == 0 && len(req.SourceTaskIDs) == 0 && len(req.AssetIDs) == 0 {
		return taskErr(400, "批量扇出至少需要 company_ids / source_task_ids / asset_ids 之一来确定资产范围")
	}
	return nil
}

// prepareBatch runs the shared single-task pipeline once, then resolves/filters
// assets and plans shards. Used by both HTTP batch creation and schedules.
func (s *Server) prepareBatch(ctx context.Context, req createTaskBatchReq) (*preparedTaskCreate, []db.BatchAsset, []batchShardPlan, int, *taskCreateErr) {
	prep, perr := s.prepareTaskCreate(ctx, req.createTaskReq)
	if perr != nil {
		return nil, nil, nil, 0, perr
	}
	assets, truncated, err := s.m.PG().ResolveBatchAssets(ctx, prep.companyIDs, prep.sourceIDs, req.AssetIDs, req.AssetTypes, req.MaxAssets)
	if err != nil {
		return nil, nil, nil, 0, taskErr(500, err.Error())
	}
	if truncated {
		return nil, nil, nil, 0, taskErr(400, fmt.Sprintf("范围内资产超过上限 %d：请缩窄范围或提高 max_assets", req.MaxAssets))
	}
	if len(assets) == 0 {
		return nil, nil, nil, 0, taskErr(400, "所选范围没有资产（检查 company_ids / source_task_ids / asset_ids 与 asset_types）")
	}
	assets, skipped, err := s.m.PG().FilterBatchAssets(ctx, assets, req.OnlyNewAssets, req.OnlyUntestedAssets)
	if err != nil {
		return nil, nil, nil, 0, taskErr(500, err.Error())
	}
	if len(assets) == 0 {
		return nil, nil, nil, skipped, taskErr(400, fmt.Sprintf("按 only_new/only_untested 过滤后没有资产（跳过 %d 个）", skipped))
	}
	plans, perr := planBatchShards(assets, req.ShardBy, req.AssetsPerTask, req.MaxShards)
	if perr != nil {
		return nil, nil, nil, skipped, perr
	}
	return prep, assets, plans, skipped, nil
}

// persistBatch creates the campaign and every shard. It returns the results even
// when some shards fail; created==0 means the campaign was removed.
func (s *Server) persistBatch(ctx context.Context, req createTaskBatchReq, prep *preparedTaskCreate, plans []batchShardPlan, totalAssets, skipped int) (*db.Campaign, []batchShardResult, int, error) {
	campaign, err := s.createBatchCampaign(ctx, req, prep, len(plans), totalAssets)
	if err != nil {
		return nil, nil, 0, err
	}
	base := batchBaseName(req, prep)
	results := make([]batchShardResult, 0, len(plans))
	created := 0
	var firstErr error
	for i, p := range plans {
		name := batchShardName(base, i, len(plans))
		desc := batchShardDesc(prep, i, len(plans), p)
		shard := &taskShardSpec{
			Name: name, Description: desc, Goal: prep.req.Goal,
			TokenBudget: prep.req.TokenBudget, ScopeRows: p.Scope, AssetIDs: assetIDs(p.Assets),
			Priority:       autoShardPriority(p.Assets, campaign.Priority, prep.req.Priority),
			OmitCompanyIDs: true,
		}
		t, perr := s.persistPreparedTask(ctx, prep, shard)
		if perr != nil {
			results = append(results, batchShardResult{Index: i, Name: name, Assets: len(p.Assets), Hosts: p.Hosts, Status: "failed", Error: perr.msg})
			if firstErr == nil {
				firstErr = fmt.Errorf("分片 %d 创建失败：%s", i+1, perr.msg)
			}
			continue
		}
		if err := s.m.PG().SetTaskCampaign(parseIntDefault(t.ID), &campaign.ID); err != nil {
			log.Printf("[task-batch] task %s 归入 campaign %d 失败: %v", t.ID, campaign.ID, err)
		}
		s.launchTask(t, desc+" "+prep.req.Goal, prep.req.SeedFirstIntent != nil && *prep.req.SeedFirstIntent)
		results = append(results, batchShardResult{Index: i, TaskID: t.ID, Name: name, Assets: len(p.Assets), Hosts: p.Hosts, Status: "created"})
		created++
	}
	if created == 0 {
		_, _ = s.m.PG().DeleteCampaign(campaign.ID)
		return campaign, results, 0, firstErr
	}
	return campaign, results, created, nil
}

func batchBaseName(req createTaskBatchReq, prep *preparedTaskCreate) string {
	base := strings.TrimSpace(req.NamePrefix)
	if base == "" {
		base = truncateRunes(strings.TrimSpace(prep.req.Name), 32)
	}
	if base == "" {
		base = truncateRunes(strings.TrimSpace(prep.req.Description), 32)
	}
	if base == "" {
		base = "批量扇出"
	}
	return base
}

func batchShardName(base string, i, total int) string {
	return fmt.Sprintf("%s · 分片 %d/%d", base, i+1, total)
}

func batchShardDesc(prep *preparedTaskCreate, i, total int, p batchShardPlan) string {
	hosts := p.Hosts
	if len(hosts) > 8 {
		hosts = append(append([]string{}, hosts[:8]...), fmt.Sprintf("…等 %d 个", len(p.Hosts)))
	}
	return fmt.Sprintf("%s\n\n【批量扇出 · 分片 %d/%d】资产 %d 个；主机：%s",
		prep.req.Description, i+1, total, len(p.Assets), strings.Join(hosts, ", "))
}

// createBatchCampaign groups the shards under one campaign so the result can be
// reviewed/archived as a unit. Auto-generated names are timestamped (campaign names
// are unique); an explicit duplicate name is rejected instead of silently reusing.
func (s *Server) createBatchCampaign(ctx context.Context, req createTaskBatchReq, prep *preparedTaskCreate, shards, assets int) (*db.Campaign, error) {
	name := strings.TrimSpace(req.CampaignName)
	if name == "" {
		prefix := truncateRunes(strings.TrimSpace(prep.req.Description), 24)
		name = fmt.Sprintf("批量扇出 %s · %s", time.Now().Format("20060102-150405"), prefix)
	}
	var companyID *int64
	if len(prep.companyIDs) == 1 {
		id := prep.companyIDs[0]
		companyID = &id
	}
	note := fmt.Sprintf("批量扇出：%d 个分片 / %d 个资产；shard_by=%s", shards, assets, req.ShardBy)
	c, err := s.m.PG().CreateCampaign(name, note, companyID, req.Priority, req.CampaignTokenBudget)
	if err != nil {
		return nil, fmt.Errorf("创建 campaign 失败：%w", err)
	}
	return c, nil
}

// autoShardPriority derives a queue priority for one shard when the caller did
// not set an explicit priority. Explicit priority always wins; otherwise the
// campaign priority plus an asset-tier score decides who starts first when the
// concurrency limit queues work. The score is deliberately simple and auditable:
// primary +12, api/admin +8, mobile +4, third-party -3.
func autoShardPriority(assets []db.BatchAsset, campaignPriority, explicit int) int {
	if explicit != 0 {
		return explicit
	}
	score := campaignPriority
	for _, a := range assets {
		switch a.Tier {
		case "primary":
			score += 12
		case "api", "admin":
			score += 8
		case "mobile":
			score += 4
		case "third_party":
			score -= 3
		}
	}
	if score < -1000 {
		score = -1000
	}
	if score > 1000 {
		score = 1000
	}
	return score
}

// planBatchShards groups assets into non-overlapping host/root-domain shards.
// assets_per_task is a soft cap: a single host group is never split, because a
// task scope is host-based and splitting one host across tasks would make the
// shards overlap.
func planBatchShards(assets []db.BatchAsset, shardBy string, assetsPerTask, maxShards int) ([]batchShardPlan, *taskCreateErr) {
	if len(assets) == 0 {
		return nil, taskErr(400, "没有可分片的资产")
	}
	sorted := append([]db.BatchAsset(nil), assets...)
	sort.Slice(sorted, func(i, j int) bool {
		hi, hj := batchAssetHost(sorted[i]), batchAssetHost(sorted[j])
		if hi != hj {
			return hi < hj
		}
		return sorted[i].ID < sorted[j].ID
	})

	var plans []batchShardPlan
	{
		groups := map[string][]db.BatchAsset{}
		order := []string{}
		for _, a := range sorted {
			key := batchAssetHost(a)
			if shardBy == "root_domain" {
				key = strings.ToLower(strings.TrimSpace(a.RootDomain))
				if key == "" {
					key = batchAssetHost(a)
				}
			}
			if _, ok := groups[key]; !ok {
				order = append(order, key)
			}
			groups[key] = append(groups[key], a)
		}
		sort.Strings(order)
		var cur []db.BatchAsset
		for _, key := range order {
			group := groups[key]
			if len(cur) > 0 && len(cur)+len(group) > assetsPerTask {
				plans = append(plans, buildBatchShard(cur))
				cur = nil
			}
			cur = append(cur, group...)
		}
		if len(cur) > 0 {
			plans = append(plans, buildBatchShard(cur))
		}
	}
	if len(plans) > maxShards {
		return nil, taskErr(400, fmt.Sprintf("分片数 %d 超过上限 %d：增大 assets_per_task、提高 max_shards，或缩窄范围", len(plans), maxShards))
	}
	return plans, nil
}

// buildBatchShard computes exact scope rows and a host summary for one shard.
func buildBatchShard(assets []db.BatchAsset) batchShardPlan {
	p := batchShardPlan{Assets: assets}
	seenScope := map[string]bool{}
	seenHost := map[string]bool{}
	for _, a := range assets {
		host := batchAssetHost(a)
		if host != "" && !seenHost[host] {
			seenHost[host] = true
			p.Hosts = append(p.Hosts, host)
		}
		row, ok := batchAssetScope(a)
		if !ok {
			continue
		}
		key := row.Kind + "|" + scopeCompanyKey(row) + "|" + row.Domain + "|" + row.Net + "|" + row.Value
		if seenScope[key] {
			continue
		}
		seenScope[key] = true
		p.Scope = append(p.Scope, row)
	}
	sort.Strings(p.Hosts)
	return p
}

func scopeCompanyKey(r db.TaskScopeInput) string {
	if r.CompanyID == nil {
		return ""
	}
	return strconv.FormatInt(*r.CompanyID, 10)
}

func assetIDs(assets []db.BatchAsset) []int64 {
	out := make([]int64, 0, len(assets))
	for _, a := range assets {
		out = append(out, a.ID)
	}
	return out
}

// batchAssetHost is the shard key: domain for domain assets, URL hostname for
// services/endpoints, IP for IP assets, app/root-domain as fallbacks.
func batchAssetHost(a db.BatchAsset) string {
	if h := strings.ToLower(strings.TrimSpace(a.Domain)); h != "" {
		return h
	}
	if raw := strings.TrimSpace(a.URL); raw != "" {
		if u, err := url.Parse(raw); err == nil {
			if h := strings.ToLower(u.Hostname()); h != "" {
				return h
			}
		}
	}
	if h := strings.ToLower(strings.TrimSpace(a.IP)); h != "" {
		return h
	}
	if h := strings.ToLower(strings.TrimSpace(a.AppName)); h != "" {
		return h
	}
	if h := strings.ToLower(strings.TrimSpace(a.RootDomain)); h != "" {
		return h
	}
	return fmt.Sprintf("%s:%d", a.Type, a.ID)
}

// batchAssetScope maps an asset to a task_scope row. host-level scopes keep shards
// disjoint: a subdomain scope covers that host's services/endpoints too, which is
// why host groups are never split.
func batchAssetScope(a db.BatchAsset) (db.TaskScopeInput, bool) {
	reason := "批量扇区分片"
	switch a.Type {
	case "root_domain":
		if d := strings.ToLower(strings.TrimSpace(a.Domain)); d != "" {
			return db.TaskScopeInput{Kind: "root_domain", Domain: d, Reason: reason}, true
		}
		if d := strings.ToLower(strings.TrimSpace(a.RootDomain)); d != "" {
			return db.TaskScopeInput{Kind: "root_domain", Domain: d, Reason: reason}, true
		}
	case "subdomain":
		if d := strings.ToLower(strings.TrimSpace(a.Domain)); d != "" {
			return db.TaskScopeInput{Kind: "subdomain", Domain: d, Reason: reason}, true
		}
	case "ip":
		if c := batchHostCIDR(a.IP); c != "" {
			return db.TaskScopeInput{Kind: "ip", Net: c, Reason: reason}, true
		}
	case "service", "endpoint":
		host := batchAssetHost(a)
		if c := batchHostCIDR(host); c != "" {
			return db.TaskScopeInput{Kind: "ip", Net: c, Reason: reason}, true
		}
		if host != "" {
			return db.TaskScopeInput{Kind: "subdomain", Domain: host, Reason: reason}, true
		}
	case "app":
		// app_name 是展示名，不一定是 host；只用真实域名/根域。
		if d := strings.ToLower(strings.TrimSpace(a.RootDomain)); d != "" {
			return db.TaskScopeInput{Kind: "root_domain", Domain: d, Reason: reason}, true
		}
		if d := strings.ToLower(strings.TrimSpace(a.Domain)); d != "" {
			return db.TaskScopeInput{Kind: "subdomain", Domain: d, Reason: reason}, true
		}
		if raw := strings.TrimSpace(a.URL); raw != "" {
			if u, err := url.Parse(raw); err == nil {
				if h := strings.ToLower(u.Hostname()); h != "" {
					return db.TaskScopeInput{Kind: "subdomain", Domain: h, Reason: reason}, true
				}
			}
		}
	}
	return db.TaskScopeInput{}, false
}

func batchHostCIDR(host string) string {
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return ""
	}
	if ip.To4() != nil {
		return ip.String() + "/32"
	}
	return ip.String() + "/128"
}

func truncateRunes(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "…"
}
