package db

// SRC 挖掘模式的覆盖格（src_cells）。与 task_scope.go 的 CoverageMatrix 分工：
//
//   - CoverageMatrix 是从**意图**推导的只读视图（哪个风险面被派过意图、结果如何）；
//     它回答"我们干过什么"，missing 的面根本不出现在里面。
//   - src_cells 是 SRC 模式的**判定源**：每个（范围内资产 × 必做面）必须有显式终态，
//     空格 = 没测。收尾门只认它。
//
// 只存终态、不预填行：空白格由「范围内资产 × 必做面 − 已有行」现算，避免资产 × 面
// × item 的行数在创建任务时就爆炸。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// src_cells.state 的四个终态。不在这里的状态一律拒绝——"进行中"不是终态，
// 空格本身就是进行中的表达方式。
const (
	SrcStateFound      = "tested_found" // 测了，有洞（必须挂 finding_id）
	SrcStateClear      = "tested_clear" // 测了，没洞（必须给证据节点或说明）
	SrcStateNA         = "na"           // 不适用（必须写原因）
	SrcStateBudgetStop = "budget_stop"  // 预算/时间用尽，没测完（必须写原因）
)

// SrcStates 是全部合法终态（顺序即 UI 展示顺序）。
var SrcStates = []string{SrcStateFound, SrcStateClear, SrcStateNA, SrcStateBudgetStop}

// IsSrcState reports whether state is one of the four terminal cell states.
func IsSrcState(state string) bool {
	for _, s := range SrcStates {
		if s == state {
			return true
		}
	}
	return false
}

// SrcCell is one terminal (asset × area × item) coverage cell of a task.
type SrcCell struct {
	ID        int64  `json:"id"`
	TaskID    int64  `json:"task_id"`
	AssetID   int64  `json:"asset_id"`
	Asset     string `json:"asset,omitempty"` // 资产标签（url/domain/ip…），只读展示用
	AssetType string `json:"asset_type,omitempty"`
	Area      string `json:"area"`
	Item      string `json:"item"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
	// NACode 是 na 的理由码（srcmode 的白名单）：把"没这个面"和"没测"分开。
	NACode         string `json:"na_code,omitempty"`
	EvidenceNodeID int64  `json:"evidence_node_id,omitempty"`
	FindingID      int64  `json:"finding_id,omitempty"`
	Attempts       int    `json:"attempts"`
	UpdatedBy      string `json:"updated_by,omitempty"`
	// InheritedFromTaskID >0 表示这一格是从该任务继承的 tested_clear（证据在源任务），
	// 本轮没重测。重测并覆盖后归零。
	InheritedFromTaskID int64     `json:"inherited_from_task_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// SrcAsset is one in-scope asset that the SRC gate must account for.
type SrcAsset struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
	// Tier 是人工设置的资产分级（assets.extra.asset_tier）：primary/api/admin/
	// mobile/third_party/other。空 = 未分级，按 1.0 参与补格优先级。
	Tier string `json:"tier,omitempty"`
}

// SrcStore is the SRC coverage store (cells + mode snapshot reads).
type SrcStore struct{ db *DB }

// Src returns the SRC coverage store bound to this database.
func (d *DB) Src() *SrcStore { return &SrcStore{db: d} }

// Src returns the SRC coverage store bound to the same database. It exists so
// agent-side callers that only hold an AssetStore can reach the SRC tables.
// A nil receiver or nil database returns nil: callers treat that as "no SRC mode".
func (s *AssetStore) Src() *SrcStore {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Src()
}

