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
	var programID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(mode,''), src_program_id FROM tasks WHERE id=$1 AND deleted_at IS NULL`, taskID).Scan(&mode, &programID)
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
	return false, "不在任务冻结的授权白名单", nil
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
