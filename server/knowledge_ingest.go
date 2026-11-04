package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/docingest"
)

// P-Q 知识喂入（移植自 quiver docingest + knowledge_ingest）：
//
//	POST /api/knowledge/ingest
//	  - multipart/form-data：file（docx/pdf/md/txt/json/csv）+ 可选 title/company_id
//	  - application/json：{title, text, company_id?, source_type?, source_ref?}
//
// 产出不直接入库：写成 approvals(kind=knowledge) 待审批。这里**不做 LLM 蒸馏**
// （简化）：summary 取正文首段，正文原文入库；需要结构化时由 ops/reviewer 在审批前整理。
func (s *Server) handleKnowledgeIngest(w http.ResponseWriter, r *http.Request) {
	var (
		title      string
		sourceType string
		sourceRef  string
		text       string
		companyID  int64
	)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			writeErr(w, 400, "multipart 解析失败: "+err.Error())
			return
		}
		title = strings.TrimSpace(r.FormValue("title"))
		sourceRef = strings.TrimSpace(r.FormValue("source_ref"))
		companyID = parseIntDefault(r.FormValue("company_id"))
		file, hdr, err := r.FormFile("file")
		if err != nil {
			writeErr(w, 400, "缺少 file")
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, docingest.MaxArchiveBytes+1))
		if err != nil {
			writeErr(w, 400, "读取文件失败: "+err.Error())
			return
		}
		if title == "" {
			title = hdr.Filename
		}
		if sourceRef == "" {
			sourceRef = hdr.Filename
		}
		extracted, err := docingest.Extract(hdr.Filename, data)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		text = extracted
		sourceType = "file"
	} else {
		var req struct {
			Title      string `json:"title"`
			Text       string `json:"text"`
			URL        string `json:"url"`
			CompanyID  int64  `json:"company_id"`
			SourceType string `json:"source_type"`
			SourceRef  string `json:"source_ref"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, docingest.MaxTextBytes+64<<10)).Decode(&req); err != nil {
			writeErr(w, 400, "bad json: "+err.Error())
			return
		}
		title, text, companyID = strings.TrimSpace(req.Title), req.Text, req.CompanyID
		sourceRef = strings.TrimSpace(req.SourceRef)
		sourceType = "text"
		if u := strings.TrimSpace(req.URL); u != "" {
			fetched, err := fetchURLText(r.Context(), u)
			if err != nil {
				writeErr(w, 400, "抓取失败: "+err.Error())
				return
			}
			text = fetched
			sourceType = "url"
			sourceRef = u
			if title == "" {
				title = u
			}
		} else if strings.TrimSpace(req.SourceType) != "" {
			sourceType = strings.TrimSpace(req.SourceType)
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		writeErr(w, 400, "提取结果为空（扫描件/加密文档请先转文本）")
		return
	}
	if len(text) < 100 {
		writeErr(w, 400, "提取文本过短（<100 字），不像可入库的知识；扫描件请先 OCR")
		return
	}
	if title == "" {
		title = "未命名文档"
	}
	// P-Q：有激活 LLM profile 时先蒸馏（结构化 JSON）；失败回退原文。
	entryType := db.KnowledgeTypeMethodology
	docTitle := title
	summary := distillClip(text, 200)
	bodyMD := text
	distilled := false
	distillErr := ""
	{
		system := "你是 SRC 知识库维护助手。把用户给的文档蒸馏成一条可复用的方法论/技术知识。" +
			"只输出一个 JSON 对象，不要解释。字段：title（≤40字）、summary（≤200字，写清解决什么问题）、" +
			"body_md（Markdown，保留可执行步骤/命令/字段名，删掉客套与重复）、" +
			"entry_type（technique|methodology|vendor_rule 之一）、key（短 slug）、" +
			"confidence（observed|inferred）。不得编造文档里没有的内容；拿不准标 inferred。"
		user := "文档标题：" + title + "\n\n文档正文：\n" + distillClip(text, 24000)
		if out, err := s.distillJSON(r.Context(), system, user); err == nil {
			if t := strings.TrimSpace(str(out["title"])); t != "" {
				docTitle = t
			}
			if sm := strings.TrimSpace(str(out["summary"])); sm != "" {
				summary = sm
			}
			if bd := strings.TrimSpace(str(out["body_md"])); len([]rune(bd)) >= 50 {
				bodyMD = bd
			}
			switch et := db.NormalizeKnowledgeType(str(out["entry_type"])); et {
			case db.KnowledgeTypeTechnique, db.KnowledgeTypeMethodology, db.KnowledgeTypeVendorRule:
				entryType = et
			}
			distilled = true
		} else if !errors.Is(err, errNoLLM) {
			distillErr = err.Error()
		}
	}
	scope := db.KnowledgeScopeGlobal
	if companyID > 0 {
		scope = db.KnowledgeScopeCompany
	}
	payload, _ := json.Marshal(map[string]any{
		"entry_type": entryType, "scope": scope, "company_id": companyID,
		"title": docTitle, "summary": summary, "body_md": bodyMD,
		"source_type": sourceType, "source_ref": sourceRef,
	})
	ap, err := s.knowledgeStore().CreateApproval(db.ApprovalKindKnowledge, payload, "文档喂入："+title)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	note := fmt.Sprintf("已提取 %d 字并生成待审批知识提案；批准后写入知识库。", len(text))
	if distilled {
		note = fmt.Sprintf("已提取 %d 字并由 LLM 蒸馏为 %s；批准后写入知识库。", len(text), entryType)
	} else if distillErr != "" {
		note = fmt.Sprintf("已提取 %d 字（LLM 蒸馏失败，已按原文入库）：%s", len(text), distillErr)
	}
	writeJSON(w, 200, map[string]any{
		"approval_id": ap.ID, "status": ap.Status, "chars": len(text),
		"distilled": distilled, "distill_error": distillErr,
		"note": note,
	})
}

// str 取 JSON 对象里的字符串字段。
func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ── URL 喂入：抓取正文并转文本 ──

// IngestAllowPrivateEnv 允许抓取私网/本机地址（默认拒绝，防 SSRF）。
const IngestAllowPrivateEnv = "BLADEPRO_INGEST_ALLOW_PRIVATE"

func ingestAllowPrivate() bool {
	v := strings.TrimSpace(os.Getenv(IngestAllowPrivateEnv))
	return v == "1" || strings.EqualFold(v, "true")
}

func blockedIngestIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// fetchURLText 抓取 http(s) 页面并转成纯文本。拨号阶段校验目标 IP，覆盖 DNS
// 重绑定；默认拒绝本机/私网/链路本地（云元数据 169.254.169.254 也在内）。
func fetchURLText(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("URL 不合法")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("只支持 http/https URL")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !ingestAllowPrivate() && blockedIngestIP(ip) {
		return "", fmt.Errorf("拒绝抓取内网/本机地址 %s（确需如此请设 %s=1）", ip, IngestAllowPrivateEnv)
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	dialer.Control = func(_, address string, _ syscall.RawConn) error {
		if ingestAllowPrivate() {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if blockedIngestIP(ip) {
			return fmt.Errorf("拒绝连接内网/本机地址 %s", host)
		}
		return nil
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // 不要走环境代理，否则拨号守卫被绕过
	tr.DialContext = dialer.DialContext
	client := &http.Client{
		Timeout:   20 * time.Second,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("重定向次数过多")
			}
			switch strings.ToLower(req.URL.Scheme) {
			case "http", "https":
				return nil
			}
			return fmt.Errorf("重定向到不支持的协议 %q", req.URL.Scheme)
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "bladepro-ingest/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("目标返回 %s", resp.Status)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if ct != "" && !strings.HasPrefix(ct, "text/") &&
		!strings.Contains(ct, "xhtml") && !strings.Contains(ct, "json") && !strings.Contains(ct, "xml") {
		return "", fmt.Errorf("目标不是文本页面（Content-Type: %s）", ct)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	text := string(b)
	if strings.Contains(ct, "html") || strings.Contains(ct, "xml") {
		text = htmlToText(text)
	}
	text = strings.TrimSpace(text)
	if len(text) > docingest.MaxTextBytes {
		text = text[:docingest.MaxTextBytes]
	}
	return text, nil
}

var (
	ingestDropRe  = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>|<noscript[^>]*>.*?</noscript>`)
	ingestBreakRe = regexp.MustCompile(`(?i)<br\s*/?>|</(p|div|li|tr|h[1-6]|section|article|blockquote)>`)
	ingestTagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
)

// htmlToText 去掉脚本/样式与标签，保留块级换行，压缩行内空白。
func htmlToText(s string) string {
	s = ingestDropRe.ReplaceAllString(s, " ")
	s = ingestBreakRe.ReplaceAllString(s, "\n")
	s = ingestTagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
