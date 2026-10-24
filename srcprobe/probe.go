// Package srcprobe 是 SRC 的确定性只读探针执行器（P2）。
//
// 把"路径枚举 / JS 收集 / 方法矩阵 / 非 404 复测"从 LLM 回合里拿掉：串行发请求、
// 按 QPS 限速、结果结构化返回。只发 GET/HEAD/OPTIONS（只读证明），状态变更方法
// 仍应由 agent 在 guard 下显式执行。
package srcprobe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	KindPaths   = "paths"
	KindJS      = "js"
	KindMethods = "methods"
	KindNon404  = "non404"
)

// Options 是一次探针的输入。Client 由宿主提供（应走录制代理并禁止跟随跳转）。
type Options struct {
	Target    string
	Kind      string
	Wordlist  []string // paths
	MaxPaths  int
	Rate      int // requests per second; <=0 -> 5
	Client    *http.Client
	Endpoints []string // methods
	Retest    []string // non404：待复测的 URL（或相对路径）
	MaxRetest int
}

// Hit 是一条非 404/非成功的观察。
type Hit struct {
	Method      string `json:"method"`
	URL         string `json:"url"`
	Status      int    `json:"status"`
	Length      int64  `json:"length"`
	ContentType string `json:"content_type,omitempty"`
}

// Result 是结构化探针结果；Metrics 的键与 src_mark_check 对齐。
type Result struct {
	Kind           string         `json:"kind"`
	Target         string         `json:"target"`
	Requests       int            `json:"requests"`
	ElapsedMS      int64          `json:"elapsed_ms"`
	Metrics        map[string]int `json:"metrics"`
	CountsByStatus map[string]int `json:"counts_by_status"`
	Hits           []Hit          `json:"hits,omitempty"`
	JSURLs         []string       `json:"js_urls,omitempty"`
	Notes          []string       `json:"notes,omitempty"`
	Truncated      bool           `json:"truncated,omitempty"`
}

type runner struct {
	client   *http.Client
	interval time.Duration
	next     time.Time
	requests int
	host     string
}

var sourceMapRe = regexp.MustCompile(`(?im)sourceMappingURL=([^\s*"']+)`)

// Run 执行一种探针。target 必须是 http/https；调用方负责范围/时间窗校验。
func Run(ctx context.Context, opts Options) (*Result, error) {
	target, err := url.Parse(strings.TrimSpace(opts.Target))
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return nil, fmt.Errorf("探针目标必须是 http/https URL：%q", opts.Target)
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	if client.CheckRedirect == nil {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	rate := opts.Rate
	if rate <= 0 {
		rate = 5
	}
	if rate > 100 {
		rate = 100
	}
	r := &runner{
		client: client, interval: time.Second / time.Duration(rate), next: time.Now(),
		host: strings.ToLower(target.Hostname()),
	}
	res := &Result{
		Kind: opts.Kind, Target: target.String(),
		Metrics: map[string]int{}, CountsByStatus: map[string]int{},
	}
	start := time.Now()
	switch opts.Kind {
	case KindPaths:
		err = probePaths(ctx, target, opts, r, res)
	case KindJS:
		err = probeJS(ctx, target, r, res)
	case KindMethods:
		err = probeMethods(ctx, target, opts, r, res)
	case KindNon404:
		err = probeNon404(ctx, target, opts, r, res)
	default:
		err = fmt.Errorf("未知探针类型 %q（paths/js/methods/non404）", opts.Kind)
	}
	res.Requests = r.requests
	res.ElapsedMS = time.Since(start).Milliseconds()
	if err != nil {
		return nil, err
	}
	res.Notes = append(res.Notes, "只读探针：只发 GET/HEAD/OPTIONS；状态变更方法请在 guard 下显式测试。")
	return res, nil
}

func (r *runner) wait(ctx context.Context) error {
	if d := time.Until(r.next); d > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	r.next = time.Now().Add(r.interval)
	return nil
}

func (r *runner) do(ctx context.Context, method, rawURL string) (*http.Response, []byte, error) {
	if err := r.wait(ctx); err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "bladepro-src-probe/1")
	r.requests++
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp, body, nil
}

func (r *runner) record(res *Result, method, rawURL string, status int, length int64, ct string, keepHit bool) {
	res.CountsByStatus[fmt.Sprintf("%d", status)]++
	if status == 404 {
		return
	}
	if keepHit && len(res.Hits) < 200 {
		res.Hits = append(res.Hits, Hit{Method: method, URL: rawURL, Status: status, Length: length, ContentType: ct})
	}
}

func probePaths(ctx context.Context, target *url.URL, opts Options, r *runner, res *Result) error {
	max := opts.MaxPaths
	if max <= 0 || max > 1000 {
		max = 200
	}
	words := dedupe(opts.Wordlist)
	if len(words) > max {
		words = words[:max]
		res.Truncated = true
		res.Notes = append(res.Notes, fmt.Sprintf("路径字典超过 max_paths=%d，已截断", max))
	}
	prefix := strings.TrimRight(target.Path, "/")
	for _, raw := range words {
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		u := *target
		pathPart, query, _ := strings.Cut(strings.TrimPrefix(raw, "/"), "?")
		u.Path = prefix + "/" + pathPart
		u.RawQuery = query
		resp, _, err := r.do(ctx, "GET", u.String())
		if err != nil {
			res.CountsByStatus["0"]++
			continue
		}
		res.Metrics["paths"]++
		if resp.StatusCode != 404 {
			res.Metrics["non404"]++
		}
		r.record(res, "GET", u.String(), resp.StatusCode, resp.ContentLength, resp.Header.Get("Content-Type"), true)
	}
	return nil
}

