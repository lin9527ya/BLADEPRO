package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

// runSrcIdentityDiff performs two read-only requests with operator-authorized
// identities. Secrets are resolved from the deployment environment and never
// placed in the result, evidence payload, logs or model context.
func (s *Server) runSrcIdentityDiff(ctx context.Context, req agent.SrcIdentityRequest) (*agent.SrcIdentityResult, error) {
	if req.TaskID <= 0 || req.AssetID <= 0 {
		return nil, fmt.Errorf("需要 task_id 与 asset_id")
	}
	doc := s.srcDocForTask(req.TaskID)
	if !doc.IsSrc() {
		return nil, fmt.Errorf("该任务不是 SRC 模式")
	}
	if doc.RequireIsolation && !AgentIsolationEnabled() {
		return nil, fmt.Errorf("任务要求隔离运行，但当前实例未启用隔离")
	}
	if doc.NoAutomation {
		return nil, fmt.Errorf("平台禁止自动化请求：身份对照请走人工复核")
	}
	if strings.TrimSpace(doc.TimeWindow) != "" && !doc.InWindow(time.Now()) {
		return nil, fmt.Errorf("当前不在平台作业时间窗（%s）内", doc.TimeWindow)
	}
	idA, okA := doc.IdentityByLabel(req.IdentityA)
	idB, okB := doc.IdentityByLabel(req.IdentityB)
	if !okA || !okB {
		return nil, fmt.Errorf("身份 label 未在平台项目中配置（test_identities）")
	}
	if err := srcmode.ValidateTestIdentities([]srcmode.TestIdentity{idA, idB}); err != nil {
		return nil, err
	}
	credA, credB := strings.TrimSpace(os.Getenv(idA.CredentialEnv)), strings.TrimSpace(os.Getenv(idB.CredentialEnv))
	if strings.ContainsAny(credA, "\r\n") || strings.ContainsAny(credB, "\r\n") {
		return nil, fmt.Errorf("身份凭据包含非法换行字符")
	}
	if credA == "" || credB == "" {
		return nil, fmt.Errorf("身份凭据未在部署环境提供（credential_env）；不要把凭据写进程序或提示词")
	}
	store := s.m.PG().Src()
	asset, err := store.SrcAssetInScope(ctx, req.TaskID, req.AssetID)
	if err != nil || asset == nil {
		return nil, fmt.Errorf("资产不在本任务测试范围内")
	}
	base := srcProbeTargetURL(asset.Label)
	bu, err := url.Parse(base)
	if err != nil || bu.Hostname() == "" {
		return nil, fmt.Errorf("资产不是可请求的 http(s) URL")
	}
	rel, err := url.Parse(strings.TrimSpace(req.Path))
	if err != nil || rel.IsAbs() || rel.Host != "" {
		return nil, fmt.Errorf("path 必须是同 host 的相对路径，不能是绝对 URL")
	}
	target := bu.ResolveReference(rel)
	if !strings.EqualFold(target.Hostname(), bu.Hostname()) {
		return nil, fmt.Errorf("path 不能跳转到其他 host")
	}
	if inScope, decidable, err := store.SrcHostInScope(ctx, req.TaskID, target.Hostname()); err != nil || (decidable && !inScope) {
		return nil, fmt.Errorf("目标 host 不在任务授权范围内")
	}
	client, _ := s.srcProbeClient(req.TaskID)
	observe := func(id srcmode.TestIdentity, credential string) agent.SrcIdentityObservation {
		out := agent.SrcIdentityObservation{Label: id.Label, Role: id.Role, Tenant: id.Tenant}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			out.Error = "请求构造失败"
			return out
		}
		header := id.Header
		if header == "" {
			header = "Cookie"
		}
		httpReq.Header.Set(header, credential)
		httpReq.Header.Set("User-Agent", "bladepro-src-identity/1")
		resp, err := client.Do(httpReq)
		if err != nil {
			out.Error = "请求失败（未回包）"
			return out
		}
		defer resp.Body.Close()
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		out.Status = resp.StatusCode
		out.Length = int64(len(body))
		sum := sha256.Sum256(body)
		out.SHA256 = hex.EncodeToString(sum[:])
		if readErr != nil || len(body) > 1<<20 {
			out.Error = "响应正文未完整读取"
		}
		return out
	}
	a := observe(idA, credA)
	b := observe(idB, credB)
	distinct := a.Status != b.Status || a.Length != b.Length || a.SHA256 != b.SHA256
	res := &agent.SrcIdentityResult{AssetID: req.AssetID, URL: target.String(), A: a, B: b, Distinct: distinct,
		Note: "只读对照，仅比较状态/长度/正文摘要；distinct 只是线索，需结合对象归属并经独立复验，不能直接判越权。"}
	task, ok := s.m.Task(fmt.Sprintf("%d", req.TaskID))
	if ok && task.Store != nil {
		payload := map[string]any{
			"summary":    fmt.Sprintf("身份对照：%s（%s）vs %s（%s）%s", a.Label, a.Role, b.Label, b.Role, target.Path),
			"detail":     fmt.Sprintf("GET %s；A status=%d len=%d sha=%s；B status=%d len=%d sha=%s；distinct=%v。凭证由宿主环境提供，未进入本事实。", target.String(), a.Status, a.Length, shortHash(a.SHA256), b.Status, b.Length, shortHash(b.SHA256), distinct),
			"identity_a": map[string]any{"label": a.Label, "role": a.Role, "tenant": a.Tenant, "status": a.Status, "length": a.Length, "body_sha256": a.SHA256},
			"identity_b": map[string]any{"label": b.Label, "role": b.Role, "tenant": b.Tenant, "status": b.Status, "length": b.Length, "body_sha256": b.SHA256},
			"distinct":   distinct,
		}
		nodeID, err := task.Store.AddNode(db.KindFact, payload, 1, "confirmed", "src_identity", []int64{req.AssetID})
		if err == nil {
			res.EvidenceNodeID = nodeID
		}
	}
	return res, nil
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
