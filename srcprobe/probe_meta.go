package srcprobe

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

const (
	// metaMaxURLs 是 alive/fingerprint 单个资产最多探测的 URL 数。
	metaMaxURLs = 100
	// fingerprintExtraPaths 是 fingerprint 在首页之外额外抓取的固定路径（各 1 次请求）。
	fingerprintExtraPaths = 3
)

// metaURLs 把资产 URL 与调用方给的相对路径合并成同 host 的 GET 列表（首个恒为资产 URL）。
func metaURLs(target *url.URL, raw []string, max int) []string {
	if max <= 0 || max > metaMaxURLs {
		max = metaMaxURLs
	}
	out := []string{target.String()}
	seen := map[string]bool{out[0]: true}
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		u, ok := resolveSameHost(target, p)
		if !ok || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
		if len(out) >= max {
			break
		}
	}
	return out
}

// probeAlive 逐个 GET 目标（及其相对路径），统计存活与状态分布。
// 只做"有没有响应"，不解析正文——正文层面交给 fingerprint。
func probeAlive(ctx context.Context, target *url.URL, opts Options, r *runner, res *Result) error {
	for _, raw := range metaURLs(target, opts.URLs, opts.MaxURLs) {
		resp, body, err := r.do(ctx, http.MethodGet, raw)
		if err != nil {
			res.Metrics["failed"]++
			appendNote(res, "存活探测失败："+raw+"："+err.Error())
			continue
		}
		countAlive(res, resp, body)
		r.record(res, http.MethodGet, raw, resp.StatusCode, int64(len(body)), resp.Header.Get("Content-Type"), resp.StatusCode != http.StatusNotFound)
	}
	return nil
}

// probeFingerprint 在存活统计之外收集低成本指纹：响应头特征、favicon 哈希、
// robots.txt / sitemap.xml 是否存在。每个资产最多 1 + 3 次请求（首个 URL 才抓固定路径）。
func probeFingerprint(ctx context.Context, target *url.URL, opts Options, r *runner, res *Result) error {
	urls := metaURLs(target, opts.URLs, opts.MaxURLs)
	tech := map[string]bool{}
	for i, raw := range urls {
		resp, body, err := r.do(ctx, http.MethodGet, raw)
		if err != nil {
			res.Metrics["failed"]++
			appendNote(res, "指纹探测失败："+raw+"："+err.Error())
			continue
		}
		countAlive(res, resp, body)
		for _, t := range headerTech(resp) {
			tech[t] = true
		}
		r.record(res, http.MethodGet, raw, resp.StatusCode, int64(len(body)), resp.Header.Get("Content-Type"), resp.StatusCode != http.StatusNotFound)
		if i != 0 {
			continue // 固定路径只对资产本身抓一次
		}
		probeFavicon(ctx, target, r, res)
		probeStaticPath(ctx, target, r, res, "/robots.txt", "robots")
		probeStaticPath(ctx, target, r, res, "/sitemap.xml", "sitemap")
	}
	res.Metrics["tech"] = len(tech)
	res.Tech = sortedKeys(tech)
	return nil
}

func countAlive(res *Result, resp *http.Response, body []byte) {
	res.Metrics["alive"]++
	status := resp.StatusCode
	res.Metrics[fmt.Sprintf("status_%dxx", status/100)]++
	if title := pageTitle(body); title != "" {
		res.Metrics["titled"]++
	}
}

// headerTech 从响应头提取指纹信号（不做正文特征匹配，避免误报与额外解析成本）。
func headerTech(resp *http.Response) []string {
	var out []string
	add := func(name, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, name+": "+value)
		}
	}
	add("server", resp.Header.Get("Server"))
	add("x-powered-by", resp.Header.Get("X-Powered-By"))
	add("via", resp.Header.Get("Via"))
	add("x-cache", resp.Header.Get("X-Cache"))
	for _, c := range resp.Header.Values("Set-Cookie") {
		if name, _, ok := strings.Cut(c, "="); ok {
			name = strings.TrimSpace(name)
			if name != "" {
				out = append(out, "cookie: "+name)
			}
		}
	}
	return out
}

// probeFavicon 抓 /favicon.ico 并算 FOFA 口径哈希。
func probeFavicon(ctx context.Context, target *url.URL, r *runner, res *Result) {
	u, ok := resolveSameHost(target, "/favicon.ico")
	if !ok {
		return
	}
	resp, body, err := r.do(ctx, http.MethodGet, u)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 || len(body) == 0 {
		return
	}
	res.Metrics["favicon"]++
	res.FaviconMMH3 = fmt.Sprintf("%d", faviconHash(body))
	appendNote(res, fmt.Sprintf("favicon_mmh3=%s", res.FaviconMMH3))
}

// probeStaticPath 检查一个固定路径是否存在（200 且正文非空）。
func probeStaticPath(ctx context.Context, target *url.URL, r *runner, res *Result, path, metric string) {
	u, ok := resolveSameHost(target, path)
	if !ok {
		return
	}
	resp, body, err := r.do(ctx, http.MethodGet, u)
	if err != nil || resp.StatusCode != http.StatusOK || len(bytes.TrimSpace(body)) == 0 {
		return
	}
	res.Metrics[metric]++
}

// pageTitle 取 HTML 的 <title> 文本；非 HTML 或没有标题时返回空串。
func pageTitle(body []byte) string {
	if len(body) == 0 || !bytes.Contains(bytes.ToLower(body), []byte("<title")) {
		return ""
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	var out string
	var walk func(*html.Node) bool
	walk = func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "title" {
			if n.FirstChild != nil {
				out = strings.TrimSpace(n.FirstChild.Data)
			}
			return true
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if walk(c) {
				return true
			}
		}
		return false
	}
	walk(doc)
	if len(out) > 200 {
		out = out[:200]
	}
	return out
}

func appendNote(res *Result, note string) {
	if len(res.Notes) < 40 {
		res.Notes = append(res.Notes, note)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
