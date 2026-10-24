package server

// P0-7：SRC 收尾项指标的证据化核验（只读告警，不阻断）。
//
// 收尾闸的 metrics 由模型自报，门只比数字——"格子清零但只读了首页"正是这样发生的。
// 这里从录制流量推导同一组可观察指标（不同路径数 / 方法数 / JS 响应数 / 非 404 数 /
// 复测数），在自报 > 实测时给出告警。它**不阻断**收尾：录制可能关闭、MITM 可能透传，
// 实测偏低不等于模型在撒谎；但数字对不上这件事必须让人和 agent 当场看见。

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lin9527ya/bladepro/traffic"
)

// srcObservedMetrics 是从流量里推导出的可核对数字（也是告警里引用的"实测"）。
type srcObservedMetrics struct {
	Requests   int `json:"requests"`
	Paths      int `json:"paths"`
	Methods    int `json:"methods"`
	JS         int `json:"js"`
	SourceMaps int `json:"sourcemap"`
	Non404     int `json:"non404"`
	Retested   int `json:"retested"`
}

// srcVerifiableCheck 标记哪些收尾项有可推导的流量指标。截图/防护对照等不在其中。
func srcVerifiableCheck(key string) bool {
	switch strings.TrimSpace(key) {
	case "paths_enum", "js_enum", "method_matrix", "non404_retest":
		return true
	}
	return false
}

// verifySrcCheckMetrics 是注入给 agent ToolSet 的 SrcVerifier：返回告警与实测数字。
func (s *Server) verifySrcCheckMetrics(ctx context.Context, taskID, assetID int64, checkKey string, reported map[string]int) ([]string, map[string]int) {
	if !srcVerifiableCheck(checkKey) || taskID <= 0 || assetID <= 0 {
		return nil, nil
	}
	store := s.m.PG().Src()
	asset, err := store.SrcAssetInScope(ctx, taskID, assetID)
	if err != nil || asset == nil {
		return nil, nil
	}
	host := srcAssetHost(asset.Label)
	if host == "" {
		return nil, nil
	}
	obs, ok := s.observedTrafficForHost(taskID, host)
	if !ok {
		return nil, nil
	}
	return verifySrcMetrics(checkKey, reported, obs), observedAsMap(obs)
}

// verifySrcMetrics 是纯判定核（便于单测）：自报超过实测才告警，措辞里带上差值。
func verifySrcMetrics(checkKey string, reported map[string]int, obs srcObservedMetrics) []string {
	var warnings []string
	switch strings.TrimSpace(checkKey) {
	case "paths_enum":
		if r := reported["paths"]; r > obs.Paths {
			warnings = append(warnings, fmt.Sprintf(
				"自报 paths=%d，但录制流量里该 host 只有 %d 个不同路径；确认枚举是否走了录制代理（录制关闭/MITM 透传时实测会偏低）",
				r, obs.Paths))
		}
	case "js_enum":
		if r := reported["js_external"] + reported["js_chunks"]; r > obs.JS {
			warnings = append(warnings, fmt.Sprintf(
				"自报外链/异步 JS=%d，但录制流量里该 host 只有 %d 个 JS 响应", r, obs.JS))
		}
		if r := reported["sourcemap"]; r > obs.SourceMaps {
			warnings = append(warnings, fmt.Sprintf(
				"自报 sourcemap=%d，但流量里只看到 %d 个 .map 响应", r, obs.SourceMaps))
		}
	case "method_matrix":
		r := reported["methods"]
		if r == 0 {
			r = reported["state_methods"]
		}
		if r > obs.Methods {
			warnings = append(warnings, fmt.Sprintf(
				"自报已测方法=%d，但流量里该 host 只出现过 %d 种 HTTP 方法", r, obs.Methods))
		}
	case "non404_retest":
		if r := reported["non404"]; r > obs.Non404 {
			warnings = append(warnings, fmt.Sprintf(
				"自报 non404=%d，但流量里只有 %d 条非 404 响应", r, obs.Non404))
		}
		if r := reported["retested"]; r > obs.Retested {
			warnings = append(warnings, fmt.Sprintf(
				"自报已复测=%d，但流量里满足「同一路由 ≥2 次非 404」的只有 %d 个", r, obs.Retested))
		}
	}
	return warnings
}

func observedAsMap(o srcObservedMetrics) map[string]int {
	return map[string]int{
		"requests": o.Requests, "paths": o.Paths, "methods": o.Methods,
		"js": o.JS, "sourcemap": o.SourceMaps, "non404": o.Non404, "retested": o.Retested,
	}
}

