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
	ID             int64     `json:"id"`
	TaskID         int64     `json:"task_id"`
	AssetID        int64     `json:"asset_id"`
	Asset          string    `json:"asset,omitempty"` // 资产标签（url/domain/ip…），只读展示用
	AssetType      string    `json:"asset_type,omitempty"`
	Area           string    `json:"area"`
	Item           string    `json:"item"`
	State          string    `json:"state"`
	Reason         string    `json:"reason,omitempty"`
	EvidenceNodeID int64     `json:"evidence_node_id,omitempty"`
	FindingID      int64     `json:"finding_id,omitempty"`
	Attempts       int       `json:"attempts"`
	UpdatedBy      string    `json:"updated_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// SrcAsset is one in-scope asset that the SRC gate must account for.
type SrcAsset struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
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
c.area, c.item, c.state, c.reason,
COALESCE(c.evidence_node_id,0), COALESCE(c.finding_id,0),
c.attempts, c.updated_by, c.created_at, c.updated_at`

func scanSrcCell(sc interface{ Scan(...any) error }) (*SrcCell, error) {
	var c SrcCell
	if err := sc.Scan(&c.ID, &c.TaskID, &c.AssetID, &c.Asset, &c.AssetType,
		&c.Area, &c.Item, &c.State, &c.Reason,
		&c.EvidenceNodeID, &c.FindingID,
		&c.Attempts, &c.UpdatedBy, &c.CreatedAt, &c.UpdatedAt); err != nil {
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
INSERT INTO src_cells(task_id, asset_id, area, item, state, reason, evidence_node_id, finding_id, attempts, updated_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,1,$9)
ON CONFLICT (task_id, asset_id, area, item) DO UPDATE SET
    state            = EXCLUDED.state,
    reason           = EXCLUDED.reason,
    evidence_node_id = COALESCE(EXCLUDED.evidence_node_id, src_cells.evidence_node_id),
    finding_id       = COALESCE(EXCLUDED.finding_id, src_cells.finding_id),
    attempts         = src_cells.attempts + 1,
    updated_by       = EXCLUDED.updated_by
RETURNING id`, c.TaskID, c.AssetID, c.Area, item, c.State, c.Reason, ev, find, c.UpdatedBy)
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
SELECT t.id, t.type, t.label FROM target t
WHERE t.type IN ('root_domain','subdomain','ip','app','service','endpoint')
ORDER BY t.type, t.label`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcAsset{}
	for rows.Next() {
		var a SrcAsset
		if err := rows.Scan(&a.ID, &a.Type, &a.Label); err != nil {
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
SELECT t.id, t.type, t.label FROM target t WHERE t.id=$2`, taskID, assetID)
	var a SrcAsset
	if err := row.Scan(&a.ID, &a.Type, &a.Label); err != nil {
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
