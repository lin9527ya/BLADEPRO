package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/evidence"
	"github.com/lin9527ya/bladepro/report"
	"github.com/lin9527ya/bladepro/srcmode"
)

// 交付面（P11）：把一条发现导出成 src-dig 报告规范的可提交件。
//
// 机器填它确知的部分——标题 / 目标 URL / 等级 / 涉及接口清单 / 复现整包（来自绑定
// 证据，敏感字段脱敏）——描述/危害/修复优先取 prepare 的三段，其次从 reporter 的
// “概述/影响与危害/修复建议”小节提取；完整 12 节详细报告不会塞进 8 块正文。
// 两种输出（Markdown / docx）共用同一份 report.SrcInput，版式一致。

func (s *Server) registerSrcExport(mux routeRegistrar) {
	mux.HandleFunc("GET /api/findings/{id}/src-report", s.getFindingSrcReport)
	mux.HandleFunc("GET /api/findings/{id}/src-report.docx", s.getFindingSrcReportDocx)
	// 单条交付包：md/docx + PoC + 该发现自己的截图，便于直接上传平台附件。
	mux.HandleFunc("GET /api/findings/{id}/src-report.zip", s.getFindingSrcReportZip)
	// 证据门（P15）：下载前先看"这份简报缺什么"。
	mux.HandleFunc("GET /api/findings/{id}/src-report/check", s.getFindingSrcReportCheck)
	// 提交预览：最终 Markdown + 平台字段 + 提交/复验问题，前端可直接逐项复制。
	mux.HandleFunc("GET /api/findings/{id}/src-preview", s.getFindingSrcPreview)
	// 批量交付（P19）：一个任务的全部发现 -> 计划预览 / 打包下载。
	mux.HandleFunc("GET /api/tasks/{id}/src-batch", s.getTaskSrcBatch)
	mux.HandleFunc("GET /api/tasks/{id}/src-report.zip", s.getTaskSrcReportZip)
}

func (s *Server) srcFinding(w http.ResponseWriter, r *http.Request) *db.DBFinding {
	id := int64(atoiDefault(r.PathValue("id"), 0))
	if id <= 0 {
		writeErr(w, 400, "bad finding id")
		return nil
	}
	f, err := s.m.pg.GetFinding(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return nil
	}
	if f == nil {
		writeErr(w, 404, "finding not found")
		return nil
	}
	return f
}

// srcMarkdownSection extracts one heading section from the reporter Markdown.
// It stops at the next heading of the same or higher level, so the 12-section
// internal report is never copied wholesale into the 8-block delivery body.
func srcMarkdownSection(md string, titles ...string) string {
	want := map[string]bool{}
	for _, t := range titles {
		want[strings.TrimSpace(t)] = true
	}
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	start, level := -1, 0
	inFence := false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(t, "#") {
			continue
		}
		n := 0
		for n < len(t) && t[n] == '#' {
			n++
		}
		title := strings.TrimSpace(strings.TrimLeft(t, "#"))
		if start < 0 {
			if want[title] {
				start, level = i+1, n
			}
			continue
		}
		if n <= level {
			return strings.TrimSpace(strings.Join(lines[start:i], "\n"))
		}
	}
	if start >= 0 {
		return strings.TrimSpace(strings.Join(lines[start:], "\n"))
	}
	return ""
}

// srcDeliveryDescription takes only the overview/summary, never the full
// reporter output. This is what the "漏洞描述" block is for; the complete
// investigation report belongs in the evidence package, not the platform form.
func srcDeliveryDescription(f *db.DBFinding) string {
	if f == nil {
		return ""
	}
	if s := srcMarkdownSection(f.Report, "概述"); s != "" {
		return s
	}
	if s := strings.TrimSpace(f.Summary); s != "" {
		return s
	}
	rep := strings.TrimSpace(f.Report)
	if rep != "" && !strings.Contains(rep, "\n#") && len([]rune(rep)) <= 1200 {
		return rep
	}
	return ""
}

func srcDeliveryImpact(f *db.DBFinding) string {
	if f == nil {
		return ""
	}
	return srcMarkdownSection(f.Report, "影响与危害")
}

