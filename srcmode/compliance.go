package srcmode

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 证据合规（P2，方案文档 §7.2）。
//
// 这一层只做**判定**，不碰存储：请求/响应原文由宿主从流量快照里取出来交给它，
// 这样规则可以单测，而"怎么取原文"留在 server。判定结论进 src_finding_meta.compliance，
// report_ready 由它与正文完整性共同决定。
//
// 为什么要有它：交付被打回的头号原因不是漏洞不成立，而是"证据是抄的/被截断的"。
// 人眼很难看出 Content-Length 与正文对不上，机器一眼就能。

// EvidenceStep 是一条绑定流量的取证摘要。
type EvidenceStep struct {
	BindingID int64  `json:"binding_id"`
	Role      string `json:"role"`
	Method    string `json:"method"`
	URL       string `json:"url"`
	ReqHead   string `json:"req_head"`
	RespHead  string `json:"resp_head"`
	ReqLen    int64  `json:"req_len"`  // 请求体实际字节数（快照记的真实长度）
	RespLen   int64  `json:"resp_len"` // 响应体实际字节数
	Status    int    `json:"status"`
	ReqHash   string `json:"req_hash"`
	// InScope / ScopeDecidable 由宿主用任务范围判定：判定不了时不要报越界。
	InScope        bool `json:"in_scope"`
	ScopeDecidable bool `json:"scope_decidable"`
}

// ComplianceIssue 是一条合规问题（code 供程序判等，detail 给人看）。
type ComplianceIssue struct {
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	BindingID int64  `json:"binding_id,omitempty"`
}

// 合规问题码。
const (
	ComplianceNoEvidence     = "no_evidence"
	ComplianceNoProof        = "no_proof"
	ComplianceOutOfScope     = "out_of_scope"
	ComplianceEmptyResponse  = "empty_response"
	ComplianceLengthMismatch = "content_length_mismatch"
	ComplianceReplayedID     = "replayed_request_id"
	ComplianceDuplicateReq   = "duplicate_request"
	ComplianceTruncatedHead  = "truncated_header"
)

// requestIDHeaders 是"按请求唯一"的标识头。同一个值出现在两步里，说明第二步
// 是复制的（或两次抓的是同一个包），这正是不合规证据最常见的形状。
//
// 注意**不要**收链级头：traceparent / x-b3-traceid / x-amzn-trace-id 在一条链路里
// 本来就被多个请求共用，收进来会稳定误报（src-dig 的 report_validate.py 明确排除）。
var requestIDHeaders = []string{
	"x-tt-logid", "x-tt-trace-id", "x-request-id", "x-trace-id",
	"request-id", "x-traceid", "x-log-id",
}

// longSignatureHeaders 是"值很长、被截断就复现不了"的签名头。值明显偏短时报警：
// 技能记录过一次 x-medusa 被截断到 400 字符导致复现失败的事故。
var longSignatureHeaders = []string{"x-medusa", "x-argus", "x-tt-token", "x-gorgon", "x-khronos", "x-ladon"}

