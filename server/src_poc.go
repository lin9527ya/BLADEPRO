package server

// P1 PoC 可执行化：从绑定证据生成可直接重放的 PoC。
//
// 产出三件套（放进交付包 attachments/<finding>-slug/）：
//   - poc.sh        curl 脚本（内联小正文；大正文引用同目录 step-N.body）
//   - request.http  原始请求的 .http 形式（REST Client / IntelliJ 可直接跑）
//   - README.txt    使用边界（签名可能过期、只在授权范围执行、限速）
//
// 同一份脚本也塞进 retest context，让独立复验"执行的就是报告里那份 PoC"。

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/evidence"
	"github.com/lin9527ya/bladepro/report"
)

const (
	srcPocMaxBody    = 2 << 20 // 2MB：更大的正文不生成可重放件
	srcPocInlineBody = 64 << 10
	srcPocMaxScript  = 96 << 10 // retest context 里最多塞 96KB 脚本
)

// srcPocBundle 是一份发现的 PoC 产物（Paths 是相对交付包根目录的路径）。
type srcPocBundle struct {
	Dir     string
	Script  string
	HTTP    string
	Readme  string
	Bodies  map[string][]byte
	Omitted []string // 生成时放弃的内容（过大/读不到），写进 README
}

type srcPocStep struct {
	Role      string
	Note      string
	Method    string
	URL       string
	Headers   []string
	Body      []byte
	BodyName  string
	Truncated bool
}

