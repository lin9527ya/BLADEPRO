package db

// SRC 模式的"自动入范围白名单"：agent 新发现的资产只有命中项目/任务已有边界时
// 才自动写 task_scope，否则只登记资产。普通渗透任务保持原有自动入范围行为。
//
// 判定顺序：program out 排除 → program 企业 → program in 范围 → 任务现有 scope。
// program 有显式 in 时以 in 为准（不再让企业粗范围放大边界）。

import (
	"context"
	"database/sql"
	"net"
	"net/url"
	"strings"
)

// AutoScopeAllowed reports whether a newly registered asset may be added to the
// task's SRC scope automatically. reason explains a denial (and is empty on allow).
func (s *SrcStore) AutoScopeAllowed(ctx context.Context, taskID, assetID int64) (bool, string, error) {
	if s == nil || s.db == nil || taskID <= 0 || assetID <= 0 {
		return true, "", nil
	}
	var mode string
	var programID, taskCompany sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(mode,''), src_program_id, company_id FROM tasks WHERE id=$1 AND deleted_at IS NULL`, taskID).Scan(&mode, &programID, &taskCompany)
	if err == sql.ErrNoRows {
		return true, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if strings.TrimSpace(mode) != "src" {
		return true, "", nil // 普通渗透保持旧行为
	}
	var (
		assetType, domain, rawURL, ip string
		companyID                     sql.NullInt64
	)
	if err := s.db.QueryRowContext(ctx, `
SELECT type, COALESCE(domain,''), COALESCE(url,''), COALESCE(ip,''), company_id
FROM assets WHERE id=$1`, assetID).Scan(&assetType, &domain, &rawURL, &ip, &companyID); err != nil {
		if err == sql.ErrNoRows {
			return false, "资产不存在", nil
		}
		return false, "", err
	}
	host := assetHostForScope(assetType, domain, rawURL, ip)
	inScope, _, err := s.SrcHostInScope(ctx, taskID, host)
	if err != nil {
		return false, "", err
	}
	if inScope {
		return true, "", nil
	}
	// 以下是有边界的"证据扩权"：
	// - program 显式配了 in 范围 → 操作者写死的项目边界，agent 不能借证据绕过；
	// - program 只有 out 或完全没有机器可读范围（常见于只写了 scope_note 的 SRC 平台）
	//   → 没有可越过的 in 边界，按证据/企业归属允许扩展，但 out 排除仍然优先。
	if programID.Valid {
		var hasIn bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM src_program_scope WHERE program_id=$1 AND direction='in')`, programID.Int64).Scan(&hasIn); err != nil {
			return false, "", err
		}
		if hasIn {
			return false, "不在 program in 范围（program 边界不允许自动扩展）", nil
		}
	}
	excluded, err := s.db.TaskHostExcluded(ctx, taskID, host)
	if err != nil {
		return false, "", err
	}
	if excluded {
		return false, "被 program out 范围显式排除", nil
	}
	// 归属证据 1：资产属于任务关联的企业（tasks.company_id 或 company scope 行）。
	if taskCompany.Valid && companyID.Valid && taskCompany.Int64 == companyID.Int64 {
		return true, "", nil
	}
	if companyID.Valid {
		var companyScoped bool
		if err := s.db.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM task_scope WHERE task_id=$1 AND kind='company' AND company_id=$2)`,
			taskID, companyID.Int64).Scan(&companyScoped); err != nil {
			return false, "", err
		}
		if companyScoped {
			return true, "", nil
		}
	}
	// 归属证据 2：资产是本任务自己登记/锚定的（insert_assets 带证据节点，或已被本任务
	// 的某条意图显式点名）。这是 APP/开放式任务的主要路径：APP 调用的所属资产由
	// 探索过程本身提供归属证据，而不是靠 agent 自述。
	var evidenced bool
	if err := s.db.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM task_asset_links l
  WHERE l.task_id=$1 AND l.asset_id=$2 AND l.source_node_id IS NOT NULL
) OR EXISTS(
  SELECT 1 FROM exploration_anchors ea
  JOIN exploration_nodes en ON en.id=ea.node_id AND en.kind='intent'
  JOIN tasks t ON t.exploration_id=en.exploration_id
  WHERE t.id=$1 AND ea.asset_id=$2
)`, taskID, assetID).Scan(&evidenced); err != nil {
		return false, "", err
	}
	if evidenced {
		return true, "", nil
	}
	return false, "不在任务冻结的授权白名单，也没有任务内归属证据", nil
}

// assetHostForScope picks the host used for whitelist matching.
func assetHostForScope(assetType, domain, rawURL, ip string) string {
	if d := strings.ToLower(strings.TrimSpace(domain)); d != "" {
		return d
	}
	if raw := strings.TrimSpace(rawURL); raw != "" {
		if u, err := url.Parse(raw); err == nil {
			if h := strings.ToLower(u.Hostname()); h != "" {
				return h
			}
		}
	}
	if v := strings.TrimSpace(ip); v != "" {
		return strings.ToLower(v)
	}
	_ = assetType
	return ""
}

