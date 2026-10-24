package agent

// SRC 交付面（P2）的两个工具 + 与宿主之间的契约。
//
// src_prepare_submission 的**判定逻辑在宿主**（server）里：它要读绑定流量的请求/响应
// 原文、任务范围、台账与元数据。agent 侧只负责把"谁想交付哪条发现、正文写了什么"
// 交给它。因此这里定义纯数据的请求/结果类型，由宿主注入实现（见 SetSrcPreparer）——
// 与 execService / findingRecorder 同一套接线方式。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	actool "github.com/lin9527ya/Norma/tool"
	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/report"
)

// SrcPrepareRequest 是"把一条发现准备成可提交件"的输入。
type SrcPrepareRequest struct {
	TaskID      int64  `json:"task_id"`
	FindingID   int64  `json:"finding_id"`
	Platform    string `json:"platform,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Impact      string `json:"impact,omitempty"`
	Fix         string `json:"fix,omitempty"`
}

// SrcDuplicateRef 是查重命中的一条（可能是别的任务里的同一个洞）。
type SrcDuplicateRef struct {
	TaskID           int64  `json:"task_id"`
	TaskName         string `json:"task_name,omitempty"`
	FindingID        int64  `json:"finding_id"`
	Title            string `json:"title"`
	Severity         string `json:"severity"`
	Status           string `json:"status"`
	Platform         string `json:"platform,omitempty"`
	ExternalID       string `json:"external_id,omitempty"`
	SubmissionStatus string `json:"submission_status,omitempty"`
}

// SrcPrepareResult 是准备结果：落库的台账 + 问题清单 + 查重命中 + 渲染好的正文。
type SrcPrepareResult struct {
	SubmissionID int64             `json:"submission_id"`
	Status       string            `json:"status"`
	Fingerprint  string            `json:"fingerprint"`
	ReportReady  bool              `json:"report_ready"`
	Issues       []string          `json:"issues,omitempty"`
	Duplicates   []SrcDuplicateRef `json:"duplicates,omitempty"`
	// Suspects 是低置信相似发现（无流量时的降级指纹命中）：只提示，不自动定案。
	Suspects   []SrcDuplicateRef `json:"suspects,omitempty"`
	DegradedFP string            `json:"degraded_fingerprint,omitempty"`
	DedupeOf   int64             `json:"dedupe_of,omitempty"`
	Markdown   string            `json:"markdown,omitempty"`
	GapsTotal  int               `json:"gaps_total"`
	Finishable bool              `json:"finishable"`
	Note       string            `json:"note,omitempty"`
}

// SrcPreparer is the host implementation of submission preparation.
type SrcPreparer func(ctx context.Context, req SrcPrepareRequest) (*SrcPrepareResult, error)

// SetSrcPreparer wires the host-side submission preparation (nil = tool reports
// "未接线" instead of silently doing nothing).
func (t *ToolSet) SetSrcPreparer(fn SrcPreparer) { t.srcPreparer = fn }

// 三个 agent 各自的接入口：它们每次 run 新建 ToolSet，准备实现要存在 agent 结构体上
// （与 findingRecorder / execService 同一套接法）。
func (p *Planner) SetSrcPreparer(fn SrcPreparer)   { p.srcPreparer = fn }
func (w *Worker) SetSrcPreparer(fn SrcPreparer)    { w.srcPreparer = fn }
func (m *MainAgent) SetSrcPreparer(fn SrcPreparer) { m.srcPreparer = fn }

// SetSrcVerifier 同上：流量核验实现也要在每个 run 新建的 ToolSet 上重新挂。
func (p *Planner) SetSrcVerifier(fn SrcVerifier)   { p.srcVerifier = fn }
func (w *Worker) SetSrcVerifier(fn SrcVerifier)    { w.srcVerifier = fn }
func (m *MainAgent) SetSrcVerifier(fn SrcVerifier) { m.srcVerifier = fn }

// srcPrepareSubmission 把一条发现准备成台账行：校验正文、跑证据合规、算指纹、查重、
// 渲染八段正文并落库。它**不是**"导出下载"——导出由 HTTP 面负责（含整包原文）。
func (t *ToolSet) srcPrepareSubmission() actool.CoreTool {
	return writeTool(
		"src_prepare_submission",
		"【SRC 交付】把一条发现准备成可提交件（台账 draft/ready），并当场校验：\n"+
			"  · 正文：描述/危害/修复必须写全，出现「待补」这类占位符直接拒绝；\n"+
			"  · 证据合规：中危及以上要有 proof/verification 绑定；Content-Length 不得大于实际正文；"+
			"同一请求标识头（x-request-id 等）不得跨步骤重复；主机必须在任务范围内；\n"+
			"  · 查重：按「漏洞类型 + 归一化端点」算指纹，跨任务命中会写 dedupe_of 并挡在收尾门前；\n"+
			"  · 独立复验：中危及以上必须有 verdict=reproduced 的独立复验（首次准备会自动发起，等结论回来后再准备一次）。\n"+
			"issues 非空 = 还不能交（台账落 draft，按清单补齐后重调）。issues 为空 = ready。\n"+
			"描述可省略（回落到该发现的报告/摘要，但仍要过占位符检查）。",
		obj(map[string]any{
			"finding_id":  intp("要交付的发现 id（report_finding 返回的 id / list_findings 里取）"),
			"description": str("漏洞描述：第一人称讲做成了什么（省略则用发现的报告/摘要）"),
			"impact":      str("漏洞危害：每种伤害单独一行，写最终伤害"),
			"fix":         str("修复建议：贴合本漏洞、可落地"),
			"platform":    str("提交平台标识（可选，默认空 = 本任务自己的台账）"),
			"title":       str("漏洞标题（可选，默认用发现标题）"),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !t.srcReady() {
				return actool.Errorf("src_prepare_submission 仅在 SRC 模式的任务内可用"), nil
			}
			if t.srcPreparer == nil {
				return actool.Errorf("src_prepare_submission 未接线（宿主没有注入交付准备实现）"), nil
			}
			var req SrcPrepareRequest
			if err := json.Unmarshal(in, &req); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			if req.FindingID <= 0 {
				return actool.Errorf("必须提供 finding_id"), nil
			}
			req.TaskID = t.taskID
			res, err := t.srcPreparer(ctx, req)
			if err != nil {
				return actool.Errorf("准备提交失败：" + err.Error()), nil
			}
			if res == nil {
				return actool.Errorf("准备提交没有返回结果"), nil
			}
			return jsonResult(res)
		})
}

// srcDedupe 查重与定案：check 只读；mark 把"这条是 #N 的重复"写进元数据；clear 解除。
func (t *ToolSet) srcDedupe() actool.CoreTool {
	return writeTool(
		"src_dedupe",
		"【SRC 交付】按取证指纹（漏洞类型 + 归一化端点）查重，或给一条发现定案：\n"+
			"  check   —— 返回指纹与跨任务命中的同类发现（默认）；\n"+
			"  mark    —— 确认「这条是 of_finding_id 的重复」，写 dedupe_of（收尾门会要求台账标 duplicate）；\n"+
			"  clear   —— 解除 dedupe_of（人工确认「不是同一个洞」）。\n"+
			"指纹优先用绑定流量（类型 + 归一化端点，对象 ID 已折叠）；没有流量时用类型 + 资产 host/路径 + 参数名给低置信疑似重复（suspects，只提示，不自动定案）。",
		obj(map[string]any{
			"finding_id":    intp("要查重/定案的发现 id"),
			"action":        str("check（默认）| mark | clear"),
			"of_finding_id": intp("mark 时必填：它和哪条是同一个洞"),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !t.srcReady() {
				return actool.Errorf("src_dedupe 仅在 SRC 模式的任务内可用"), nil
			}
			var req struct {
				FindingID   int64  `json:"finding_id"`
				Action      string `json:"action"`
				OfFindingID int64  `json:"of_finding_id"`
			}
			if err := json.Unmarshal(in, &req); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			if req.FindingID <= 0 {
				return actool.Errorf("必须提供 finding_id"), nil
			}
			action := strings.TrimSpace(req.Action)
			if action == "" {
				action = "check"
			}
			switch action {
			case "mark":
				if req.OfFindingID <= 0 {
					return actool.Errorf("mark 必须提供 of_finding_id"), nil
				}
				if req.OfFindingID == req.FindingID {
					return actool.Errorf("不能把一条发现标成它自己的重复"), nil
				}
			case "clear":
				if err := t.srcStore.SetSrcDedupeOf(ctx, req.FindingID, 0); err != nil {
					return actool.Errorf("解除 dedupe_of 失败：" + err.Error()), nil
				}
				return jsonResult(map[string]any{"finding_id": req.FindingID, "dedupe_of": 0, "note": "已解除重复标记"})
			case "check":
			default:
				return actool.Errorf("未知 action：" + action + "（可选 check/mark/clear）"), nil
			}

			f, err := t.srcStore.SrcFinding(ctx, req.FindingID)
			if err != nil {
				return actool.Errorf("读取发现失败：" + err.Error()), nil
			}
			if f == nil {
				return actool.Errorf(fmt.Sprintf("发现 #%d 不存在", req.FindingID)), nil
			}
			if f.TaskID != nil && *f.TaskID != t.taskID {
				return actool.Errorf(fmt.Sprintf("发现 #%d 不属于本任务，不能在这里定案", req.FindingID)), nil
			}
			meta, err := t.srcStore.GetSrcFindingMeta(ctx, req.FindingID)
			if err != nil {
				return actool.Errorf("读取元数据失败：" + err.Error()), nil
			}
			fingerprint := ""
			if meta != nil {
				fingerprint = meta.Fingerprint
			}
			if fingerprint == "" {
				fingerprint = t.srcFingerprintFor(ctx, f)
			}
			dupes, err := t.srcStore.SrcDuplicatesByFingerprint(ctx, fingerprint, req.FindingID, 20)
			if err != nil {
				return actool.Errorf("查重失败：" + err.Error()), nil
			}
			out := map[string]any{
				"finding_id":  req.FindingID,
				"fingerprint": fingerprint,
				"duplicates":  toSrcDuplicateRefs(dupes),
			}
			// 没有绑定流量时给低置信提示：类型 + 资产 host/路径 + 参数名。它不写
			// dedupe_of，也不进交付门；agent/人自己决定要不要 mark。
			degraded := ""
			if fingerprint == "" {
				degraded = t.srcDegradedFingerprintFor(ctx, f)
				if degraded != "" {
					suspects, sErr := t.srcStore.SrcSuspectsByDegradedFingerprint(ctx, degraded, req.FindingID, 20)
					if sErr != nil {
						return actool.Errorf("低置信查重失败：" + sErr.Error()), nil
					}
					out["degraded_fingerprint"] = degraded
					out["suspects"] = toSrcDuplicateRefs(suspects)
				}
			}
			if meta != nil && meta.DedupeOf > 0 {
				out["dedupe_of"] = meta.DedupeOf
			}
			if action == "mark" {
				if err := t.srcStore.SetSrcDedupeOf(ctx, req.FindingID, req.OfFindingID); err != nil {
					return actool.Errorf("写 dedupe_of 失败：" + err.Error()), nil
				}
				out["dedupe_of"] = req.OfFindingID
				out["note"] = "已登记重复：把台账状态标为 duplicate 后收尾门才会放行"
			}
			if fingerprint == "" {
				if degraded != "" {
					out["note"] = "该发现没有绑定流量：只能用降级指纹给低置信疑似重复（不自动定案）。确认是同一个洞请手动 mark duplicate；要精确查重请先绑证据。"
				} else {
					out["note"] = "该发现没有绑定流量，也算不出降级指纹（缺资产 URL）：无法查重（先绑证据）"
				}
			}
			return jsonResult(out)
		})
}

// srcFingerprintFor 现算一条发现的指纹（没有绑定流量时为空）。
func (t *ToolSet) srcFingerprintFor(ctx context.Context, f *db.DBFinding) string {
	traffic, err := t.srcStore.SrcFindingTraffic(ctx, f.ID)
	if err != nil || traffic == nil {
		return ""
	}
	refs := make([]report.EndpointRef, 0, len(traffic.Bindings))
	for _, b := range traffic.Bindings {
		refs = append(refs, report.EndpointRef{Method: b.Snapshot.Method, URL: b.Snapshot.URL})
	}
	return report.Fingerprint(f.VulnClass, refs)
}

// srcDegradedFingerprintFor 用发现关联的第一个可解析资产算降级指纹。
func (t *ToolSet) srcDegradedFingerprintFor(ctx context.Context, f *db.DBFinding) string {
	if t.as == nil || f == nil || len(f.AssetIDs) == 0 {
		return ""
	}
	assets, err := t.as.GetByIDs(f.AssetIDs)
	if err != nil {
		return ""
	}
	for _, a := range assets {
		if a == nil {
			continue
		}
		if u := a.TargetURL(); u != "" {
			return report.DegradedFingerprint(f.VulnClass, u, a.ParamNames())
		}
	}
	return ""
}

func toSrcDuplicateRefs(in []db.SrcDuplicate) []SrcDuplicateRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]SrcDuplicateRef, 0, len(in))
	for _, d := range in {
		out = append(out, SrcDuplicateRef{
			TaskID: d.TaskID, TaskName: d.TaskName, FindingID: d.FindingID,
			Title: d.Title, Severity: d.Severity, Status: d.Status,
			Platform: d.Platform, ExternalID: d.ExternalID, SubmissionStatus: d.SubmissionSt,
		})
	}
	return out
}