// buildSrcPoc 从 proof/verification 绑定生成 PoC；没有可重放证据时返回 nil。
func (s *Server) buildSrcPoc(ctx context.Context, f *db.DBFinding) (*srcPocBundle, error) {
	if f == nil {
		return nil, nil
	}
	traffic, err := s.m.PG().Src().SrcFindingTraffic(ctx, f.ID)
	if err != nil || traffic == nil {
		return nil, nil
	}
	store := s.evidenceStore()
	var steps []srcPocStep
	var omitted []string
	for _, b := range traffic.Bindings {
		if b.Role != "proof" && b.Role != "verification" {
			continue
		}
		sn := b.Snapshot
		step := srcPocStep{
			Role: b.Role, Note: strings.TrimSpace(b.Note), Method: strings.ToUpper(strings.TrimSpace(sn.Method)),
			URL: strings.TrimSpace(sn.URL), Headers: parseSrcPocHeaders(sn.ReqHead),
		}
		if step.Method == "" {
			step.Method = "GET"
		}
		if sn.ReqLen > 0 {
			body, truncated, note := s.readSrcPocBody(store, sn)
			if note != "" {
				omitted = append(omitted, note)
			}
			step.Body, step.Truncated = body, truncated
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		return nil, nil
	}
	for i := range steps {
		steps[i].BodyName = fmt.Sprintf("step-%d.body", i+1)
		if len(steps[i].Body) > srcPocInlineBody {
			omitted = append(omitted, fmt.Sprintf(
				"step %d 正文 %dKB 未内联，脚本引用同目录 %s（交付包内有；复验环境请从包内取）",
				i+1, len(steps[i].Body)>>10, steps[i].BodyName))
		}
	}
	slug := srcFileSlug(report.FindingTitle(f), "finding-"+itoa64(f.ID))
	dir := fmt.Sprintf("attachments/%d-%s", f.ID, slug)
	title := report.FindingTitle(f)
	return &srcPocBundle{
		Dir:     dir,
		Script:  renderSrcPocScript(f, title, steps, omitted),
		HTTP:    renderSrcPocHTTP(title, steps),
		Readme:  renderSrcPocReadme(f, title, steps, omitted),
		Bodies:  srcPocBodies(steps),
		Omitted: omitted,
	}, nil
}

// readSrcPocBody 读取原始请求正文（不做二进制过滤：PoC 需要原字节）。超过 2MB
// 只保留前 2MB，并在 README/脚本里标注不完整。
func (s *Server) readSrcPocBody(store *evidence.Store, sn db.TrafficEvidenceSnapshot) ([]byte, bool, string) {
	body, _, err := store.OpenBody(sn, "request")
	if err != nil {
		return nil, false, "读取请求正文失败：" + err.Error()
	}
	defer body.Close()
	b, err := io.ReadAll(io.LimitReader(body, srcPocMaxBody+1))
	if err != nil {
		return nil, false, "读取请求正文失败：" + err.Error()
	}
	if len(b) > srcPocMaxBody {
		return b[:srcPocMaxBody], true, fmt.Sprintf("请求正文超过 %dMB，PoC 只保留前 %dMB", srcPocMaxBody>>20, srcPocMaxBody>>20)
	}
	return b, false, ""
}

func parseSrcPocHeaders(reqHead string) []string {
	lines := strings.Split(strings.ReplaceAll(reqHead, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines[1:] {
		ln = strings.TrimRight(ln, "\r")
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t") {
			continue // 折叠头：curl 不认
		}
		out = append(out, ln)
	}
	return out
}

var srcPocCurlSkipHeaders = map[string]bool{
	"content-length": true, "transfer-encoding": true, "connection": true, "proxy-connection": true,
}

func srcPocCurlHeaders(headers []string) []string {
	out := make([]string, 0, len(headers))
	for _, h := range headers {
		name, _, ok := strings.Cut(h, ":")
		if !ok {
			continue
		}
		if srcPocCurlSkipHeaders[strings.ToLower(strings.TrimSpace(name))] {
			continue
		}
		out = append(out, strings.TrimRight(h, "\r"))
	}
	return out
}

func srcShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func renderSrcPocScript(f *db.DBFinding, title string, steps []srcPocStep, omitted []string) string {
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	fmt.Fprintf(&b, "# SRC PoC — finding #%d %s\n", f.ID, oneLine(title))
	b.WriteString("# 由绑定证据生成。签名/时间戳/一次性 token 可能已过期；只在授权范围内执行并遵守限速。\n")
	b.WriteString("# 用法：bash poc.sh   （响应头由 curl -D - 打印）\n")
	b.WriteString("set -euo pipefail\n")
	b.WriteString("cd \"$(dirname \"$0\")\"\n")
	b.WriteString("tmp=\"$(mktemp -d)\"\n")
	b.WriteString("trap 'rm -rf \"$tmp\"' EXIT\n\n")
	for i, st := range steps {
		fmt.Fprintf(&b, "echo '### step %d [%s] %s %s'\n", i+1, st.Role, st.Method, st.URL)
		bodyRef := ""
		if len(st.Body) > 0 {
			if len(st.Body) <= srcPocInlineBody {
				enc := base64.StdEncoding.EncodeToString(st.Body)
				fmt.Fprintf(&b, "printf '%%s' %s | base64 -d > \"$tmp/%s\"\n", srcShellQuote(enc), st.BodyName)
				bodyRef = "\"$tmp/" + st.BodyName + "\""
			} else {
				bodyRef = st.BodyName // 同目录的 sidecar（zip 里一定有；retest context 可能没有）
			}
		}
		fmt.Fprintf(&b, "curl -sS -D - --max-time 30 -X %s %s", srcShellQuote(st.Method), srcShellQuote(st.URL))
		for _, h := range srcPocCurlHeaders(st.Headers) {
			fmt.Fprintf(&b, " \\\n  -H %s", srcShellQuote(h))
		}
		if bodyRef != "" {
			fmt.Fprintf(&b, " \\\n  --data-binary @%s", bodyRef)
		}
		b.WriteString("\necho\n\n")
	}
	for _, n := range omitted {
		fmt.Fprintf(&b, "# 注意：%s\n", oneLine(n))
	}
	return b.String()
}

func renderSrcPocHTTP(title string, steps []srcPocStep) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# SRC PoC — %s\n# 生成自绑定证据；签名/时间戳可能已过期。\n", oneLine(title))
	for i, st := range steps {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "### step %d [%s] %s\n", i+1, st.Role, st.URL)
		fmt.Fprintf(&b, "%s %s HTTP/1.1\n", st.Method, st.URL)
		for _, h := range st.Headers {
			b.WriteString(h + "\n")
		}
		b.WriteString("\n")
		if len(st.Body) > 0 {
			b.Write(st.Body)
			if st.Truncated {
				b.WriteString("\n[正文超过 2MB，已截断]")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func renderSrcPocReadme(f *db.DBFinding, title string, steps []srcPocStep, omitted []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SRC PoC — finding #%d %s\n", f.ID, oneLine(title))
	b.WriteString("生成自绑定证据（proof/verification）。\n\n")
	b.WriteString("文件：\n")
	b.WriteString("  poc.sh        curl 复现脚本（bash poc.sh）\n")
	b.WriteString("  request.http  原始请求的 .http 形式（REST Client / IntelliJ 可直接跑）\n")
	b.WriteString("  step-N.body   原始请求正文（脚本内联小正文；大正文引用这些文件）\n\n")
	b.WriteString("执行前：\n")
	b.WriteString("  - 只在授权范围（program scope）内执行，并遵守平台限速与作业时间窗。\n")
	b.WriteString("  - 签名、时间戳、一次性 token 可能已过期；必要时先重新登录/重新抓包。\n")
	b.WriteString("  - poc.sh 只发请求、不修改目标状态；响应头由 curl -D - 打印。\n\n")
	for i, st := range steps {
		fmt.Fprintf(&b, "step %d: %s %s（角色 %s", i+1, st.Method, st.URL, st.Role)
		if st.Note != "" {
			b.WriteString("；" + oneLine(st.Note))
		}
		b.WriteString("）\n")
	}
	if len(omitted) > 0 {
		b.WriteString("\n生成告警：\n")
		for _, n := range omitted {
			b.WriteString("  - " + n + "\n")
		}
	}
	return b.String()
}

func srcPocBodies(steps []srcPocStep) map[string][]byte {
	out := map[string][]byte{}
	for _, st := range steps {
		if len(st.Body) > 0 {
			out[st.BodyName] = st.Body
		}
	}
	return out
}