func srcDeliveryFix(f *db.DBFinding) string {
	if f == nil {
		return ""
	}
	return srcMarkdownSection(f.Report, "修复建议")
}

// srcSubmissionAsset turns an asset label such as "GET https://host/path" or a
// bare hostname into the absolute http(s) URL a platform form expects. Query
// values are redacted so tokens do not leak into the form/report.
func srcSubmissionAsset(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	fields := strings.Fields(raw)
	candidates := []string{raw}
	for i := 1; i < len(fields); i++ {
		candidates = append(candidates, fields[i])
	}
	for _, c := range candidates {
		if u, err := url.Parse(c); err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") {
			return redactURL(c)
		}
	}
	if len(fields) == 1 && !strings.HasPrefix(raw, "/") {
		if u, err := url.Parse("https://" + raw); err == nil && u.Host != "" {
			return redactURL("https://" + raw)
		}
	}
	return ""
}

// buildSrcInput assembles the export skeleton from the finding row, its anchored
// assets and its bound traffic evidence.
func (s *Server) buildSrcInput(ctx context.Context, f *db.DBFinding) (report.SrcInput, []string) {
	in := report.SrcInput{
		Title:     report.FindingTitle(f),
		Severity:  f.Severity,
		VulnClass: f.VulnClass,
		// 影响面/定级建议：结构化字段直接进报告头部；没有就留空，不硬塞占位符。
		CWE: f.CWE, CVSSVector: f.CVSSVector, ImpactScope: f.ImpactScope,
		ReproEnv: f.ReproEnv, TestAccounts: f.TestAccounts,
		Description: srcDeliveryDescription(f),
		Impact:      srcDeliveryImpact(f),
		Fix:         srcDeliveryFix(f),
	}
	// P2：台账里写好的三段优先于发现的原始摘要/报告——那是为交付写的正文，不是探索
	// 过程的记录。没有台账时保持原行为（否则导出会缺正文）。
	if sub, err := s.m.PG().Src().GetSrcSubmissionByFinding(ctx, f.ID, ""); err == nil && sub != nil {
		if t := strings.TrimSpace(sub.Title); t != "" {
			in.Title = t
		}
		if d := strings.TrimSpace(sub.Description); d != "" {
			in.Description = d
		}
		if d := strings.TrimSpace(sub.Impact); d != "" {
			in.Impact = d
		}
		if d := strings.TrimSpace(sub.Fix); d != "" {
			in.Fix = d
		}
	}

	// 目标 URL：优先把资产展示标签归一成 http(s) 链接；不是 URL 时再回落到
	// 绑定流量里的真实 URL。平台表单的"资产/URL"不能是 "METHOD /path"。
	assets := s.resolveAssetIDs(f.AssetIDs)
	for _, aid := range f.AssetIDs {
		if a := assets[aid]; a != nil {
			if u := srcSubmissionAsset(assetLabel(a)); u != "" {
				in.TargetURL = u
				break
			}
		}
	}

	// warnings 是导出前的证据缺项清单（P15 交付证据门）。空 = 本发现可以直接交。
	traffic, err := s.m.pg.GetFindingTraffic(ctx, f.ID)
	if err != nil {
		traffic = nil
	}
	var roles []string
	if traffic != nil {
		for _, b := range traffic.Bindings {
			roles = append(roles, b.Role)
		}
	}
	warnings := s.srcWarnings(f, roles)
	store := s.evidenceStore()
	var stepNotes []string
	if traffic != nil {
		seen := map[string]bool{}
		stepNo := 0
		baselineNote := ""
		stepRedacted := false
		for _, b := range traffic.Bindings {
			sn := b.Snapshot
			redactedURL := redactURL(strings.TrimSpace(sn.URL))
			if in.TargetURL == "" {
				in.TargetURL = srcSubmissionAsset(sn.URL)
			}
			key := strings.TrimSpace(sn.Method + " " + redactedURL)
			if key != "" && !seen[key] {
				seen[key] = true
				in.Endpoints = append(in.Endpoints, report.SrcEndpoint{
					Method: sn.Method, URL: redactedURL, Source: srcBindingSource(b),
				})
			}
			if b.Role == "baseline" {
				// 规范：对照写进那一步的描述里，不单独成步。
				baselineNote = sn.Method + " " + redactedURL
				continue
			}
			if b.Role != "proof" && b.Role != "verification" {
				continue
			}
			stepNo++
			body := srcRequestBody(store, sn)
			raw, redacted := redactSrcRawRequest(sn.ReqHead, body)
			if redacted {
				stepRedacted = true
			}
			if body == "" && sn.ReqLen > 0 {
				// 正文没内联（二进制/过大/暂存缺失）时必须显式标注缩减：否则头里的
				// Content-Length 与交出去的正文对不上，平台会判"拼凑报文"。
				raw = strings.TrimRight(raw+"\n\n"+srcOmissionMarker, "\r\n")
			}
			raw = truncateSrcPacket(raw)
			desc := srcRoleLabel(b.Role)
			if n := strings.TrimSpace(b.Note); n != "" {
				desc += "：" + clipRunes(n, 160)
			}
			in.Steps = append(in.Steps, report.SrcStep{Desc: desc, Raw: raw})
		}
		if baselineNote != "" && len(in.Steps) > 0 {
			in.Steps[0].Desc += "（同接口正常对照：" + baselineNote + "，对照请求不单独成步）"
		}
		if stepRedacted {
			stepNotes = append(stepNotes, "提交报文中的 Cookie/Authorization/token 等敏感字段已脱敏；原始字节保留在内部证据库。")
		}
	}

	// 【提交信息】块：单条导出、批量包、交付准备共用同一份，平台等级优先。
	// 放在流量解析之后，确保资产/URL 已经是可提交的 http(s) 链接。
	if f.TaskID != nil && *f.TaskID > 0 {
		doc := s.srcDocForTask(*f.TaskID)
		in.SubmitBlock = s.srcSubmitBlock(ctx, *f.TaskID, f, in, doc)
	}

	notes := append([]string{"发现 #" + itoa64(f.ID) + " · 证据版本 v" + itoa64(f.EvidenceVersion) + " · 生成时间 " + time.Now().Format("2006-01-02 15:04:05")}, stepNotes...)
	if strings.TrimSpace(f.TaskDescription) != "" {
		notes = append(notes, "来源任务："+clipRunes(f.TaskDescription, 80))
	}
	// 证据门（P15）：缺项既进 Notes 顶部（人下载就看得见），也由 API 以 warnings[]
	// 单独返回（前端/自动化在下载前就能看到）。两者同一份来源，不会各说各话。
	for _, w := range warnings {
		notes = append(notes, "⚠ "+w)
	}
	in.Notes = append(notes, in.Notes...)
	// 严格版式是任务级快照：单条导出、批量包、prepare 必须同一口径。
	if f.TaskID != nil && *f.TaskID > 0 {
		if doc := s.srcDocForTask(*f.TaskID); doc.StrictLayout {
			in.Notes = warningNotes(in.Notes)
		}
	}
	return in, warnings
}

