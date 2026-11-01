package srcprobe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// idEnumMaxIDs 是单次 ID 枚举的上限：每个 ID 发 2 个只读请求（A/B 各一次）。
const idEnumMaxIDs = 50

// idTokenRe 限制 ID 的字符集，避免把路径分隔符/查询串/编码注入模板。
var idTokenRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,64}$`)

// IDCandidate 是 id_enum 的一条候选：同一 ID 用两个身份各取一次后的差异归类。
type IDCandidate struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Class   string `json:"class"`
	AStatus int    `json:"a_status"`
	ALength int64  `json:"a_length"`
	ASHA256 string `json:"a_sha256,omitempty"`
	BStatus int    `json:"b_status"`
	BLength int64  `json:"b_length"`
	BSHA256 string `json:"b_sha256,omitempty"`
}

// probeIDEnum 用 {id} 模板批量取对象，A/B 两个身份各一次，只发 GET。
//
// 归类（B 视为"有权读该对象"的身份）：
//   - same_body：A 也能读到同一对象（状态与正文摘要一致）——最可疑，IDOR 候选；
//   - diff_body：A 也能读但内容不同（可能是 A 自己租户的同名对象）——弱候选；
//   - a_only   ：A 能读、B 反而不能——可疑（身份权限倒挂）；
//   - b_only   ：只有 B 能读——正常（鉴权生效）；
//   - neither  ：两边都不可读。
func probeIDEnum(ctx context.Context, target *url.URL, opts Options, r *runner, res *Result) error {
	tmpl := strings.TrimSpace(opts.Template)
	if strings.Count(tmpl, "{id}") != 1 {
		return fmt.Errorf("template 必须包含且只包含一个 {id} 占位符")
	}
	ids := dedupe(opts.IDs)
	if len(ids) == 0 {
		return fmt.Errorf("ids 不能为空")
	}
	if len(ids) > idEnumMaxIDs {
		ids = ids[:idEnumMaxIDs]
		res.Truncated = true
		res.Notes = append(res.Notes, fmt.Sprintf("ID 数超过 %d，已截断", idEnumMaxIDs))
	}
	type obs struct {
		status int
		length int64
		sha    string
		ok     bool
		err    bool
	}
	fetch := func(u string, headers map[string]string) obs {
		resp, body, err := r.doWith(ctx, http.MethodGet, u, headers)
		if err != nil {
			return obs{err: true}
		}
		sum := sha256.Sum256(body)
		return obs{
			status: resp.StatusCode,
			length: int64(len(body)),
			sha:    hex.EncodeToString(sum[:]),
			ok:     resp.StatusCode >= 200 && resp.StatusCode < 300,
		}
	}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if !idTokenRe.MatchString(id) {
			res.Metrics["skipped"]++
			appendNote(res, "ID 字符集非法，已跳过："+id)
			continue
		}
		u, ok := resolveSameHost(target, strings.Replace(tmpl, "{id}", id, 1))
		if !ok {
			res.Metrics["skipped"]++
			continue
		}
		a := fetch(u, opts.HeadersA)
		b := fetch(u, opts.HeadersB)
		res.Metrics["tested"]++
		if a.err || b.err {
			res.Metrics["failed"]++
		}
		switch {
		case a.ok && b.ok && a.sha == b.sha:
			res.Metrics["same_body"]++
			addIDCandidate(res, IDCandidate{ID: id, URL: u, Class: "same_body", AStatus: a.status, ALength: a.length, ASHA256: a.sha, BStatus: b.status, BLength: b.length, BSHA256: b.sha})
		case a.ok && b.ok:
			res.Metrics["diff_body"]++
			addIDCandidate(res, IDCandidate{ID: id, URL: u, Class: "diff_body", AStatus: a.status, ALength: a.length, ASHA256: a.sha, BStatus: b.status, BLength: b.length, BSHA256: b.sha})
		case a.ok && !b.ok:
			res.Metrics["a_only"]++
			addIDCandidate(res, IDCandidate{ID: id, URL: u, Class: "a_only", AStatus: a.status, ALength: a.length, ASHA256: a.sha, BStatus: b.status, BLength: b.length, BSHA256: b.sha})
		case b.ok && !a.ok:
			res.Metrics["b_only"]++
		default:
			res.Metrics["neither"]++
		}
	}
	res.Metrics["candidates"] = res.Metrics["same_body"] + res.Metrics["diff_body"] + res.Metrics["a_only"]
	appendNote(res, "id_enum 只是越权线索：same_body 不等于越权，需结合对象归属并经独立复验。")
	return nil
}

func addIDCandidate(res *Result, c IDCandidate) {
	if len(res.IDCandidates) < 100 {
		res.IDCandidates = append(res.IDCandidates, c)
	}
}