// TaskMode reads a task's mode snapshot (mode + mode_doc). A missing task or an
// unset mode yields ("", nil) — i.e. plain pentest, no SRC overlay.
func (s *SrcStore) TaskMode(ctx context.Context, taskID int64) (string, json.RawMessage, error) {
	if s == nil || s.db == nil || taskID <= 0 {
		return "", nil, nil
	}
	var mode string
	var doc []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(mode,''), COALESCE(mode_doc,'{}'::jsonb) FROM tasks WHERE id=$1`, taskID).
		Scan(&mode, &doc)
	if err == sql.ErrNoRows {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	return mode, json.RawMessage(doc), nil
}

const srcCellCols = `c.id, c.task_id, c.asset_id,
COALESCE(a.url, a.domain, a.ip, a.app_name, a.root_domain, ''), COALESCE(a.type,''),
c.area, c.item, c.state, c.reason, c.na_code,
COALESCE(c.evidence_node_id,0), COALESCE(c.finding_id,0),
c.attempts, c.updated_by, COALESCE(c.inherited_from_task_id,0), c.created_at, c.updated_at`

func scanSrcCell(sc interface{ Scan(...any) error }) (*SrcCell, error) {
	var c SrcCell
	if err := sc.Scan(&c.ID, &c.TaskID, &c.AssetID, &c.Asset, &c.AssetType,
		&c.Area, &c.Item, &c.State, &c.Reason, &c.NACode,
		&c.EvidenceNodeID, &c.FindingID,
		&c.Attempts, &c.UpdatedBy, &c.InheritedFromTaskID, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// UpsertSrcCell writes (or overwrites) one cell's terminal state. A cell is
// keyed by (task, asset, area, item); re-marking it bumps attempts and replaces
// the state, while a later mark that omits evidence/finding keeps the previous
// binding rather than silently dropping the proof.
func (s *SrcStore) UpsertSrcCell(ctx context.Context, c *SrcCell) (*SrcCell, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("src store unavailable")
	}
	if c == nil || c.TaskID <= 0 || c.AssetID <= 0 || c.Area == "" || !IsSrcState(c.State) {
		return nil, fmt.Errorf("invalid src cell")
	}
	item := c.Item
	if item == "" {
		item = "*"
	}
	var ev, find any
	if c.EvidenceNodeID > 0 {
		ev = c.EvidenceNodeID
	}
	if c.FindingID > 0 {
		find = c.FindingID
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO src_cells(task_id, asset_id, area, item, state, reason, na_code, evidence_node_id, finding_id, attempts, updated_by, inherited_from_task_id, observed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,1,$10,$11,now())
ON CONFLICT (task_id, asset_id, area, item) DO UPDATE SET
    state            = EXCLUDED.state,
    reason           = EXCLUDED.reason,
    na_code          = EXCLUDED.na_code,
    evidence_node_id = COALESCE(EXCLUDED.evidence_node_id, src_cells.evidence_node_id),
    finding_id       = COALESCE(EXCLUDED.finding_id, src_cells.finding_id),
    attempts         = src_cells.attempts + 1,
    -- revision_count is a separate edit ledger; attempts keeps its historical
    -- "every write is one attempt" semantics used by the cap.
    revision_count   = src_cells.revision_count + 1,
    updated_by       = EXCLUDED.updated_by,
    observed_at      = now(),
    -- 正常点格/agent 重测一律清零继承标记：这条结论从现在起是本轮新鲜结论。
    inherited_from_task_id = EXCLUDED.inherited_from_task_id
RETURNING id`, c.TaskID, c.AssetID, c.Area, item, c.State, c.Reason, c.NACode, ev, find, c.UpdatedBy, c.InheritedFromTaskID)
	var id int64
	if err := row.Scan(&id); err != nil {
		return nil, err
	}
	return s.GetSrcCell(ctx, c.TaskID, c.AssetID, c.Area, item)
}

// GetSrcCell returns one cell, or (nil, nil) when the cell is still blank.
func (s *SrcStore) GetSrcCell(ctx context.Context, taskID, assetID int64, area, item string) (*SrcCell, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if item == "" {
		item = "*"
	}
	cell, err := scanSrcCell(s.db.QueryRowContext(ctx,
		`SELECT `+srcCellCols+` FROM src_cells c LEFT JOIN assets a ON a.id=c.asset_id
WHERE c.task_id=$1 AND c.asset_id=$2 AND c.area=$3 AND c.item=$4`, taskID, assetID, area, item))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cell, nil
}