// srcExportIssues 是导出前的正文/成品校验：标题/描述/危害/修复是否写全、报文是否
// 可复现。导出不阻断（交不交由人决定），但问题必须跟着文件走——否则占位符报告会
// 被原样贴到平台，审核阶段才被打回。
func srcExportIssues(in report.SrcInput) []string {
	issues := append([]string{}, srcmode.ValidateReportText(in.Title, in.Description, in.Impact, in.Fix)...)
	for _, label := range srcmode.SubmitBlockPlaceholders(in.SubmitBlock) {
		issues = append(issues, "提交信息缺"+label+"（仍是待补）")
	}
	if md := report.SrcMarkdown(in); strings.TrimSpace(md) != "" {
		for _, p := range srcmode.CheckReportMarkdown(md) {
			issues = append(issues, "报文格式："+p)
		}
	}
	return issues
}

// appendExportIssues 把校验问题写进备注区（导出物自带告警），并返回清单。
func appendExportIssues(in *report.SrcInput) []string {
	issues := srcExportIssues(*in)
	for _, is := range issues {
		in.Notes = append(in.Notes, "⚠ "+is)
	}
	return issues
}

// srcWarnings 是「交付证据门」的唯一实现（P19 起）：每条发现导出前缺什么。
// roles 是该发现的绑定流量角色（空 = 没有任何绑定）。调用方给它，是因为批量交付
// 走的是"一次查完所有发现的绑定"的批量路径，不该为了算警告再逐条查一遍库——
// 两处必须给同一份清单，否则批量包里的话和单条导出的话会互相打架。
func (s *Server) srcWarnings(f *db.DBFinding, roles []string) []string {
	var warnings []string
	if len(roles) == 0 {
		warnings = append(warnings, "该发现没有任何绑定流量：复现步骤与整包证据缺失，请先绑定流量再交付。")
	} else {
		hasProof := false
		for _, r := range roles {
			if r == "proof" || r == "verification" {
				hasProof = true
				break
			}
		}
		if !hasProof {
			warnings = append(warnings, "已绑定流量中没有 proof/verification 角色：复现步骤缺少整包证据，请补绑后重新导出。")
		}
	}
	if f.Status == db.FindingRetracted {
		warnings = append(warnings, "该发现已撤回（复测推翻），不要提交；如需提交请先重新确认。")
	}
	// P13：所属任务还有长任务执行在跑 → 输出尚未落地，此刻的证据可能不完整。
	if f.TaskID != nil {
		if x := s.m.Executions(); x != nil {
			if n := len(x.ActiveForTask(itoa64(*f.TaskID))); n > 0 {
				warnings = append(warnings, fmt.Sprintf(
					"所属任务仍有 %d 个长任务执行未结束（扫描/枚举输出未落地）：本次证据可能不完整，建议等执行收束后重新导出。", n))
			}
		}
	}
	return warnings
}

