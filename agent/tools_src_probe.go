package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	actool "github.com/lin9527ya/Norma/tool"
	"github.com/lin9527ya/bladepro/srcprobe"
)

// SrcProbeRequest 是确定性只读探针的输入；AssetID 决定目标与范围。
type SrcProbeRequest struct {
	TaskID    int64
	AssetID   int64
	Kind      string
	MaxPaths  int
	Endpoints []string
	// URLs / MaxURLs 供 alive、fingerprint：在资产 URL 之外再测的同 host 相对路径。
	URLs    []string
	MaxURLs int
	// Identity is a Program test-identity label; the host resolves its credential
	// from the deployment environment. The value never enters the prompt/cache.
	Identity string
	// IdentityB 是 id_enum 的第二个身份（A=被验证方，B=有权读该对象的身份）。
	IdentityB string
	// Template / IDs 供 id_enum：template 含一个 {id} 占位符，IDs 最多 50 个。
	Template string
	IDs      []string
	// Force bypasses the asset-level result cache (paths/js/methods) and re-runs.
	Force bool
}

// SrcProber 由宿主（server）实现：解析资产的 URL/范围、走录制代理、按 QPS 跑探针。
type SrcProber func(ctx context.Context, req SrcProbeRequest) (*srcprobe.Result, error)

// SetSrcProber wires the host-side probe executor (nil = tool reports 未接线).
func (t *ToolSet) SetSrcProber(fn SrcProber) { t.srcProber = fn }

func (t *ToolSet) srcProbeTool() actool.CoreTool {
	return writeTool(
		"src_probe",
		"【SRC 只读探针】对一个资产跑确定性探针，把重复动作从 LLM 回合里拿掉。只发 GET/HEAD/OPTIONS，按任务 QPS 串行、走录制代理留证。\n"+
			"  paths   —— 用内置 API 路径字典枚举（默认 200 条，可调 max_paths）；结果给 paths/non404。\n"+
			"  js      —— 抽首页的内联脚本、同 host 外链 JS、chunk 与 sourcemap；结果给 js_inline/js_external/js_chunks/sourcemap。\n"+
			"  methods —— 对 endpoints（默认资产 URL）测 GET/HEAD/OPTIONS，记录状态与 Allow；状态变更方法不自动发。\n"+
			"  non404  —— 对本任务流量里该 host 的非 404 路由慢速复测 2 次；结果给 non404/retested。\n"+
			"  alive   —— 逐个 GET 目标（或 urls 里的同 host 路径），统计存活/状态码分布/有标题数；结果给 alive/status_2xx/titled。\n"+
			"  fingerprint —— 在 alive 基础上收集低成本指纹：响应头特征、favicon mmh3、robots.txt/sitemap.xml；结果给 tech/favicon/robots/sitemap。\n"+
			"  id_enum —— 用 template（含一个 {id} 的同 host 相对路径）对 ids 逐个 GET，每 ID 用 identity 与 identity_b 各取一次；\n"+
			"             按 same_body（A 也能读到同一对象，最可疑）/ diff_body / a_only / b_only / neither 归类，返回 id_candidates。只发 GET，最多 50 个 ID。\n"+
			"返回 metrics、probe_run_id、evidence_node_id；登记收尾项必须引用同一次可信执行凭据。缓存只作历史提示，需 force=true 获得当前任务的新鲜凭据。探针不会自动点格。\n"+
			"paths/js/methods 的结果按资产缓存 12h（跨任务复用），返回 cached=true 时是历史结果；需要新鲜数据传 force=true。",
		obj(map[string]any{
			"asset_id":   intp("要探测的资产 id（来自 src_state / list_assets）"),
			"kind":       map[string]any{"type": "string", "enum": []string{srcprobe.KindPaths, srcprobe.KindJS, srcprobe.KindMethods, srcprobe.KindNon404, srcprobe.KindAlive, srcprobe.KindFingerprint, srcprobe.KindIDEnum}, "description": "探针类型"},
			"max_paths":  intp("paths 探针最多跑多少条（默认 200，上限 1000）"),
			"endpoints":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "methods 探针的可选端点列表（URL 或相对路径，默认资产 URL）"},
			"urls":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "alive/fingerprint 探针的可选路径列表（同 host 相对路径，最多 100 条；默认只测资产 URL）"},
			"max_urls":   intp("alive/fingerprint 最多测多少条 URL（默认 100，上限 100）"),
			"force":      map[string]any{"type": "boolean", "description": "true = 跳过资产级枚举缓存重新跑（默认 false，12h 内的 paths/js/methods 结果会直接复用）"},
			"identity":   str("可选：使用 Program 中已授权的测试身份 label 发起认证请求；凭据由宿主从部署环境读取，模型不接触"),
			"identity_b": str("id_enum 必填：第二个测试身份 label（有权读该对象的身份）"),
			"template":   str("id_enum 必填：同 host 相对路径模板，含且仅含一个 {id}，如 /api/users/{id}"),
			"ids":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "id_enum 必填：要枚举的 ID 列表（最多 50 个，字符集限字母数字 _ . -）"},
		}, "asset_id", "kind"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !t.srcReady() {
				return actool.Errorf("src_probe 仅在 SRC 模式的任务内可用"), nil
			}
			if t.srcProber == nil {
				return actool.Errorf("src_probe 未接线（宿主未注入探针执行器）"), nil
			}
			var req struct {
				AssetID   int64    `json:"asset_id"`
				Kind      string   `json:"kind"`
				MaxPaths  int      `json:"max_paths"`
				Endpoints []string `json:"endpoints"`
				URLs      []string `json:"urls"`
				MaxURLs   int      `json:"max_urls"`
				Identity  string   `json:"identity"`
				IdentityB string   `json:"identity_b"`
				Template  string   `json:"template"`
				IDs       []string `json:"ids"`
				Force     bool     `json:"force"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			if req.AssetID <= 0 {
				return actool.Errorf("asset_id 必填且必须为正数"), nil
			}
			kind := strings.ToLower(strings.TrimSpace(req.Kind))
			switch kind {
			case srcprobe.KindPaths, srcprobe.KindJS, srcprobe.KindMethods, srcprobe.KindNon404,
				srcprobe.KindAlive, srcprobe.KindFingerprint, srcprobe.KindIDEnum:
			default:
				return actool.Errorf("kind 只能是 paths / js / methods / non404 / alive / fingerprint / id_enum"), nil
			}
			res, err := t.srcProber(ctx, SrcProbeRequest{
				TaskID: t.taskID, AssetID: req.AssetID, Kind: kind,
				MaxPaths: req.MaxPaths, Endpoints: req.Endpoints, URLs: req.URLs, MaxURLs: req.MaxURLs,
				Identity: req.Identity, IdentityB: req.IdentityB, Template: req.Template, IDs: req.IDs,
				Force: req.Force,
			})
			if err != nil {
				return actool.Errorf("探针失败：" + err.Error()), nil
			}
			t.writes.Evidence++
			return jsonResult(map[string]any{
				"probe":   res,
				"metrics": res.Metrics,
				"note":    fmt.Sprintf("只读探针完成：%d 个请求，用时 %dms；metrics 可直接用于 src_mark_check。", res.Requests, res.ElapsedMS),
			})
		})
}