// ListSrcCells returns every terminal cell of a task, newest first by asset.
func (s *SrcStore) ListSrcCells(ctx context.Context, taskID int64) ([]SrcCell, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+srcCellCols+` FROM src_cells c LEFT JOIN assets a ON a.id=c.asset_id
WHERE c.task_id=$1 ORDER BY c.asset_id, c.area, c.item`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcCell{}
	for rows.Next() {
		c, err := scanSrcCell(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// SrcCellsByAsset groups a task's terminal cells by asset id.
func (s *SrcStore) SrcCellsByAsset(ctx context.Context, taskID int64) (map[int64][]SrcCell, error) {
	cells, err := s.ListSrcCells(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]SrcCell, len(cells))
	for _, c := range cells {
		out[c.AssetID] = append(out[c.AssetID], c)
	}
	return out, nil
}

// InScopeSrcAssets lists the in-scope assets that carry a site face — the SRC
// gate's denominator. The predicate is the same task_scope × assets join used by
// CoverageMatrix (see srcTargetCTE), restricted to the types that own a必做面.
func (s *SrcStore) InScopeSrcAssets(ctx context.Context, taskID int64) ([]SrcAsset, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `WITH `+srcTargetCTE+`
SELECT t.id, t.type, t.label, t.tier FROM target t
WHERE t.type IN ('root_domain','subdomain','ip','app','service','endpoint')
ORDER BY t.type, t.label`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcAsset{}
	for rows.Next() {
		var a SrcAsset
		if err := rows.Scan(&a.ID, &a.Type, &a.Label, &a.Tier); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SrcAssetInScope resolves one asset as an in-scope SRC asset (nil when it is not
// in this task's scope — the mark_cell guard against writing cells for assets the
// task was never authorized to test).
func (s *SrcStore) SrcAssetInScope(ctx context.Context, taskID, assetID int64) (*SrcAsset, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx, `WITH `+srcTargetCTE+`
SELECT t.id, t.type, t.label, t.tier FROM target t WHERE t.id=$2`, taskID, assetID)
	var a SrcAsset
	if err := row.Scan(&a.ID, &a.Type, &a.Label, &a.Tier); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

// SrcCellCounts returns (总终态格数, 其中 tested_found+tested_clear 的格数).
func (s *SrcStore) SrcCellCounts(ctx context.Context, taskID int64) (total, measured int, err error) {
	if s == nil || s.db == nil {
		return 0, 0, nil
	}
	err = s.db.QueryRowContext(ctx, `
SELECT count(*), count(*) FILTER (WHERE state IN ('tested_found','tested_clear'))
FROM src_cells WHERE task_id=$1`, taskID).Scan(&total, &measured)
	return total, measured, err
}

// NodeBelongsToTask reports whether an exploration node belongs to this task's
// exploration. src_cells.evidence_node_id 只允许指本任务的节点——挂到别的任务的
// 证据上会让"测过"这句话失去可核对性。
func (s *SrcStore) NodeBelongsToTask(ctx context.Context, taskID, nodeID int64) (bool, error) {
	if s == nil || s.db == nil || nodeID <= 0 {
		return false, nil
	}
	var ok bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM exploration_nodes en
    JOIN tasks t ON t.exploration_id = en.exploration_id
    WHERE t.id=$1 AND en.id=$2 AND t.deleted_at IS NULL
)`, taskID, nodeID).Scan(&ok)
	return ok, err
}

// FindingBelongsToTask reports whether a finding row belongs to this task.
func (s *SrcStore) FindingBelongsToTask(ctx context.Context, taskID, findingID int64) (bool, error) {
	if s == nil || s.db == nil || findingID <= 0 {
		return false, nil
	}
	var ok bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (SELECT 1 FROM findings WHERE id=$2 AND task_id=$1)`, taskID, findingID).Scan(&ok)
	return ok, err
}

// SrcInheritResult 是一次覆盖继承实际复制了多少行。
type SrcInheritResult struct {
	Cells  int `json:"cells"`
	Checks int `json:"checks"`
}

// LatestInheritSource 选出可作为覆盖继承来源的任务：SRC 模式、未被删除、在时间窗内，
// 且在新任务范围内至少有一格 tested_clear 或一项 done 收尾项（否则继承没有意义）。
// 优先显式来源任务（preferredIDs），其次同平台项目，最后同企业；同类按最近活动排序。
func (s *SrcStore) LatestInheritSource(ctx context.Context, taskID int64, programID int64, companyIDs []int64, preferredIDs []int64, since time.Time) (int64, error) {
	// 没有平台项目/企业/显式来源时也允许按资产重叠选源：共同资产本身就说明两条任务
	// 测的是同一个目标。
	if s == nil || s.db == nil || taskID <= 0 {
		return 0, nil
	}
	if companyIDs == nil {
		companyIDs = []int64{}
	}
	if preferredIDs == nil {
		preferredIDs = []int64{}
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `
WITH target AS (`+scopeTargetSelect+`)
SELECT t.id
FROM tasks t
WHERE t.mode='src' AND t.deleted_at IS NULL AND t.id <> $1
  AND COALESCE(t.completed_at, t.created_at) >= $5
  AND (
      t.id = ANY($4::bigint[])
      OR ($2 > 0 AND t.src_program_id = $2)
      OR EXISTS (
          SELECT 1 FROM task_scope ts
          WHERE ts.task_id=t.id AND ts.kind='company' AND ts.company_id = ANY($3::bigint[])
      )
      -- 三者都没有时按"共同资产"兜底（target 子查询已经限定了新任务范围）。
      OR ($2 <= 0 AND cardinality($3::bigint[]) = 0 AND cardinality($4::bigint[]) = 0)
  )
  AND (
      EXISTS (SELECT 1 FROM src_cells c JOIN target tg ON tg.id=c.asset_id
              WHERE c.task_id=t.id AND c.state='tested_clear')
      OR EXISTS (SELECT 1 FROM src_checks k JOIN target tg ON tg.id=k.asset_id
                 WHERE k.task_id=t.id AND k.state='done')
  )
ORDER BY CASE
             WHEN t.id = ANY($4::bigint[]) THEN 0
             WHEN $2 > 0 AND t.src_program_id = $2 THEN 1
             ELSE 2
         END,
         COALESCE(t.completed_at, t.created_at) DESC,
         t.id DESC
LIMIT 1`, taskID, programID, companyIDs, preferredIDs, since).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}

// InheritSrcCoverage copies the source task's fresh-negative coverage into the
// target task, restricted to assets in the target's scope:
//
//   - tested_clear cells → also tested_clear, with inherited_from_task_id set and
//     the source task named in the reason (evidence stays in the source task, so the
//     new task has no evidence node of its own);
//   - done checks → copied with their metrics, so the收尾闸 numbers are inherited too.
//
// It never copies findings, na, or budget_stop: those are task-specific conclusions
// (a finding belongs to the old task's evidence chain; na plausibility can change).
// ON CONFLICT DO NOTHING means an existing fresh cell always wins.
func (s *SrcStore) InheritSrcCoverage(ctx context.Context, fromTaskID, toTaskID int64) (SrcInheritResult, error) {
	var out SrcInheritResult
	if s == nil || s.db == nil {
		return out, fmt.Errorf("src store unavailable")
	}
	if fromTaskID <= 0 || toTaskID <= 0 || fromTaskID == toTaskID {
		return out, fmt.Errorf("invalid inherit source/target")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.ExecContext(ctx, `
WITH target AS (`+scopeTargetSelect+`)
INSERT INTO src_cells(task_id, asset_id, area, item, state, reason, na_code, evidence_node_id, finding_id, attempts, updated_by, inherited_from_task_id, observed_at)
SELECT $1, c.asset_id, c.area, c.item, c.state,
       CASE
           WHEN btrim(COALESCE(c.reason,'')) = ''
               THEN '继承自任务 #' || COALESCE(NULLIF(c.inherited_from_task_id,0), c.task_id)
           WHEN COALESCE(c.inherited_from_task_id,0) > 0 THEN c.reason
           ELSE c.reason || '（继承自任务 #' || c.task_id || '）'
       END,
       c.na_code, NULL, NULL, 0, 'inherit',
       CASE WHEN COALESCE(c.inherited_from_task_id,0) > 0 THEN c.inherited_from_task_id ELSE c.task_id END,
       c.observed_at
FROM src_cells c
JOIN target t ON t.id = c.asset_id
WHERE c.task_id = $2 AND c.state = 'tested_clear'
  AND c.observed_at IS NOT NULL AND c.observed_at > now() - interval '30 days'
ON CONFLICT (task_id, asset_id, area, item) DO NOTHING`, toTaskID, fromTaskID)
	if err != nil {
		return out, err
	}
	if n, err := res.RowsAffected(); err == nil {
		out.Cells = int(n)
	}

	res, err = tx.ExecContext(ctx, `
WITH target AS (`+scopeTargetSelect+`)
INSERT INTO src_checks(task_id, asset_id, check_key, state, na_code, reason, evidence_node_id, metrics, updated_by, inherited_from_task_id, verification, observed_at)
SELECT $1, c.asset_id, c.check_key, c.state, c.na_code,
       CASE
           WHEN btrim(COALESCE(c.reason,'')) = ''
               THEN '继承自任务 #' || COALESCE(NULLIF(c.inherited_from_task_id,0), c.task_id)
           WHEN COALESCE(c.inherited_from_task_id,0) > 0 THEN c.reason
           ELSE c.reason || '（继承自任务 #' || c.task_id || '）'
       END,
       NULL, c.metrics, 'inherit',
       CASE WHEN COALESCE(c.inherited_from_task_id,0) > 0 THEN c.inherited_from_task_id ELSE c.task_id END,
       'inherited', c.observed_at
FROM src_checks c
JOIN target t ON t.id = c.asset_id
WHERE c.task_id = $2 AND c.state = 'done'
  AND c.observed_at IS NOT NULL AND c.observed_at > now() - interval '30 days'
ON CONFLICT (task_id, asset_id, check_key) DO NOTHING`, toTaskID, fromTaskID)
	if err != nil {
		return out, err
	}
	if n, err := res.RowsAffected(); err == nil {
		out.Checks = int(n)
	}
	if err := tx.Commit(); err != nil {
		return SrcInheritResult{}, err
	}
	return out, nil
}

// PriorSrcCell 是同一资产在其他 SRC 任务里的最近终态（跨任务复用提示）。
type PriorSrcCell struct {
	AssetID   int64     `json:"asset_id"`
	TaskID    int64     `json:"task_id"`
	Area      string    `json:"area"`
	Item      string    `json:"item"`
	State     string    `json:"state"`
	Reason    string    `json:"reason,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PriorSrcCoverage returns the most recent terminal cell per (asset, area, item)
// from OTHER SRC tasks, restricted to the assets in the current task and a time
// window. It is a read-only hint: callers can surface it to the planner (or let
// creation-time coverage inheritance copy it), but it never changes this task's
// cells by itself.
func (s *SrcStore) PriorSrcCoverage(ctx context.Context, taskID int64, assetIDs []int64, since time.Time, limit int) ([]PriorSrcCell, error) {
	if s == nil || s.db == nil || taskID <= 0 || len(assetIDs) == 0 {
		return []PriorSrcCell{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT ON (c.asset_id, c.area, c.item)
       c.asset_id, c.task_id, c.area, c.item, c.state, c.reason, c.updated_at
FROM src_cells c
JOIN tasks t ON t.id = c.task_id
WHERE c.task_id <> $1
  AND c.asset_id = ANY($2::bigint[])
  AND t.mode = 'src' AND t.deleted_at IS NULL
  AND COALESCE(c.observed_at, c.updated_at) >= $3
  AND c.state IN ('tested_clear','tested_found','na')
ORDER BY c.asset_id, c.area, c.item, c.updated_at DESC
LIMIT $4`, taskID, assetIDs, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PriorSrcCell{}
	for rows.Next() {
		var c PriorSrcCell
		if err := rows.Scan(&c.AssetID, &c.TaskID, &c.Area, &c.Item, &c.State, &c.Reason, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteSrcCells removes terminal cells for one (task, asset, area). item=="" removes
// every item of the area. Used by the human review queue to "退回重测": deleting a
// row turns it back into a blank, which the收尾门 will report on the next check.
func (s *SrcStore) DeleteSrcCells(ctx context.Context, taskID, assetID int64, area, item string) (int64, error) {
	if s == nil || s.db == nil || taskID <= 0 || assetID <= 0 || area == "" {
		return 0, fmt.Errorf("invalid src cell delete")
	}
	res, err := s.db.ExecContext(ctx, `
DELETE FROM src_cells
WHERE task_id=$1 AND asset_id=$2 AND area=$3 AND ($4='' OR item=$4)`, taskID, assetID, area, item)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// StaleInheritedCells returns inherited cells whose source row disappeared,
// changed away from tested_clear, or was updated after the inheritance copy.
// It is a read-time invalidation signal; it never silently deletes the row.
func (s *SrcStore) StaleInheritedCells(ctx context.Context, taskID int64) (map[string]bool, error) {
	out := map[string]bool{}
	if s == nil || s.db == nil || taskID <= 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT c.asset_id, c.area, c.item
FROM src_cells c
LEFT JOIN src_cells src
  ON src.task_id=c.inherited_from_task_id AND src.asset_id=c.asset_id
 AND src.area=c.area AND src.item=c.item
WHERE c.task_id=$1 AND c.inherited_from_task_id > 0
  AND (src.id IS NULL OR src.state <> 'tested_clear' OR src.updated_at > c.created_at)`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var assetID int64
		var area, item string
		if err := rows.Scan(&assetID, &area, &item); err != nil {
			return nil, err
		}
		out[strconv.FormatInt(assetID, 10)+"|"+area+"|"+item] = true
	}
	return out, rows.Err()
}