// srcBindingSource renders an endpoint's provenance for 涉及接口清单.
func srcBindingSource(b db.FindingTrafficBinding) string {
	src := srcRoleLabel(b.Role)
	if n := strings.TrimSpace(b.Note); n != "" {
		src += " · " + clipRunes(n, 60)
	}
	return src
}

func srcRoleLabel(role string) string {
	switch role {
	case "proof":
		return "打穿请求（proof）"
	case "baseline":
		return "正常对照（baseline）"
	case "verification":
		return "补充验证（verification）"
	case "supporting":
		return "辅助证据（supporting）"
	}
	if strings.TrimSpace(role) == "" {
		return "证据"
	}
	return role
}

// srcRequestBody inlines the staged request body when it is textual and small;
// binary or oversized bodies are skipped (the exported step keeps head only).
func srcRequestBody(store *evidence.Store, sn db.TrafficEvidenceSnapshot) string {
	const maxInline = 64 * 1024
	if sn.ReqLen <= 0 || sn.ReqLen > maxInline {
		return ""
	}
	ct := strings.ToLower(sn.ContentType)
	for _, bin := range []string{"multipart", "octet-stream", "image/", "audio/", "video/", "application/zip", "application/pdf"} {
		if strings.Contains(ct, bin) {
			return ""
		}
	}
	body, _, err := store.OpenBody(sn, "request")
	if err != nil {
		return ""
	}
	defer body.Close()
	b, err := io.ReadAll(io.LimitReader(body, maxInline))
	if err != nil || len(b) == 0 || !utf8.Valid(b) || bytes.ContainsRune(b, 0) {
		return ""
	}
	return string(b)
}

