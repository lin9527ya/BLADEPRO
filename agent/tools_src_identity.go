package agent

import (
	"context"
	"encoding/json"
	"strings"

	actool "github.com/lin9527ya/Norma/tool"
)

// SrcIdentityRequest is one read-only identity differential on the same asset.
type SrcIdentityRequest struct {
	TaskID  int64  `json:"-"`
	AssetID int64  `json:"asset_id"`
	Path    string `json:"path"`
	// Paths 是批量模式：一次对照多条同 host 路径（上限 20）。与 Path 二选一。
	Paths     []string `json:"paths,omitempty"`
	IdentityA string   `json:"identity_a"`
	IdentityB string   `json:"identity_b"`
}

// SrcIdentityObservation intentionally carries no request/response headers or
// bodies: the credential is host-side only, and response bodies may contain
// other tenants' data.
type SrcIdentityObservation struct {
	Label  string `json:"label"`
	Role   string `json:"role,omitempty"`
	Tenant string `json:"tenant,omitempty"`
	Status int    `json:"status,omitempty"`
	Length int64  `json:"length,omitempty"`
	SHA256 string `json:"body_sha256,omitempty"`
	Error  string `json:"error,omitempty"`
}

// SrcIdentityResult is the structured comparison returned to the model.
type SrcIdentityResult struct {
	AssetID        int64                  `json:"asset_id"`
	URL            string                 `json:"url"`
	Path           string                 `json:"path,omitempty"`
	A              SrcIdentityObservation `json:"identity_a"`
	B              SrcIdentityObservation `json:"identity_b"`
	Distinct       bool                   `json:"distinct"`
	EvidenceNodeID int64                  `json:"evidence_node_id"`
	Note           string                 `json:"note"`
	// Error 是批量模式下该条路径失败的原因（单条模式直接返回 error）。
	Error string `json:"error,omitempty"`
	// Items / DistinctCount 只在批量模式（paths）下有值。
	Items         []SrcIdentityResult `json:"items,omitempty"`
	DistinctCount int                 `json:"distinct_count,omitempty"`
}

type SrcIdentityDiffer func(ctx context.Context, req SrcIdentityRequest) (*SrcIdentityResult, error)

func (t *ToolSet) SetSrcIdentityDiffer(fn SrcIdentityDiffer) { t.srcIdentityDiffer = fn }
func (p *Planner) SetSrcIdentityDiffer(fn SrcIdentityDiffer) { p.srcIdentityDiffer = fn }
func (w *Worker) SetSrcIdentityDiffer(fn SrcIdentityDiffer)  { w.srcIdentityDiffer = fn }

// SetSrcHostCapabilities wires the host-side SRC capabilities as one set. Keeping
// them together prevents a new capability from being added to the host but silently
// left unwired in one of the two agent loops (the identity tool had exactly that bug).
func (t *ToolSet) SetSrcHostCapabilities(preparer SrcPreparer, verifier SrcVerifier, prober SrcProber, screenshotter SrcScreenshotter, differ SrcIdentityDiffer) {
	t.SetSrcPreparer(preparer)
	t.SetSrcVerifier(verifier)
	t.SetSrcProber(prober)
	t.SetSrcScreenshotter(screenshotter)
	t.SetSrcIdentityDiffer(differ)
}

func (t *ToolSet) srcIdentityDiffTool() actool.CoreTool {
	return writeTool("src_identity_diff",
		"【SRC 只读身份对照】用平台授权的两个身份对同一资产、同一路径做 GET，比较状态/长度/正文摘要，落一条事实证据。\n"+
			"凭据由宿主从部署环境读取，永不进入模型上下文或报告；只支持读取型请求。\n"+
			"返回 only 结构化差异，不返回正文；distinct=true 只是线索，不能直接判越权，需结合对象归属与独立复验。",
		obj(map[string]any{
			"asset_id":   intp("资产 id（来自 src_state / list_assets）"),
			"path":       str("同 host 的路径，如 /api/users/1（可带查询串；不允许绝对 URL 或跨 host）；与 paths 二选一"),
			"paths":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "批量对照的路径列表（同 host 相对路径，最多 20 条）；与 path 二选一"},
			"identity_a": str("身份 A 的 label（来自 Program 的 test_identities）"),
			"identity_b": str("身份 B 的 label（来自 Program 的 test_identities）"),
		}, "asset_id", "identity_a", "identity_b"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !t.srcReady() {
				return actool.Errorf("src_identity_diff 仅在 SRC 模式的任务内可用"), nil
			}
			if t.srcIdentityDiffer == nil {
				return actool.Errorf("src_identity_diff 未接线（宿主没有注入身份对照实现）"), nil
			}
			var req SrcIdentityRequest
			if err := json.Unmarshal(in, &req); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			if req.AssetID <= 0 || strings.TrimSpace(req.IdentityA) == "" || strings.TrimSpace(req.IdentityB) == "" {
				return actool.Errorf("asset_id / identity_a / identity_b 必填"), nil
			}
			if strings.TrimSpace(req.Path) == "" && len(req.Paths) == 0 {
				return actool.Errorf("path 与 paths 至少给一个"), nil
			}
			if len(req.Paths) > 20 {
				return actool.Errorf("paths 一次最多 20 条"), nil
			}
			req.TaskID = t.taskID
			res, err := t.srcIdentityDiffer(ctx, req)
			if err != nil {
				return actool.Errorf("身份对照失败：" + err.Error()), nil
			}
			return jsonResult(res)
		})
}
