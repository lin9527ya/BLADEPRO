package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

type SrcProbeRun struct {
	ID         string          `json:"id"`
	TaskID     int64           `json:"task_id"`
	AssetID    int64           `json:"asset_id"`
	NodeID     int64           `json:"node_id"`
	Kind       string          `json:"kind"`
	Target     string          `json:"target"`
	Params     json.RawMessage `json:"params"`
	ParamsHash string          `json:"params_hash"`
	ResultHash string          `json:"result_hash"`
	Metrics    map[string]int  `json:"metrics"`
	Complete   bool            `json:"complete"`
	Truncated  bool            `json:"truncated"`
	Result     json.RawMessage `json:"result"`
	CreatedAt  time.Time       `json:"created_at"`
	ExpiresAt  time.Time       `json:"expires_at"`
}

func (d *DB) RecordSrcProbeRun(ctx context.Context, r *SrcProbeRun) error {
	if r == nil || r.ID == "" || r.TaskID <= 0 || r.AssetID <= 0 {
		return errors.New("invalid probe receipt")
	}
	sum := sha256.Sum256(r.Params)
	r.ParamsHash = hex.EncodeToString(sum[:])
	sum = sha256.Sum256(r.Result)
	r.ResultHash = hex.EncodeToString(sum[:])
	metrics, err := json.Marshal(r.Metrics)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"summary": fmt.Sprintf("只读探针 %s：%s", r.Kind, r.Target), "probe_run_id": r.ID, "result_hash": r.ResultHash, "metrics": r.Metrics, "complete": r.Complete, "truncated": r.Truncated})
	if err != nil {
		return err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var expID int64
	if err := tx.QueryRowContext(ctx, `SELECT exploration_id FROM tasks WHERE id=$1 AND mode='src' AND deleted_at IS NULL`, r.TaskID).Scan(&expID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO exploration_nodes(exploration_id,kind,payload,priority,state,origin) VALUES($1,'fact',$2,1,'confirmed','src_probe') RETURNING id`, expID, payload).Scan(&r.NodeID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO exploration_anchors(node_id,asset_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, r.NodeID, r.AssetID); err != nil {
		return err
	}
	returnRow := tx.QueryRowContext(ctx, `INSERT INTO src_probe_runs(id,task_id,asset_id,node_id,kind,target,params,params_hash,result_hash,metrics,complete,truncated,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING created_at,expires_at`, r.ID, r.TaskID, r.AssetID, r.NodeID, r.Kind, r.Target, r.Params, r.ParamsHash, r.ResultHash, metrics, r.Complete, r.Truncated, r.Result)
	if err := returnRow.Scan(&r.CreatedAt, &r.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SrcStore) ValidateProbeCheck(ctx context.Context, c SrcCheck) error {
	kinds := map[string]string{"paths_enum": "paths", "js_enum": "js", "method_matrix": "methods", "non404_retest": "non404"}
	expected, ok := kinds[c.CheckKey]
	if !ok {
		return errors.New("此收尾项不接受探针凭据")
	}
	var kind string
	var nodeID int64
	var complete, truncated bool
	var metrics []byte
	err := s.db.QueryRowContext(ctx, `SELECT r.kind,r.node_id,r.complete,r.truncated,r.metrics FROM src_probe_runs r JOIN exploration_nodes n ON n.id=r.node_id
 WHERE r.id=$1 AND r.task_id=$2 AND r.asset_id=$3 AND r.expires_at>now() AND n.kind='fact' AND n.state='confirmed'
 AND n.payload->>'probe_run_id'=r.id AND n.payload->>'result_hash'=r.result_hash`, c.ProbeRunID, c.TaskID, c.AssetID).Scan(&kind, &nodeID, &complete, &truncated, &metrics)
	if err != nil {
		return fmt.Errorf("探针凭据不存在、已过期或不属于本任务/资产：%w", err)
	}
	if kind != expected || nodeID != c.EvidenceNodeID || !complete || truncated {
		return errors.New("探针凭据类型/证据不匹配，或执行未完整完成")
	}
	var actual map[string]int
	if err := json.Unmarshal(metrics, &actual); err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, c.Metrics) {
		return errors.New("登记指标与宿主探针凭据不一致")
	}
	return nil
}

func (s *SrcStore) FactEvidenceForAsset(ctx context.Context, taskID, assetID, nodeID int64) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM exploration_nodes n JOIN tasks t ON t.exploration_id=n.exploration_id JOIN exploration_anchors a ON a.node_id=n.id WHERE t.id=$1 AND a.asset_id=$2 AND n.id=$3 AND n.kind='fact' AND n.state IN ('observed','confirmed'))`, taskID, assetID, nodeID).Scan(&ok)
	return ok, err
}

func (s *SrcStore) CheckVerified(ctx context.Context, c SrcCheck) error {
	if c.Verification == "inherited" {
		if c.ObservedAt != nil && time.Since(*c.ObservedAt) <= 30*24*time.Hour {
			return nil
		}
		return errors.New("继承证据已过期或缺少观察时间，需要重测")
	}
	if c.Verification == "probe_verified" {
		return s.ValidateProbeCheck(ctx, c)
	}
	if c.Verification == "artifact_verified" {
		a, err := s.ScreenshotArtifactForNode(ctx, c.TaskID, c.EvidenceNodeID)
		if err != nil {
			return err
		}
		if a == nil {
			return errors.New("截图证据缺失")
		}
		return nil
	}
	if c.Verification == "manual_approved" && c.ReviewNote != "" {
		ok, err := s.FactEvidenceForAsset(ctx, c.TaskID, c.AssetID, c.EvidenceNodeID)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return errors.New("收尾项尚无可信执行凭据或人工核验，不能算已验证完成")
}

// Only the authenticated human review path calls this; no model tool accepts a
// verification status. The current row version is pinned by its queue ref_key.
func (s *SrcStore) ApproveSrcCheck(ctx context.Context, taskID, assetID int64, key, ref, note string) error {
	note = strings.TrimSpace(note)
	if note == "" {
		return errors.New("人工核验必须填写实际核对依据")
	}
	row, err := s.GetSrcCheck(ctx, taskID, assetID, key)
	if err != nil {
		return err
	}
	if row == nil || row.State != SrcCheckDone {
		return errors.New("收尾项不存在或不是待核验 done")
	}
	if ref != fmt.Sprintf("check_unverified:%d:%d", row.ID, row.UpdatedAt.UnixNano()) {
		return errors.New("收尾项已变化，请刷新后重新核验")
	}
	ok, err := s.FactEvidenceForAsset(ctx, taskID, assetID, row.EvidenceNodeID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("证据不是对应资产的事实节点")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE src_checks SET verification='manual_approved',review_note=$5,updated_by='human-review' WHERE task_id=$1 AND asset_id=$2 AND check_key=$3 AND updated_at=$4 AND state='done'`, taskID, assetID, key, row.UpdatedAt, note)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("收尾项并发变更，核验未生效")
	}
	return nil
}

func (s *SrcStore) ProbeRun(ctx context.Context, taskID int64, id string) (*SrcProbeRun, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT to_jsonb(r) FROM src_probe_runs r WHERE r.id=$1 AND r.task_id=$2`, id, taskID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var run SrcProbeRun
	if err := json.Unmarshal(raw, &run); err != nil {
		return nil, err
	}
	return &run, nil
}
