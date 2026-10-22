package srcmode

import (
	"fmt"
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
)

// requestIDHeaders 是能标识"这一次请求"的头。同一个值出现在两步里，说明第二步
// 是复制的（或两次抓的是同一个包），这正是不合规证据最常见的形状。
var requestIDHeaders = []string{
	"x-request-id", "x-trace-id", "x-tt-logid", "x-b3-traceid",
	"x-amzn-trace-id", "traceparent", "x-correlation-id",
}

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

	seenID := map[string]int64{} // 标识头值 -> 首次出现的 binding
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