func clipRunes(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "…"
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// srcDeliveryReadiness is the non-mutating readiness check shared by the
// preview and /src-report/check endpoints. It adds platform fields, severity
// and independent-retest state on top of the evidence/text gate. It never starts
// a retest (prepare does that); it only reports what is missing.
func (s *Server) srcDeliveryReadiness(ctx context.Context, f *db.DBFinding, in report.SrcInput) []string {
	issues := append([]string{}, srcExportIssues(in)...)
	issues = append(issues, s.srcPlatformReadiness(ctx, f, in)...)
	return dedupeStrings(issues)
}

func (s *Server) srcPlatformReadiness(ctx context.Context, f *db.DBFinding, in report.SrcInput) []string {
	if f == nil || f.TaskID == nil || *f.TaskID <= 0 {
		return nil
	}
	doc := s.srcDocForTask(*f.TaskID)
	if !doc.IsSrc() {
		return nil
	}
	var issues []string
	if !db.ValidSeverity(f.Severity) {
		issues = append(issues, "发现等级为空或未知："+f.Severity)
	}
	submitCtx := s.srcSubmitContext(ctx, *f.TaskID, f, in, doc)
	issues = append(issues, srcmode.SubmitIssues(doc, submitCtx)...)
	if strings.HasPrefix(strings.TrimSpace(doc.PlatformTemplate), "custom:") {
		for _, label := range srcmode.SubmitBlockPlaceholders(in.SubmitBlock) {
			issues = append(issues, "提交信息缺"+label+"（仍是待补）")
		}
	}
	issues = append(issues, s.srcRetestReadiness(ctx, f, doc)...)
	return issues
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func (s *Server) srcRetestReadiness(ctx context.Context, f *db.DBFinding, doc srcmode.ModeDoc) []string {
	if f == nil || srcRetestExemptStatuses[f.Status] || !srcmode.IsMediumPlus(f.Severity) || !doc.RequireRetest {
		return nil
	}
	rows, err := s.m.pg.ListFindingRetests(f.ID)
	if err != nil {
		return []string{"独立复验：无法读取复验记录：" + err.Error()}
	}
	passed, active, last := srcRetestState(rows)
	if passed && srcRetestCurrent(rows, f, time.Now()) {
		return nil
	}
	if passed {
		return []string{"独立复验：结论针对旧证据版本或已过期，请重新发起"}
	}
	if active {
		return []string{"独立复验：已有复验在进行中，等结论 reproduced 后再准备"}
	}
	if last != "" {
		return []string{"独立复验：上次结论为 " + last + "，未通过；请人工复核或补充证据"}
	}
	return []string{"独立复验：尚未复验；准备交付件时会自动发起"}
}

// srcMarkdownPlain turns the delivery Markdown into a copy-friendly plain-text
// form for platforms whose rich-text editor does not render fences or headings.
func srcMarkdownPlain(md string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if !inFence && (t == "＃" || t == "#") {
			out = append(out, "------------------------")
			continue
		}
		if !inFence && strings.HasPrefix(t, "#") {
			t = strings.TrimSpace(strings.TrimLeft(t, "#"))
		}
		t = strings.ReplaceAll(t, "**", "")
		t = strings.ReplaceAll(t, "`", "")
		out = append(out, t)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func (s *Server) getFindingSrcPreview(w http.ResponseWriter, r *http.Request) {
	f := s.srcFinding(w, r)
	if f == nil {
		return
	}
	in, warnings := s.buildSrcInput(r.Context(), f)
	issues := s.srcDeliveryReadiness(r.Context(), f, in)
	doc := srcmode.ModeDoc{}
	if f.TaskID != nil && *f.TaskID > 0 {
		doc = s.srcDocForTask(*f.TaskID)
	}
	tpl := srcmode.ResolvePlatformTemplate(doc)
	if key := strings.TrimSpace(doc.PlatformTemplate); strings.HasPrefix(key, "custom:") {
		if row, err := s.m.PG().Src().GetSrcSubmitTemplate(r.Context(), key); err == nil && row != nil {
			tpl = srcmode.PlatformTemplate{Key: row.Key, Name: row.Name, RuleURL: row.RuleURL, RuleDate: row.RuleDate}
		}
	}
	platformLevel := ""
	if f.TaskID != nil && *f.TaskID > 0 {
		platformLevel, _ = s.srcPlatformInfo(r.Context(), *f.TaskID, f.Severity)
	}
	writeJSON(w, 200, map[string]any{
		"ready":          len(issues) == 0 && len(warnings) == 0,
		"platform_level": platformLevel,
		"markdown":       report.SrcMarkdown(in),
		"plain_text":     srcMarkdownPlain(report.SrcMarkdown(in)),
		"submit_block":   in.SubmitBlock,
		"fields":         srcmode.ParseSubmitFields(in.SubmitBlock),
		"warnings":       warnings,
		"issues":         issues,
		"steps":          len(in.Steps),
		"endpoints":      len(in.Endpoints),
		"platform_template": map[string]any{
			"key": tpl.Key, "name": tpl.Name, "rule_url": tpl.RuleURL, "rule_date": tpl.RuleDate,
			"explicit": strings.TrimSpace(doc.PlatformTemplate),
		},
	})
}

func (s *Server) getFindingSrcReport(w http.ResponseWriter, r *http.Request) {
	f := s.srcFinding(w, r)
	if f == nil {
		return
	}
	in, _ := s.buildSrcInput(r.Context(), f)
	if issues := appendExportIssues(&in); len(issues) > 0 {
		log.Printf("[src] 发现 #%d 导出正文校验：%s", f.ID, strings.Join(issues, "；"))
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(report.SrcFilename(in.Title, ".md")))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(crlfText(report.SrcMarkdown(in))))
}

func (s *Server) getFindingSrcReportDocx(w http.ResponseWriter, r *http.Request) {
	f := s.srcFinding(w, r)
	if f == nil {
		return
	}
	in, _ := s.buildSrcInput(r.Context(), f)
	if issues := appendExportIssues(&in); len(issues) > 0 {
		log.Printf("[src] 发现 #%d 导出正文校验：%s", f.ID, strings.Join(issues, "；"))
	}
	blob, err := report.SrcDOCX(in)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(report.SrcFilename(in.Title, ".docx")))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(blob)
}

func (s *Server) getFindingSrcReportZip(w http.ResponseWriter, r *http.Request) {
	f := s.srcFinding(w, r)
	if f == nil {
		return
	}
	blob, err := s.buildSrcFindingZip(r.Context(), f)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(report.SrcFilename(report.FindingTitle(f), "-交付包.zip")))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(blob)
}

