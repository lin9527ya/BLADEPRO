package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

type SrcAuthorization struct {
	Allowed        []TaskScope `json:"allowed"`
	ProgramAllowed []TaskScope `json:"program_allowed,omitempty"`
	Excluded       []TaskScope `json:"excluded"`
	Hash           string      `json:"hash"`
}

// SealSrcAuthorization freezes the operator-supplied scope before agent actions.
// Repeated calls never refresh it from mutable program/company attribution.
func (d *DB) SealSrcAuthorization(ctx context.Context, taskID int64) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var mode string
	var existing []byte
	var program sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT mode,src_authorization,src_program_id FROM tasks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, taskID).Scan(&mode, &existing, &program); err != nil {
		return err
	}
	if mode != "src" || len(existing) > 0 {
		return tx.Commit()
	}
	auth := SrcAuthorization{Allowed: []TaskScope{}, Excluded: []TaskScope{}}
	rows, err := tx.QueryContext(ctx, `SELECT kind,COALESCE(domain,''),COALESCE(net::text,'') FROM task_scope WHERE task_id=$1 AND kind IN ('root_domain','subdomain','ip','cidr')
 UNION ALL SELECT CASE WHEN c.kind='domain' THEN 'root_domain' ELSE c.kind END,COALESCE(c.domain,''),COALESCE(c.net::text,'') FROM company_scope c JOIN task_scope t ON t.company_id=c.company_id WHERE t.task_id=$1 AND t.kind='company' AND c.kind IN ('domain','ip','cidr')`, taskID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var r TaskScope
		if err := rows.Scan(&r.Kind, &r.Domain, &r.Net); err != nil {
			rows.Close()
			return err
		}
		auth.Allowed = append(auth.Allowed, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT a.type,COALESCE(a.domain,''),COALESCE(a.url,''),COALESCE(a.ip,'') FROM assets a JOIN task_scope t ON t.company_id=a.company_id WHERE t.task_id=$1 AND t.kind='company'`, taskID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var typ, domain, rawURL, ip string
		if err := rows.Scan(&typ, &domain, &rawURL, &ip); err != nil {
			rows.Close()
			return err
		}
		host := assetHostForScope(typ, domain, rawURL, ip)
		if host == "" {
			continue
		}
		r := TaskScope{Kind: "subdomain", Domain: host}
		if addr := net.ParseIP(host); addr != nil {
			r = TaskScope{Kind: "ip", Net: ipToHostCIDR(host)}
		}
		auth.Allowed = append(auth.Allowed, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if program.Valid {
		rows, err = tx.QueryContext(ctx, `SELECT direction,kind,COALESCE(domain,''),COALESCE(net::text,'') FROM src_program_scope WHERE program_id=$1`, program.Int64)
		if err != nil {
			return err
		}
		for rows.Next() {
			var r TaskScope
			var direction string
			if err := rows.Scan(&direction, &r.Kind, &r.Domain, &r.Net); err != nil {
				rows.Close()
				return err
			}
			if direction == "out" {
				auth.Excluded = append(auth.Excluded, r)
			} else {
				auth.ProgramAllowed = append(auth.ProgramAllowed, r)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	if len(auth.Allowed) == 0 {
		auth.Allowed = append(auth.Allowed, auth.ProgramAllowed...)
	}
	raw, err := json.Marshal(auth)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	auth.Hash = hex.EncodeToString(sum[:])
	raw, err = json.Marshal(auth)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET src_authorization=$2 WHERE id=$1 AND src_authorization IS NULL`, taskID, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

func (a SrcAuthorization) AllowsHost(host string) bool {
	match := func(rows []TaskScope) bool {
		for _, r := range rows {
			if scopeHostMatch(r.Kind, r.Domain, r.Net, host) {
				return true
			}
		}
		return false
	}
	return !match(a.Excluded) && match(a.Allowed) && (len(a.ProgramAllowed) == 0 || match(a.ProgramAllowed))
}

func (d *DB) srcAuthorization(ctx context.Context, taskID int64) (*SrcAuthorization, error) {
	var raw []byte
	if err := d.QueryRowContext(ctx, `SELECT src_authorization FROM tasks WHERE id=$1`, taskID).Scan(&raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		if d.tx != nil {
			return nil, fmt.Errorf("SRC 授权快照必须在收官事务之前冻结")
		}
		if err := d.SealSrcAuthorization(ctx, taskID); err != nil {
			return nil, err
		}
		if err := d.QueryRowContext(ctx, `SELECT src_authorization FROM tasks WHERE id=$1`, taskID).Scan(&raw); err != nil {
			return nil, err
		}
	}
	var a SrcAuthorization
	if len(raw) == 0 {
		return nil, fmt.Errorf("SRC 授权快照缺失")
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// Agent refinements must be subsets of the frozen authorization, not just members
// of the same company. Company/keyword/ICP expansion is never an agent action.
func (s *AssetStore) checkAgentSrcScope(ts TaskScope) error {
	var mode string
	if err := s.db.QueryRow(`SELECT mode FROM tasks WHERE id=$1`, ts.TaskID).Scan(&mode); err != nil {
		return err
	}
	if mode != "src" {
		return nil
	}
	a, err := s.db.srcAuthorization(context.Background(), ts.TaskID)
	if err != nil {
		return err
	}
	switch ts.Kind {
	case "subdomain":
		if a.AllowsHost(ts.Domain) {
			return nil
		}
	case "root_domain":
		for _, r := range a.Allowed {
			if r.Kind == "root_domain" && scopeHostMatch(r.Kind, r.Domain, r.Net, ts.Domain) && a.AllowsHost(ts.Domain) {
				return nil
			}
		}
	case "ip", "cidr":
		ip, n, err := net.ParseCIDR(ts.Net)
		if err != nil {
			return err
		}
		end := append(net.IP(nil), n.IP...)
		for i := range end {
			end[i] |= ^n.Mask[i]
		}
		for _, r := range a.Allowed {
			if (r.Kind == "ip" || r.Kind == "cidr") && scopeHostMatch(r.Kind, r.Domain, r.Net, ip.String()) && scopeHostMatch(r.Kind, r.Domain, r.Net, end.String()) && a.AllowsHost(ip.String()) && a.AllowsHost(end.String()) {
				return nil
			}
		}
	}
	return fmt.Errorf("SRC Agent 不能扩大授权范围（%s）；请由人工重新授权", strings.TrimSpace(ts.Kind))
}