// CheckCompliance 判定一份发现的绑定证据是否可提交。返回空 = 合规。
func CheckCompliance(severity string, steps []EvidenceStep) []ComplianceIssue {
	issues := make([]ComplianceIssue, 0, 4)
	if len(steps) == 0 {
		return append(issues, ComplianceIssue{
			Code:   ComplianceNoEvidence,
			Detail: "该发现没有任何绑定流量：复现整包缺失，不能提交",
		})
	}
	hasProof := false
	for _, st := range steps {
		if st.Role == "proof" || st.Role == "verification" {
			hasProof = true
			break
		}
	}
	if !hasProof && isMediumPlus(severity) {
		issues = append(issues, ComplianceIssue{
			Code:   ComplianceNoProof,
			Detail: "中危及以上必须有 proof/verification 角色的绑定流量",
		})
	}

	seenID := map[string]int64{}  // 标识头值 -> 首次出现的 binding
	seenReq := map[string]int64{} // 请求哈希 -> 首次出现的 binding
	for _, st := range steps {
		label := strings.TrimSpace(st.Method + " " + st.URL)
		if label == "" {
			label = fmt.Sprintf("绑定 #%d", st.BindingID)
		}
		if st.ScopeDecidable && !st.InScope {
			issues = append(issues, ComplianceIssue{
				Code:      ComplianceOutOfScope,
				Detail:    label + " 的主机不在任务测试范围内",
				BindingID: st.BindingID,
			})
		}
		if st.Status == 0 && strings.TrimSpace(st.RespHead) == "" && st.RespLen == 0 {
			issues = append(issues, ComplianceIssue{
				Code:      ComplianceEmptyResponse,
				Detail:    label + " 没有响应（状态行/头/体全空）：这条证明不了任何事",
				BindingID: st.BindingID,
			})
		}
		// Content-Length 大于正文实际字节数 = 抓包被截断，或是手工拼的整包。
		if want, ok := declaredLength(st.ReqHead); ok && want > st.ReqLen && !chunkedOrEncoded(st.ReqHead) {
			issues = append(issues, ComplianceIssue{
				Code:      ComplianceLengthMismatch,
				Detail:    fmt.Sprintf("%s 请求头声明 Content-Length=%d，实际正文只有 %d 字节", label, want, st.ReqLen),
				BindingID: st.BindingID,
			})
		}
		if want, ok := declaredLength(st.RespHead); ok && want > st.RespLen && !chunkedOrEncoded(st.RespHead) {
			issues = append(issues, ComplianceIssue{
				Code:      ComplianceLengthMismatch,
				Detail:    fmt.Sprintf("%s 响应头声明 Content-Length=%d，实际正文只有 %d 字节", label, want, st.RespLen),
				BindingID: st.BindingID,
			})
		}
		// 签名头被截断：值以省略号结尾，或明显偏短。
		for _, h := range longSignatureHeaders {
			v := strings.TrimSpace(headHeader(st.ReqHead, h))
			if v == "" {
				continue
			}
			if truncatedValue(v) || len(v) < 64 {
				issues = append(issues, ComplianceIssue{
					Code:      ComplianceTruncatedHead,
					Detail:    fmt.Sprintf("%s 的 %s 只有 %d 字符且像被截断：签名头必须逐字节完整，截断会导致复现失败", label, h, len(v)),
					BindingID: st.BindingID,
				})
			}
		}
		for _, h := range requestIDHeaders {
			v := strings.TrimSpace(headHeader(st.ReqHead, h))
			if v == "" {
				continue
			}
			key := h + "=" + v
			if first, dup := seenID[key]; dup && first != st.BindingID {
				issues = append(issues, ComplianceIssue{
					Code:      ComplianceReplayedID,
					Detail:    fmt.Sprintf("%s 的 %s 与绑定 #%d 完全相同（%s）：这两步是同一个请求，不是两次测试", label, h, first, v),
					BindingID: st.BindingID,
				})
			} else if !dup {
				seenID[key] = st.BindingID
			}
		}
		if st.ReqHash != "" {
			if first, dup := seenReq[st.ReqHash]; dup && first != st.BindingID {
				issues = append(issues, ComplianceIssue{
					Code:      ComplianceDuplicateReq,
					Detail:    fmt.Sprintf("%s 的请求内容与绑定 #%d 完全相同：复现步骤里出现了重复的同一请求", label, first),
					BindingID: st.BindingID,
				})
			} else if !dup {
				seenReq[st.ReqHash] = st.BindingID
			}
		}
	}
	return dedupeIssues(issues)
}

func dedupeIssues(in []ComplianceIssue) []ComplianceIssue {
	if len(in) < 2 {
		return in
	}
	seen := map[string]bool{}
	out := make([]ComplianceIssue, 0, len(in))
	for _, is := range in {
		key := is.Code + "|" + is.Detail + "|" + strconv.FormatInt(is.BindingID, 10)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, is)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].BindingID < out[j].BindingID
	})
	return out
}

// isMediumPlus 判断严重度是否达到"必须齐备证据"的门槛。
func isMediumPlus(severity string) bool {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "high", "medium":
		return true
	}
	return false
}