// scopeHostMatch applies the same root/subdomain/net semantics as task scope.
func scopeHostMatch(kind, domain, netCIDR, host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	domain = strings.ToLower(strings.TrimSpace(domain))
	if host == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "root_domain":
		return domain != "" && (host == domain || strings.HasSuffix(host, "."+domain))
	case "subdomain":
		return domain != "" && host == domain
	case "ip", "cidr":
		cidr := strings.TrimSpace(netCIDR)
		if cidr == "" {
			return false
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return false
		}
		_, n, err := net.ParseCIDR(cidr)
		return err == nil && n.Contains(ip)
	}
	return false
}

// AgentScopeAttributionAllowed 判断一条 agent 输入的 scope 是否可由任务内归属证据
// 支撑。有显式 program in 范围的任务仍严格按冻结白名单；没有 in 边界的任务（只写
// scope_note 的 SRC 平台、或普通 APP 开放式任务）可以按证据扩范围，program out 排除
// 仍然优先。APP 任务还可以用任务里的 keyword 范围做首次引导（reason 必须写依据）。
func (s *AssetStore) AgentScopeAttributionAllowed(ts TaskScope) (bool, error) {
	if s == nil || s.db == nil || ts.TaskID <= 0 {
		return false, nil
	}
	var programID, taskCompany sql.NullInt64
	if err := s.db.QueryRow(`SELECT src_program_id, company_id FROM tasks WHERE id=$1 AND deleted_at IS NULL`,
		ts.TaskID).Scan(&programID, &taskCompany); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if programID.Valid {
		var hasIn bool
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM src_program_scope WHERE program_id=$1 AND direction='in')`, programID.Int64).Scan(&hasIn); err != nil {
			return false, err
		}
		if hasIn {
			return false, nil
		}
	}
	host := strings.ToLower(strings.TrimSpace(ts.Domain))
	if strings.ToLower(strings.TrimSpace(ts.Kind)) == "ip" || strings.ToLower(strings.TrimSpace(ts.Kind)) == "cidr" {
		host = strings.SplitN(ts.Net, "/", 2)[0]
	}
	if host != "" {
		excluded, err := s.db.TaskHostExcluded(context.Background(), ts.TaskID, host)
		if err != nil {
			return false, err
		}
		if excluded {
			return false, nil
		}
	}
	var (
		rows *sql.Rows
		err  error
	)
	switch strings.ToLower(strings.TrimSpace(ts.Kind)) {
	case "subdomain":
		rows, err = s.db.Query(`SELECT id, COALESCE(company_id,0) FROM assets WHERE domain=$1`, ts.Domain)
	case "root_domain":
		rows, err = s.db.Query(`SELECT id, COALESCE(company_id,0) FROM assets WHERE root_domain=$1 OR domain=$1`, ts.Domain)
	case "ip", "cidr":
		rows, err = s.db.Query(`SELECT id, COALESCE(company_id,0) FROM assets WHERE ip=$1 OR domain=$1`, host)
	default:
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var ids []int64
	companyMatch := false
	for rows.Next() {
		var id, cid int64
		if err := rows.Scan(&id, &cid); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
		if taskCompany.Valid && cid > 0 && cid == taskCompany.Int64 {
			companyMatch = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if companyMatch {
		return true, nil
	}
	if len(ids) > 0 {
		var evidenced bool
		if err := s.db.QueryRow(`
SELECT EXISTS(
  SELECT 1 FROM task_asset_links l
  WHERE l.task_id=$1 AND l.asset_id=ANY($2::bigint[]) AND l.source_node_id IS NOT NULL
) OR EXISTS(
  SELECT 1 FROM exploration_anchors ea
  JOIN exploration_nodes en ON en.id=ea.node_id AND en.kind='intent'
  JOIN tasks t ON t.exploration_id=en.exploration_id
  WHERE t.id=$1 AND ea.asset_id=ANY($2::bigint[])
)`, ts.TaskID, ids).Scan(&evidenced); err != nil {
			return false, err
		}
		if evidenced {
			return true, nil
		}
	}
	// APP/品牌任务的首次引导：任务里带 keyword 范围（操作者给出的目标关键词），
	// agent 可以补充精确的根域/子域/IP。没有这个入口，APP 任务在还没有任何域名
	// 资产时会被冻结授权门直接封死。company/icp 不在此列。
	switch strings.ToLower(strings.TrimSpace(ts.Kind)) {
	case "subdomain", "root_domain", "ip", "cidr":
		var hasKeyword bool
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM task_scope WHERE task_id=$1 AND kind='keyword')`, ts.TaskID).Scan(&hasKeyword); err != nil {
			return false, err
		}
		if hasKeyword {
			return true, nil
		}
	}
	return false, nil
}