func probeJS(ctx context.Context, target *url.URL, r *runner, res *Result) error {
	resp, body, err := r.do(ctx, "GET", target.String())
	if err != nil {
		return fmt.Errorf("读取目标首页失败：%w", err)
	}
	r.record(res, "GET", target.String(), resp.StatusCode, int64(len(body)), resp.Header.Get("Content-Type"), false)
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("解析 HTML 失败：%w", err)
	}
	var jsURLs []string
	seen := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" {
			src := ""
			for _, a := range n.Attr {
				if strings.EqualFold(a.Key, "src") {
					src = strings.TrimSpace(a.Val)
					break
				}
			}
			if src == "" {
				res.Metrics["js_inline"]++
			} else if u, ok := resolveSameHost(target, src); ok {
				if !seen[u] {
					seen[u] = true
					jsURLs = append(jsURLs, u)
					res.Metrics["js_external"]++
					low := strings.ToLower(u)
					if strings.Contains(low, "chunk") || strings.Contains(low, ".async.") {
						res.Metrics["js_chunks"]++
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	// 从已取到的 JS 里找 sourcemap（只取同 host，最多 30 个）。
	maps := map[string]bool{}
	for i, jsURL := range jsURLs {
		if i >= 30 {
			res.Notes = append(res.Notes, "JS 文件超过 30 个，sourcemap 只检查了前 30 个")
			break
		}
		_, jsBody, err := r.do(ctx, "GET", jsURL)
		if err != nil {
			continue
		}
		for _, m := range sourceMapRe.FindAllSubmatch(jsBody, -1) {
			if u, ok := resolveSameHost(target, string(m[1])); ok {
				maps[u] = true
			}
		}
	}
	res.Metrics["sourcemap"] = len(maps)
	if len(jsURLs) > 100 {
		jsURLs = jsURLs[:100]
	}
	res.JSURLs = jsURLs
	return nil
}

func probeMethods(ctx context.Context, target *url.URL, opts Options, r *runner, res *Result) error {
	endpoints := dedupe(opts.Endpoints)
	if len(endpoints) == 0 {
		endpoints = []string{target.String()}
	}
	if len(endpoints) > 100 {
		endpoints = endpoints[:100]
		res.Truncated = true
	}
	methods := []string{"GET", "HEAD", "OPTIONS"}
	tested := map[string]bool{}
	allow := map[string]bool{}
	for _, ep := range endpoints {
		raw := ep
		if !strings.Contains(raw, "://") {
			raw = strings.TrimRight(target.String(), "/") + "/" + strings.TrimPrefix(raw, "/")
		}
		u, err := url.Parse(raw)
		if err != nil || strings.ToLower(u.Hostname()) != r.host {
			continue
		}
		for _, m := range methods {
			resp, _, err := r.do(ctx, m, raw)
			if err != nil {
				res.CountsByStatus["0"]++
				continue
			}
			tested[m] = true
			for _, a := range strings.Split(resp.Header.Get("Allow"), ",") {
				if a = strings.ToUpper(strings.TrimSpace(a)); a != "" {
					allow[a] = true
				}
			}
			r.record(res, m, raw, resp.StatusCode, resp.ContentLength, resp.Header.Get("Content-Type"), resp.StatusCode != 404)
		}
	}
	res.Metrics["methods"] = len(tested)
	if len(allow) > 0 {
		names := make([]string, 0, len(allow))
		for m := range allow {
			names = append(names, m)
		}
		sort.Strings(names)
		res.Notes = append(res.Notes, "Allow 声明："+strings.Join(names, ",")+"（未实际发送状态变更方法）")
	}
	return nil
}

func probeNon404(ctx context.Context, target *url.URL, opts Options, r *runner, res *Result) error {
	max := opts.MaxRetest
	if max <= 0 || max > 200 {
		max = 100
	}
	list := dedupe(opts.Retest)
	if len(list) > max {
		list = list[:max]
		res.Truncated = true
	}
	non404, retested := 0, 0
	for _, raw := range list {
		u, err := url.Parse(raw)
		if err != nil || strings.ToLower(u.Hostname()) != r.host {
			continue
		}
		okCount := 0
		for i := 0; i < 2; i++ {
			resp, _, err := r.do(ctx, "GET", u.String())
			if err != nil {
				res.CountsByStatus["0"]++
				continue
			}
			r.record(res, "GET", u.String(), resp.StatusCode, resp.ContentLength, resp.Header.Get("Content-Type"), false)
			if resp.StatusCode != 404 {
				okCount++
			}
		}
		if okCount > 0 {
			non404++
		}
		if okCount >= 2 {
			retested++
		}
	}
	res.Metrics["non404"] = non404
	res.Metrics["retested"] = retested
	return nil
}

func resolveSameHost(base *url.URL, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "data:") {
		return "", false
	}
	u, err := base.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	// 去掉 fragment：请求不需要它，也避免同一文件被算成多个。
	u.Fragment = ""
	if strings.ToLower(u.Hostname()) != strings.ToLower(base.Hostname()) {
		return "", false
	}
	return u.String(), true
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
