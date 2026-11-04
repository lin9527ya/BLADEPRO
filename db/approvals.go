package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 统一审批闸（移植自 quiver approvals）：知识 / 全局弹药 / 厂商档案 / 定级规则
// 四类提案先进 pending，人工批准后由调用方落库（side effect 在 server 层做，
// 保证不同 kind 的落库逻辑可测、可回滚）。
const (
	ApprovalKindKnowledge     = "knowledge"
	ApprovalKindAmmoGlobal    = "ammo_global"
	ApprovalKindVendorProfile = "vendor_profile"
	ApprovalKindRatingRule    = "rating_rule"
)

// ApprovalKinds 是合法 kind（写入白名单）。
var ApprovalKinds = []string{
	ApprovalKindKnowledge, ApprovalKindAmmoGlobal, ApprovalKindVendorProfile, ApprovalKindRatingRule,
}

// Approval 是 approvals 表的一行。
type Approval struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	Reason    string          `json:"reason,omitempty"`
	Status    string          `json:"status"`
	EntryID   int64           `json:"entry_id,omitempty"`
	DecidedBy string          `json:"decided_by,omitempty"`
	DecidedAt *time.Time      `json:"decided_at,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

const approvalCols = `id, kind, payload, reason, status, entry_id, COALESCE(decided_by,''), decided_at, created_at`

func scanApproval(sc interface {
	Scan(dest ...any) error
}) (*Approval, error) {
	var a Approval
	var payload []byte
	var decided sql.NullTime
	if err := sc.Scan(&a.ID, &a.Kind, &payload, &a.Reason, &a.Status, &a.EntryID, &a.DecidedBy, &decided, &a.CreatedAt); err != nil {
		return nil, err
	}
	a.Payload = json.RawMessage(payload)
	if decided.Valid {
		a.DecidedAt = &decided.Time
	}
	return &a, nil
}

// CreateApproval 写入一条待审批提案。
func (s *AssetStore) CreateApproval(kind string, payload json.RawMessage, reason string) (*Approval, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	valid := false
	for _, k := range ApprovalKinds {
		if k == kind {
			valid = true
		}
	}
	if !valid {
		return nil, fmt.Errorf("未知审批类型 %q", kind)
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	row := s.db.QueryRow(`INSERT INTO approvals(kind, payload, reason)
		VALUES ($1,$2,$3) RETURNING `+approvalCols, kind, []byte(payload), strings.TrimSpace(reason))
	return scanApproval(row)
}

// ListApprovals 按状态列出（空状态 = pending）。
func (s *AssetStore) ListApprovals(status string, limit int) ([]Approval, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "pending"
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT `+approvalCols+` FROM approvals WHERE status=$1 ORDER BY id DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// GetApproval 按 id 取一条。
func (s *AssetStore) GetApproval(id int64) (*Approval, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	row := s.db.QueryRow(`SELECT `+approvalCols+` FROM approvals WHERE id=$1`, id)
	a, err := scanApproval(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

// DecideApproval 把 pending 提案置为 approved/rejected。
// changed=false 表示该提案已被处理过（幂等：不重复执行 side effect）。
func (s *AssetStore) DecideApproval(id int64, approve bool, decidedBy string) (*Approval, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, fmt.Errorf("数据库未初始化")
	}
	status := "rejected"
	if approve {
		status = "approved"
	}
	row := s.db.QueryRow(`UPDATE approvals SET status=$2, decided_by=$3, decided_at=now()
		WHERE id=$1 AND status='pending' RETURNING `+approvalCols, id, status, strings.TrimSpace(decidedBy))
	a, err := scanApproval(row)
	if err == sql.ErrNoRows {
		cur, gerr := s.GetApproval(id)
		if gerr != nil {
			return nil, false, gerr
		}
		return cur, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return a, true, nil
}

// MarkApprovalEntry 记录审批落库后的知识条目 id（供 UI 跳转）。
func (s *AssetStore) MarkApprovalEntry(id, entryID int64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	_, err := s.db.Exec(`UPDATE approvals SET entry_id=$2 WHERE id=$1`, id, entryID)
	return err
}
