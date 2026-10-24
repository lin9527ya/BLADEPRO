package db

// 全局 SRC 漏斗：资产 → 在范围资产 → 有终态格 → 中危+发现 → 报告齐备 →
// 已提交 → 已受理/到账。按 program / 等级 / 天拆分，并带 token 与费用，
// 让「每条有效发现的成本」有统一口径。

import (
	"context"
	"database/sql"
	"fmt"
)

// SrcFunnelQuery selects the window and optional program.
type SrcFunnelQuery struct {
	Days      int
	ProgramID int64
}

type SrcFunnelStage struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value int64  `json:"value"`
}

type SrcFunnelProgram struct {
	ProgramID          int64   `json:"program_id"`
	Name               string  `json:"name"`
	Tasks              int64   `json:"tasks"`
	AssetsTerminal     int64   `json:"assets_terminal"`
	FindingsMediumPlus int64   `json:"findings_medium_plus"`
	ReportsReady       int64   `json:"reports_ready"`
	Submitted          int64   `json:"submitted"`
	Accepted           int64   `json:"accepted"`
	Paid               int64   `json:"paid"`
	Tokens             int64   `json:"tokens"`
	CostUSD            float64 `json:"cost_usd"`
}

type SrcFunnelCampaign struct {
	CampaignID         int64   `json:"campaign_id"`
	Name               string  `json:"name"`
	Tasks              int64   `json:"tasks"`
	AssetsTerminal     int64   `json:"assets_terminal"`
	FindingsMediumPlus int64   `json:"findings_medium_plus"`
	ReportsReady       int64   `json:"reports_ready"`
	Submitted          int64   `json:"submitted"`
	Accepted           int64   `json:"accepted"`
	Paid               int64   `json:"paid"`
	Tokens             int64   `json:"tokens"`
	CostUSD            float64 `json:"cost_usd"`
}

type SrcFunnelSeverity struct {
	Severity     string `json:"severity"`
	Findings     int64  `json:"findings"`
	ReportsReady int64  `json:"reports_ready"`
	Submitted    int64  `json:"submitted"`
	Accepted     int64  `json:"accepted"`
	Paid         int64  `json:"paid"`
}

type SrcFunnelDay struct {
	Date     string  `json:"date"`
	Tasks    int64   `json:"tasks"`
	Findings int64   `json:"findings"`
	Tokens   int64   `json:"tokens"`
	CostUSD  float64 `json:"cost_usd"`
}

type SrcFunnel struct {
	Days             int                 `json:"days"`
	ProgramID        int64               `json:"program_id,omitempty"`
	ProgramName      string              `json:"program_name,omitempty"`
	Stages           []SrcFunnelStage    `json:"stages"`
	Tasks            int64               `json:"tasks"`
	CellsTerminal    int64               `json:"cells_terminal"`
	Tokens           int64               `json:"tokens"`
	CostUSD          float64             `json:"cost_usd"`
	CostPerEffective float64             `json:"cost_per_effective"`
	ByProgram        []SrcFunnelProgram  `json:"by_program"`
	ByCampaign       []SrcFunnelCampaign `json:"by_campaign"`
	BySeverity       []SrcFunnelSeverity `json:"by_severity"`
	Daily            []SrcFunnelDay      `json:"daily"`
}

func srcTasksCTE() string {
	return `src_tasks AS (
    SELECT id, src_program_id, campaign_id, created_at
    FROM tasks
    WHERE mode='src' AND deleted_at IS NULL
      AND created_at >= now() - ($1::int * interval '1 day')
      AND ($2::bigint = 0 OR src_program_id = $2)
)`
}