// srcAssetHost 从资产展示标签里取主机名；app_name 这类非主机标签返回空（无法核对）。
func srcAssetHost(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	if strings.Contains(label, "://") {
		if u, err := url.Parse(label); err == nil {
			return strings.ToLower(u.Hostname())
		}
	}
	if h, _, err := net.SplitHostPort(label); err == nil {
		return strings.ToLower(strings.Trim(h, "[]"))
	}
	if ip := net.ParseIP(strings.Trim(label, "[]")); ip != nil {
		return strings.ToLower(strings.Trim(label, "[]"))
	}
	// 域名/子域：带空格或路径分隔符的是名字，不是 host。
	if strings.ContainsAny(label, " /?#") {
		return ""
	}
	return strings.ToLower(label)
}

// ---------- 流量观察 + 短 TTL 缓存 ----------

type srcVerifyCacheEntry struct {
	at  time.Time
	obs srcObservedMetrics
	ok  bool
}

var (
	srcVerifyMu    sync.Mutex
	srcVerifyCache = map[string]srcVerifyCacheEntry{}
)

const srcVerifyCacheTTL = 30 * time.Second

func (s *Server) observedTrafficForHost(taskID int64, host string) (srcObservedMetrics, bool) {
	if s.m.traffic == nil {
		return srcObservedMetrics{}, false
	}
	key := strconv.FormatInt(taskID, 10) + "|" + host
	now := time.Now()
	srcVerifyMu.Lock()
	if e, ok := srcVerifyCache[key]; ok && now.Sub(e.at) < srcVerifyCacheTTL {
		srcVerifyMu.Unlock()
		return e.obs, e.ok
	}
	// 顺手清过期项，避免长期运行的无界增长。
	for k, e := range srcVerifyCache {
		if now.Sub(e.at) >= srcVerifyCacheTTL {
			delete(srcVerifyCache, k)
		}
	}
	srcVerifyMu.Unlock()

	since := s.srcVerifySince(taskID)
	rows, err := s.m.traffic.Since(host, since, 20000)
	obs, ok := srcObservedMetrics{}, false
	if err == nil {
		obs = observedFromExchanges(rows)
		ok = true
	}

	srcVerifyMu.Lock()
	srcVerifyCache[key] = srcVerifyCacheEntry{at: now, obs: obs, ok: ok}
	srcVerifyMu.Unlock()
	return obs, ok
}

// srcVerifySince 取任务开始时间（首次运行优先）再往前留 5 分钟时钟余量。
func (s *Server) srcVerifySince(taskID int64) int64 {
	if t, err := s.m.pg.GetTask(taskID); err == nil && t != nil {
		if t.FirstRunAt != nil && !t.FirstRunAt.IsZero() {
			return t.FirstRunAt.Unix() - 300
		}
		if !t.CreatedAt.IsZero() {
			return t.CreatedAt.Unix() - 300
		}
	}
	return 0
}

// observedFromExchanges 把流量元数据推导成可核对数字。
func observedFromExchanges(rows []traffic.ExchangeMeta) srcObservedMetrics {
	var obs srcObservedMetrics
	paths := map[string]bool{}
	methods := map[string]bool{}
	js := map[string]bool{}
	maps := map[string]bool{}
	retest := map[string]int{}
	for _, e := range rows {
		obs.Requests++
		p := strings.TrimSpace(e.URLTemplate)
		if p == "" {
			p = strings.TrimSpace(e.Path)
		}
		if p == "" {
			p = strings.TrimSpace(e.URL)
		}
		if p != "" {
			paths[p] = true
		}
		m := strings.ToUpper(strings.TrimSpace(e.Method))
		if m != "" {
			methods[m] = true
		}
		lowerURL := strings.ToLower(e.URL)
		ct := strings.ToLower(e.ContentType)
		isJS := strings.Contains(ct, "javascript") || strings.Contains(ct, "ecmascript") ||
			strings.HasSuffix(lowerURL, ".js") || strings.HasSuffix(lowerURL, ".mjs") ||
			strings.Contains(lowerURL, ".js?")
		if isJS {
			js[p] = true
		}
		if strings.HasSuffix(lowerURL, ".map") || strings.Contains(lowerURL, ".map?") {
			maps[p] = true
		}
		if e.Status != 0 && e.Status != 404 {
			obs.Non404++
			if p != "" {
				retest[m+" "+p]++
			}
		}
	}
	obs.Paths = len(paths)
	obs.Methods = len(methods)
	obs.JS = len(js)
	obs.SourceMaps = len(maps)
	for _, n := range retest {
		if n >= 2 {
			obs.Retested++
		}
	}
	return obs
}