// getFindingSrcReportCheck is the delivery evidence gate (P15): it reports what a
// submission would be missing BEFORE anyone downloads it — no bound traffic, no
// proof/verification step, a retracted finding, still-running long tasks. The
// export itself is never blocked (a human decides); the gate makes the gap loud.
func (s *Server) getFindingSrcReportCheck(w http.ResponseWriter, r *http.Request) {
	f := s.srcFinding(w, r)
	if f == nil {
		return
	}
	in, warnings := s.buildSrcInput(r.Context(), f)
	warnings = append(warnings, srcExportIssues(in)...)
	if warnings == nil {
		warnings = []string{}
	}
	issues := s.srcPlatformReadiness(r.Context(), f, in)
	if issues == nil {
		issues = []string{}
	}
	writeJSON(w, 200, map[string]any{
		"ready":     len(warnings) == 0 && len(issues) == 0,
		"warnings":  warnings,
		"issues":    issues,
		"steps":     len(in.Steps),
		"endpoints": len(in.Endpoints),
	})
}

// srcOmissionMarker 是"中段已省略"的标准标记（与 src-dig packet_rules.py 同口径）。
const srcOmissionMarker = "[中间无关请求内容已省略]"

// srcMaxPacketChars 是单条报文的字符上限：超过就保留头与尾、中段显式缩减。
const srcMaxPacketChars = 100_000

// truncateSrcPacket 把超长报文压到上限内：保留头部（请求行+头+参数）与尾部（关键回显），
// 中段插标准标记。技能的口径是"保留头与证明段，中段显式缩减"，不是整段删掉。
func truncateSrcPacket(raw string) string {
	if len(raw) <= srcMaxPacketChars {
		return raw
	}
	head := srcMaxPacketChars * 2 / 3
	tail := srcMaxPacketChars - head - len(srcOmissionMarker) - 2
	if head >= len(raw) || tail <= 0 {
		return raw
	}
	return raw[:head] + "\n" + srcOmissionMarker + "\n" + raw[len(raw)-tail:]
}

// crlfText 把 LF 转成 CRLF：src-dig 的报告落盘规范是 CRLF（Windows 平台预览不乱版）。
func crlfText(s string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// warningNotes 只留"⚠"开头的交付告警（严格版式下丢掉元数据备注，但告警必须留）。
func warningNotes(notes []string) []string {
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		if strings.HasPrefix(strings.TrimSpace(n), "⚠") {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