func (d *DB) SrcFunnel(ctx context.Context, q SrcFunnelQuery) (*SrcFunnel, error) {
	if d == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	days := q.Days
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	out := &SrcFunnel{Days: days, ProgramID: q.ProgramID, Stages: []SrcFunnelStage{}, ByProgram: []SrcFunnelProgram{}, ByCampaign: []SrcFunnelCampaign{}, BySeverity: []SrcFunnelSeverity{}, Daily: []SrcFunnelDay{}}

	assetsTotal, err := d.funnelAssetsTotal(ctx, q.ProgramID)
	if err != nil {
		return nil, err
	}
	var assetsInScope, assetsTerminal, assetsFound int64
	err = d.QueryRowContext(ctx, `WITH `+srcTasksCTE()+`
SELECT
  (SELECT count(DISTINCT a.id) FROM src_tasks t JOIN task_scope ts ON ts.task_id=t.id
     JOIN assets a ON `+scopeJoinPredicate+`),
  (SELECT count(DISTINCT c.asset_id) FROM src_cells c JOIN src_tasks t ON t.id=c.task_id),
  (SELECT count(DISTINCT c.asset_id) FROM src_cells c JOIN src_tasks t ON t.id=c.task_id WHERE c.state='tested_found')`,
		days, q.ProgramID).Scan(&assetsInScope, &assetsTerminal, &assetsFound)
	if err != nil {
		return nil, err
	}

	var tasks, cellsTerminal, findingsMedium, reportsReady, submitted, accepted, paid int64
	var tokens int64
	var cost float64
	err = d.QueryRowContext(ctx, `WITH `+srcTasksCTE()+`
SELECT
  (SELECT count(*) FROM src_tasks),
  (SELECT count(*) FROM src_cells c JOIN src_tasks t ON t.id=c.task_id),
  (SELECT count(*) FROM findings f JOIN src_tasks t ON t.id=f.task_id
     WHERE lower(f.severity) IN ('critical','high','medium')),
  (SELECT count(DISTINCT f.id) FROM findings f JOIN src_tasks t ON t.id=f.task_id
     JOIN src_finding_meta m ON m.finding_id=f.id
     WHERE lower(f.severity) IN ('critical','high','medium') AND COALESCE(m.report_ready,false)),
  (SELECT count(DISTINCT s.finding_id) FROM src_submissions s
     JOIN findings f ON f.id=s.finding_id JOIN src_tasks t ON t.id=f.task_id
     WHERE lower(f.severity) IN ('critical','high','medium')
       AND s.status IN ('submitted','accepted','rejected','duplicate','paid')),
  (SELECT count(DISTINCT s.finding_id) FROM src_submissions s
     JOIN findings f ON f.id=s.finding_id JOIN src_tasks t ON t.id=f.task_id
     WHERE lower(f.severity) IN ('critical','high','medium')
       AND s.status IN ('accepted','paid')),
  (SELECT count(DISTINCT s.finding_id) FROM src_submissions s
     JOIN findings f ON f.id=s.finding_id JOIN src_tasks t ON t.id=f.task_id
     WHERE lower(f.severity) IN ('critical','high','medium') AND s.status='paid'),
  (SELECT COALESCE(SUM(u.input_tokens+u.output_tokens),0) FROM llm_usage u
     JOIN src_tasks t ON u.task_id=t.id::text),
  (SELECT COALESCE(SUM(u.cost_usd),0) FROM llm_usage u
     JOIN src_tasks t ON u.task_id=t.id::text)`,
		days, q.ProgramID).Scan(&tasks, &cellsTerminal, &findingsMedium, &reportsReady, &submitted, &accepted, &paid, &tokens, &cost)
	if err != nil {
		return nil, err
	}
	if q.ProgramID > 0 {
		_ = d.QueryRowContext(ctx, `SELECT name FROM src_programs WHERE id=$1`, q.ProgramID).Scan(&out.ProgramName)
	}
	out.Tasks, out.CellsTerminal, out.Tokens, out.CostUSD = tasks, cellsTerminal, tokens, cost
	if reportsReady > 0 {
		out.CostPerEffective = cost / float64(reportsReady)
	}
	out.Stages = []SrcFunnelStage{
		{Key: "assets_total", Label: "资产", Value: assetsTotal},
		{Key: "assets_in_scope", Label: "在范围资产", Value: assetsInScope},
		{Key: "assets_terminal", Label: "有终态格资产", Value: assetsTerminal},
		{Key: "assets_found", Label: "有洞资产", Value: assetsFound},
		{Key: "findings_medium_plus", Label: "中危+发现", Value: findingsMedium},
		{Key: "reports_ready", Label: "报告齐备", Value: reportsReady},
		{Key: "submitted", Label: "已提交", Value: submitted},
		{Key: "accepted", Label: "已受理", Value: accepted},
		{Key: "paid", Label: "已到账", Value: paid},
	}

	// 按 program 拆分。
	rows, err := d.QueryContext(ctx, `WITH `+srcTasksCTE()+`
SELECT x.pid, COALESCE(p.name,'未绑定') AS name,
  (SELECT count(*) FROM src_tasks t WHERE COALESCE(t.src_program_id,0)=x.pid),
  (SELECT count(DISTINCT c.asset_id) FROM src_cells c JOIN src_tasks t ON t.id=c.task_id WHERE COALESCE(t.src_program_id,0)=x.pid),
  (SELECT count(DISTINCT f.id) FROM findings f JOIN src_tasks t ON t.id=f.task_id WHERE COALESCE(t.src_program_id,0)=x.pid AND lower(f.severity) IN ('critical','high','medium')),
  (SELECT count(DISTINCT f.id) FROM findings f JOIN src_tasks t ON t.id=f.task_id JOIN src_finding_meta m ON m.finding_id=f.id
      WHERE COALESCE(t.src_program_id,0)=x.pid AND lower(f.severity) IN ('critical','high','medium') AND COALESCE(m.report_ready,false)),
  (SELECT count(DISTINCT s.finding_id) FROM src_submissions s JOIN findings f ON f.id=s.finding_id JOIN src_tasks t ON t.id=f.task_id
      WHERE COALESCE(t.src_program_id,0)=x.pid AND lower(f.severity) IN ('critical','high','medium')
        AND s.status IN ('submitted','accepted','rejected','duplicate','paid')),
  (SELECT count(DISTINCT s.finding_id) FROM src_submissions s JOIN findings f ON f.id=s.finding_id JOIN src_tasks t ON t.id=f.task_id
      WHERE COALESCE(t.src_program_id,0)=x.pid AND lower(f.severity) IN ('critical','high','medium')
        AND s.status IN ('accepted','paid')),
  (SELECT count(DISTINCT s.finding_id) FROM src_submissions s JOIN findings f ON f.id=s.finding_id JOIN src_tasks t ON t.id=f.task_id
      WHERE COALESCE(t.src_program_id,0)=x.pid AND lower(f.severity) IN ('critical','high','medium') AND s.status='paid'),
  (SELECT COALESCE(SUM(u.input_tokens+u.output_tokens),0) FROM llm_usage u JOIN src_tasks t ON u.task_id=t.id::text WHERE COALESCE(t.src_program_id,0)=x.pid),
  (SELECT COALESCE(SUM(u.cost_usd),0) FROM llm_usage u JOIN src_tasks t ON u.task_id=t.id::text WHERE COALESCE(t.src_program_id,0)=x.pid)
FROM (SELECT DISTINCT COALESCE(src_program_id,0) AS pid FROM src_tasks) x
LEFT JOIN src_programs p ON p.id=x.pid
ORDER BY 3 DESC, x.pid`, days, q.ProgramID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p SrcFunnelProgram
		if err := rows.Scan(&p.ProgramID, &p.Name, &p.Tasks, &p.AssetsTerminal, &p.FindingsMediumPlus,
			&p.ReportsReady, &p.Submitted, &p.Accepted, &p.Paid, &p.Tokens, &p.CostUSD); err != nil {
			return nil, err
		}
		out.ByProgram = append(out.ByProgram, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 按 campaign 拆分：批量扇出的分片任务归入同一战役，这里给出战役级漏斗。
	rows, err = d.QueryContext(ctx, `WITH `+srcTasksCTE()+`
SELECT x.cid, COALESCE(c.name,'未归属战役') AS name,
  (SELECT count(*) FROM src_tasks t WHERE COALESCE(t.campaign_id,0)=x.cid),
  (SELECT count(DISTINCT sc.asset_id) FROM src_cells sc JOIN src_tasks t ON t.id=sc.task_id WHERE COALESCE(t.campaign_id,0)=x.cid),
  (SELECT count(DISTINCT f.id) FROM findings f JOIN src_tasks t ON t.id=f.task_id WHERE COALESCE(t.campaign_id,0)=x.cid AND lower(f.severity) IN ('critical','high','medium')),
  (SELECT count(DISTINCT f.id) FROM findings f JOIN src_tasks t ON t.id=f.task_id JOIN src_finding_meta m ON m.finding_id=f.id
      WHERE COALESCE(t.campaign_id,0)=x.cid AND lower(f.severity) IN ('critical','high','medium') AND COALESCE(m.report_ready,false)),
  (SELECT count(DISTINCT s.finding_id) FROM src_submissions s JOIN findings f ON f.id=s.finding_id JOIN src_tasks t ON t.id=f.task_id
      WHERE COALESCE(t.campaign_id,0)=x.cid AND lower(f.severity) IN ('critical','high','medium')
        AND s.status IN ('submitted','accepted','rejected','duplicate','paid')),
  (SELECT count(DISTINCT s.finding_id) FROM src_submissions s JOIN findings f ON f.id=s.finding_id JOIN src_tasks t ON t.id=f.task_id
      WHERE COALESCE(t.campaign_id,0)=x.cid AND lower(f.severity) IN ('critical','high','medium')
        AND s.status IN ('accepted','paid')),
  (SELECT count(DISTINCT s.finding_id) FROM src_submissions s JOIN findings f ON f.id=s.finding_id JOIN src_tasks t ON t.id=f.task_id
      WHERE COALESCE(t.campaign_id,0)=x.cid AND lower(f.severity) IN ('critical','high','medium') AND s.status='paid'),
  (SELECT COALESCE(SUM(u.input_tokens+u.output_tokens),0) FROM llm_usage u JOIN src_tasks t ON u.task_id=t.id::text WHERE COALESCE(t.campaign_id,0)=x.cid),
  (SELECT COALESCE(SUM(u.cost_usd),0) FROM llm_usage u JOIN src_tasks t ON u.task_id=t.id::text WHERE COALESCE(t.campaign_id,0)=x.cid)
FROM (SELECT DISTINCT COALESCE(campaign_id,0) AS cid FROM src_tasks) x
LEFT JOIN campaigns c ON c.id=x.cid
ORDER BY 3 DESC, x.cid`, days, q.ProgramID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c SrcFunnelCampaign
		if err := rows.Scan(&c.CampaignID, &c.Name, &c.Tasks, &c.AssetsTerminal, &c.FindingsMediumPlus,
			&c.ReportsReady, &c.Submitted, &c.Accepted, &c.Paid, &c.Tokens, &c.CostUSD); err != nil {
			return nil, err
		}
		out.ByCampaign = append(out.ByCampaign, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 按等级拆分。
	rows, err = d.QueryContext(ctx, `WITH `+srcTasksCTE()+`
SELECT COALESCE(NULLIF(lower(f.severity),''),'unknown') AS severity,
  count(DISTINCT f.id),
  count(DISTINCT f.id) FILTER (WHERE COALESCE(m.report_ready,false)),
  count(DISTINCT f.id) FILTER (WHERE s.status IN ('submitted','accepted','rejected','duplicate','paid')),
  count(DISTINCT f.id) FILTER (WHERE s.status IN ('accepted','paid')),
  count(DISTINCT f.id) FILTER (WHERE s.status='paid')
FROM findings f
JOIN src_tasks t ON t.id=f.task_id
LEFT JOIN src_finding_meta m ON m.finding_id=f.id
LEFT JOIN src_submissions s ON s.finding_id=f.id
GROUP BY 1
ORDER BY 2 DESC, 1`, days, q.ProgramID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r SrcFunnelSeverity
		if err := rows.Scan(&r.Severity, &r.Findings, &r.ReportsReady, &r.Submitted, &r.Accepted, &r.Paid); err != nil {
			return nil, err
		}
		out.BySeverity = append(out.BySeverity, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 按天（UTC）拆分，供趋势图使用。
	rows, err = d.QueryContext(ctx, `WITH `+srcTasksCTE()+`
SELECT to_char(t.created_at AT TIME ZONE 'UTC','YYYY-MM-DD') AS day,
  count(DISTINCT t.id),
  COALESCE(SUM(f.cnt),0),
  COALESCE(SUM(u.tokens),0),
  COALESCE(SUM(u.cost),0)
FROM src_tasks t
LEFT JOIN (SELECT task_id, count(*) AS cnt FROM findings GROUP BY task_id) f ON f.task_id=t.id
LEFT JOIN (SELECT task_id, sum(input_tokens+output_tokens) AS tokens, sum(cost_usd) AS cost FROM llm_usage GROUP BY task_id) u ON u.task_id=t.id::text
GROUP BY 1 ORDER BY 1`, days, q.ProgramID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r SrcFunnelDay
		if err := rows.Scan(&r.Date, &r.Tasks, &r.Findings, &r.Tokens, &r.CostUSD); err != nil {
			return nil, err
		}
		out.Daily = append(out.Daily, r)
	}
	return out, rows.Err()
}

// funnelAssetsTotal is the denominator's top: global asset library, or the
// program's company assets when the program is bound to one.
func (d *DB) funnelAssetsTotal(ctx context.Context, programID int64) (int64, error) {
	if programID <= 0 {
		var n int64
		err := d.QueryRowContext(ctx, `SELECT count(*) FROM assets`).Scan(&n)
		return n, err
	}
	var companyID sql.NullInt64
	if err := d.QueryRowContext(ctx, `SELECT company_id FROM src_programs WHERE id=$1`, programID).Scan(&companyID); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	if !companyID.Valid {
		var n int64
		err := d.QueryRowContext(ctx, `SELECT count(*) FROM assets`).Scan(&n)
		return n, err
	}
	var n int64
	err := d.QueryRowContext(ctx, `SELECT count(*) FROM assets WHERE company_id=$1`, companyID.Int64).Scan(&n)
	return n, err
}
