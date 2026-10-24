package db

// 任务范围排除：program 的 out 范围编译到这里。scopeJoinPredicate 在匹配
// task_scope 之后还要过这张表；拦截器与 SRC host 判定也共用同一套匹配规则。

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// insertTaskScopeExclusions writes exclusion rows inside task creation.
func insertTaskScopeExclusions(tx *sql.Tx, taskID int64, rows []TaskScopeInput) error {
	for _, r := range rows {
		kind := strings.TrimSpace(strings.ToLower(r.Kind))
		switch kind {
		case "root_domain", "subdomain", "ip", "cidr":
		default:
			return fmt.Errorf("%w: unknown exclusion kind %q", ErrTaskScopeInvalid, kind)
		}
		var domain, netArg any
		if d := strings.TrimSpace(r.Domain); d != "" {
			domain = d
		}
		if n := strings.TrimSpace(r.Net); n != "" {
			netArg = n
		}
		if (kind == "root_domain" || kind == "subdomain") && domain == nil {
			return fmt.Errorf("%w: %s exclusion missing domain", ErrTaskScopeInvalid, kind)
		}
		if (kind == "ip" || kind == "cidr") && netArg == nil {
			return fmt.Errorf("%w: %s exclusion missing net", ErrTaskScopeInvalid, kind)
		}
		if _, err := tx.Exec(`
INSERT INTO task_scope_exclusions(task_id, kind, domain, net, reason)
VALUES ($1,$2,$3,$4::cidr,NULLIF($5,''))
ON CONFLICT DO NOTHING`, taskID, kind, domain, netArg, strings.TrimSpace(r.Reason)); err != nil {
			return err
		}
	}
	return nil
}

// ListTaskScopeExclusions returns a task's out-of-scope rows in TaskScope shape so
// the interceptor can use the same target matcher.
func (d *DB) ListTaskScopeExclusions(taskID int64) ([]TaskScope, error) {
	if d == nil || taskID <= 0 {
		return []TaskScope{}, nil
	}
	rows, err := d.Query(`SELECT id, task_id, kind, COALESCE(domain,''), COALESCE(net::text,''), COALESCE(reason,'')
FROM task_scope_exclusions WHERE task_id=$1 ORDER BY kind, COALESCE(domain,''), COALESCE(net::text,'')`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskScope{}
	for rows.Next() {
		var t TaskScope
		if err := rows.Scan(&t.ID, &t.TaskID, &t.Kind, &t.Domain, &t.Net, &t.Reason); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TaskHostExcluded reports whether host matches one of the task's exclusions.
func (d *DB) TaskHostExcluded(ctx context.Context, taskID int64, host string) (bool, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if d == nil || taskID <= 0 || host == "" {
		return false, nil
	}
	var n int
	err := d.QueryRowContext(ctx, `
SELECT count(*) FROM task_scope_exclusions
WHERE task_id=$1 AND (
      (kind='root_domain' AND ($2 = domain OR $2 LIKE '%.' || domain))
   OR (kind='subdomain' AND $2 = domain)
   OR (kind IN ('ip','cidr') AND net >>= try_inet($2))
)`, taskID, host).Scan(&n)
	return n > 0, err
}