// headHeader 从原始头块里取某个头的第一个值（大小写不敏感）。
func headHeader(head, name string) string {
	name = strings.ToLower(name)
	for _, line := range strings.Split(head, "\n") {
		line = strings.TrimRight(line, "\r")
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.ToLower(strings.TrimSpace(k)) == name {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// declaredLength 读 Content-Length（没有/非法 → ok=false）。
func declaredLength(head string) (int64, bool) {
	raw := headHeader(head, "content-length")
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// chunkedOrEncoded：分块或压缩时 Content-Length 不代表正文字节数，跳过长度判定。
func chunkedOrEncoded(head string) bool {
	if strings.Contains(strings.ToLower(headHeader(head, "transfer-encoding")), "chunked") {
		return true
	}
	enc := strings.ToLower(headHeader(head, "content-encoding"))
	return enc != "" && enc != "identity"
}

// 正文占位符：这些词一旦出现在正文里，说明人还没写。
var placeholderMarkers = []string{"待补", "待补充", "TODO", "todo", "占位", "（待", "(待", "xxx"}

// ValidateReportText 检查八段正文里"只能由人/模型写"的三段（描述/危害/修复）是否齐备。
// 返回空 = 可以保存。占位符是硬拒绝：SRC 平台退稿的第一原因就是正文没写完。
func ValidateReportText(title, description, impact, fix string) []string {
	var issues []string
	if strings.TrimSpace(title) == "" {
		issues = append(issues, "缺漏洞标题")
	} else {
		// 标题规则（长度/禁链接/禁编号/METHOD 形式）与正文规则同一层校验。
		issues = append(issues, ValidateTitle(title)...)
	}
	checks := []struct {
		name string
		text string
		min  int
	}{
		{"漏洞描述", description, 30},
		{"漏洞危害", impact, 10},
		{"修复建议", fix, 10},
	}
	for _, c := range checks {
		text := strings.TrimSpace(c.text)
		if text == "" {
			issues = append(issues, "缺"+c.name)
			continue
		}
		if n := len([]rune(text)); n < c.min {
			issues = append(issues, fmt.Sprintf("%s太短（%d 字，至少 %d 字）：平台会当成没写", c.name, n, c.min))
		}
		if marker := findPlaceholder(text); marker != "" {
			issues = append(issues, fmt.Sprintf("%s里还有占位符「%s」", c.name, marker))
		}
	}
	return issues
}

func findPlaceholder(text string) string {
	for _, m := range placeholderMarkers {
		if strings.Contains(text, m) {
			return m
		}
	}
	return ""
}

// truncatedValue 判断一个头值是否"看起来被截断了"。
func truncatedValue(v string) bool {
	v = strings.TrimSpace(v)
	return strings.HasSuffix(v, "...") || strings.HasSuffix(v, "…") ||
		strings.Contains(v, "(截断)") || strings.Contains(v, "（截断）")
}

// 标题规则（src-dig report_validate.validate_title）：标题是平台审核第一眼，
// 太长/带链接/带编号都会被判"不规范"。
const (
	maxTitleCJK   = 25
	maxTitleLatin = 50
	maxTitleRunes = 100
)

// ValidateTitle 校验漏洞标题，返回问题列表（空 = 通过）。
func ValidateTitle(title string) []string {
	title = strings.TrimSpace(title)
	if title == "" {
		return []string{"缺漏洞标题"}
	}
	var issues []string
	cjk, latin := 0, 0
	for _, r := range title {
		switch {
		case r >= 0x3400 && r <= 0x9FFF:
			cjk++
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			latin++
		}
	}
	if n := len([]rune(title)); n > maxTitleRunes || cjk > maxTitleCJK || latin > maxTitleLatin {
		issues = append(issues, fmt.Sprintf("标题太长（汉字 %d/%d、字母 %d/%d、总长 %d/%d）：说清「谁的站、做成了什么」就够了",
			cjk, maxTitleCJK, latin, maxTitleLatin, len([]rune(title)), maxTitleRunes))
	}
	lower := strings.ToLower(title)
	if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") {
		issues = append(issues, "标题里不要放完整链接：用「域名 + 接口/位置」描述")
	}
	if strings.HasPrefix(title, "/") {
		issues = append(issues, "标题不要以路径开头")
	}
	if regexpTitleNumber.MatchString(title) {
		issues = append(issues, "标题不要包含编号（如「漏洞 3」）")
	}
	if regexpTitleMethod.MatchString(title) {
		issues = append(issues, "标题不要写成「HTTP 方法 + 路径」")
	}
	return issues
}

// 标题里"漏洞 #3""漏洞3"这类编号。
var regexpTitleNumber = regexp.MustCompile(`(?i)漏洞\s*#?\d+`)

// 标题以 "GET /xxx" 开头。
var regexpTitleMethod = regexp.MustCompile(`^(GET|POST|PUT|DELETE|PATCH|OPTIONS|HEAD)\s+/`)

// hasOmissionMarker 判断报文里是否显式标注了缩减（有标记时字节数不再可比）。
func hasOmissionMarker(text string) bool {
	for _, m := range omissionMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// omissionMarkers 沿用 src-dig 的 packet_rules.py：前三个是推荐写法，后两个兼容旧报告。
var omissionMarkers = []string{
	"[中间无关请求内容已省略]",
	"[中间无关响应内容已省略]",
	"[中间无关内容已省略]",
	"...(中段省略)",
	"……(中段省略)",
}

// CheckReportMarkdown 对**成品 markdown** 再查一遍报文层的问题：
// 请求行缺 Host、整行省略号占位、跨块重复的唯一标识头、Content-Length 与正文不符。
// 与 CheckCompliance 的分工：那边看快照（精确字节数），这边看交出去的文本（防导出被改坏）。
func CheckReportMarkdown(md string) []string {
	blocks := splitFencedBlocks(md)
	var issues []string
	seen := map[string]int{}
	for i, raw := range blocks {
		text := strings.ReplaceAll(raw, "\r\n", "\n")
		lines := strings.Split(strings.TrimLeft(text, "\n"), "\n")
		if len(lines) == 0 {
			continue
		}
		// 跳过 ```http 这类语言标记行。
		first := strings.TrimSpace(lines[0])
		if len(lines) > 1 && isLangTag(first) {
			first = strings.TrimSpace(lines[1])
			lines = lines[1:]
		}
		if isRequestLine(first) {
			if !hasHeader(text, "host") {
				issues = append(issues, fmt.Sprintf("代码块 #%d：请求缺 Host 头", i+1))
			}
		} else if isStatusLine(first) {
			// 状态行：正常
		} else if isBareMethod(first) {
			issues = append(issues, fmt.Sprintf("代码块 #%d：请求行不完整（应为 `METHOD 路径 HTTP/x.x`）", i+1))
		}
		for _, line := range lines {
			t := strings.TrimSpace(line)
			if t == "..." || t == "…" || (len(t) >= 3 && strings.Trim(t, ".…") == "") {
				issues = append(issues, fmt.Sprintf("代码块 #%d：有整行省略号占位；超长报文要保留头与证明段，中段用 %s 缩减",
					i+1, omissionMarkers[0]))
				break
			}
		}
		// 跨块：同一个唯一标识头出现两次，说明两包里至少一包是抄的。
		for _, h := range requestIDHeaders {
			v := strings.TrimSpace(headHeader(text, h))
			if v == "" {
				continue
			}
			key := h + "=" + v
			if prev, dup := seen[key]; dup && prev != i {
				issues = append(issues, fmt.Sprintf("代码块 #%d 与 #%d 的 %s 完全相同（%s）：两步不可能是两个请求", prev+1, i+1, h, v))
			} else if !dup {
				seen[key] = i
			}
		}
		// Content-Length 与实际正文：有缩减标记时不比字节数。
		if !hasOmissionMarker(text) {
			if want, ok := declaredLength(text); ok {
				if body := bodyAfterBlankLine(text); int64(len(body)) < want {
					issues = append(issues, fmt.Sprintf("代码块 #%d：Content-Length=%d，实际正文只有 %d 字节", i+1, want, len(body)))
				}
			}
		}
	}
	return issues
}

// splitFencedBlocks 取出 markdown 里 ``` 围起来的代码块（不含围栏本身）。
func splitFencedBlocks(md string) []string {
	parts := strings.Split(md, "```")
	if len(parts) < 3 {
		return nil
	}
	out := make([]string, 0, len(parts)/2)
	for i := 1; i < len(parts); i += 2 {
		out = append(out, parts[i])
	}
	return out
}

func isLangTag(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') &&
			r != '+' && r != '#' && r != '.' && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func isRequestLine(s string) bool {
	f := strings.Fields(s)
	return len(f) >= 3 && strings.HasPrefix(f[len(f)-1], "HTTP/")
}

func isStatusLine(s string) bool {
	return strings.HasPrefix(s, "HTTP/") && len(strings.Fields(s)) >= 2
}

func isBareMethod(s string) bool {
	switch strings.ToUpper(strings.Fields(s + " ")[0]) {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD":
		return !isRequestLine(s)
	}
	return false
}

func hasHeader(text, name string) bool {
	return headHeader(text, name) != ""
}

// bodyAfterBlankLine 取头与体之间的正文（没有空行时返回空）。
func bodyAfterBlankLine(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if i := strings.Index(text, "\n\n"); i >= 0 {
		return text[i+2:]
	}
	return ""
}
