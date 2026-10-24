package server

// P2 确定性只读探针的宿主实现：把资产解析成 URL、校验范围/时间窗、构造走录制
// 代理的 HTTP 客户端，然后调用 srcprobe 执行。agent 侧只看到结构化结果与 metrics。

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/config"
	"github.com/lin9527ya/bladepro/srcprobe"
)

const srcProbeDefaultRate = 5

// runSrcProbe 是注入给 agent 的 SrcProber 实现。
func (s *Server) runSrcProbe(ctx context.Context, req agent.SrcProbeRequest) (*srcprobe.Result, error) {
	if req.TaskID <= 0 || req.AssetID <= 0 {
		return nil, fmt.Errorf("需要 task_id 与 asset_id")
	}
	doc := s.srcDocForTask(req.TaskID)
	if !doc.IsSrc() {
		return nil, fmt.Errorf("该任务不是 SRC 模式，不能使用只读探针")
	}
	if strings.TrimSpace(doc.TimeWindow) != "" && !doc.InWindow(time.Now()) {
		return nil, fmt.Errorf("当前不在平台作业时间窗（%s）内：等窗口开启后再探", doc.TimeWindow)
	}
	store := s.m.PG().Src()
	asset, err := store.SrcAssetInScope(ctx, req.TaskID, req.AssetID)
	if err != nil {
		return nil, err
	}
	if asset == nil {
		return nil, fmt.Errorf("资产 %d 不在本任务测试范围内", req.AssetID)
	}
	target := srcProbeTargetURL(asset.Label)
	if target == "" {
		return nil, fmt.Errorf("资产 %q 不是可探测的 http(s) URL/域名", asset.Label)
	}
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("无法解析资产 URL：%q", target)
	}
	inScope, decidable, err := store.SrcHostInScope(ctx, req.TaskID, u.Hostname())
	if err != nil {
		return nil, err
	}
	if decidable && !inScope {
		return nil, fmt.Errorf("资产 %q 的主机不在任务范围内", asset.Label)
	}

	var words []string
	if req.Kind == srcprobe.KindPaths {
		words, err = readSrcProbeWordlist()
		if err != nil {
			return nil, err
		}
	}
	var retest []string
	if req.Kind == srcprobe.KindNon404 {
		if s.m.traffic == nil {
			return nil, fmt.Errorf("录制代理未开启：non404 探针需要任务流量")
		}
		retest, err = s.srcProbeRetestURLs(req.TaskID, u.Hostname())
		if err != nil {
			return nil, err
		}
		if len(retest) == 0 {
			return nil, fmt.Errorf("该 host 没有已录制的非 404 路由：先跑 paths 探针或用工具抓包")
		}
	}
	client, note := s.srcProbeClient()
	rate := doc.RateLimit
	if rate <= 0 {
		rate = srcProbeDefaultRate
	}
	res, err := srcprobe.Run(ctx, srcprobe.Options{
		Target: target, Kind: req.Kind, Wordlist: words, MaxPaths: req.MaxPaths,
		Rate: rate, Client: client, Endpoints: req.Endpoints, Retest: retest,
	})
	if err != nil {
		return nil, err
	}
	res.Notes = append(res.Notes, note)
	return res, nil
}

// srcProbeTargetURL 把资产展示标签变成 http(s) URL；不是主机标签时返回空。
func srcProbeTargetURL(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	if strings.Contains(label, "://") {
		u, err := url.Parse(label)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return ""
		}
		return u.String()
	}
	if host, port, err := net.SplitHostPort(label); err == nil {
		return "https://" + net.JoinHostPort(host, port)
	}
	if strings.ContainsAny(label, " /?#") {
		return ""
	}
	return "https://" + label
}

// readSrcProbeWordlist 读取技能自带 API 路径字典（前 200 条以内由 max_paths 控制）。
func readSrcProbeWordlist() ([]string, error) {
	path := filepath.Join(config.SkillDir(), "src-wordlist", "wordlists", "api-paths-top200.txt")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取路径字典失败（%s）：%w", path, err)
	}
	lines := strings.Split(string(raw), "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		out = append(out, ln)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("路径字典为空：%s", path)
	}
	return out, nil
}

// srcProbeRetestURLs 从该 host 的录制流量里取非 404 的 GET URL（去重，最多 100）。
func (s *Server) srcProbeRetestURLs(taskID int64, host string) ([]string, error) {
	since := s.srcVerifySince(taskID)
	rows, err := s.m.traffic.Since(host, since, 20000)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]string, 0, 32)
	for _, e := range rows {
		if e.Status == 0 || e.Status == 404 || !strings.EqualFold(e.Method, "GET") {
			continue
		}
		raw := strings.TrimSpace(e.URL)
		if raw == "" || seen[raw] {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || strings.ToLower(u.Hostname()) != strings.ToLower(host) {
			continue
		}
		seen[raw] = true
		out = append(out, raw)
		if len(out) >= 100 {
			break
		}
	}
	return out, nil
}

// srcProbeClient 构造走录制代理的 HTTP 客户端；CA 载入失败时只提示、不阻断。
func (s *Server) srcProbeClient() (*http.Client, string) {
	note := "未走录制代理：本次探针不会进入证据链"
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if addr := strings.TrimSpace(s.m.ProxyAddr()); addr != "" {
		if pu, err := url.Parse("http://" + addr); err == nil {
			tr.Proxy = http.ProxyURL(pu)
			note = "经录制代理（探针请求会进入流量证据）"
		}
		if ca := strings.TrimSpace(s.m.ProxyCACert()); ca != "" {
			if pem, err := os.ReadFile(ca); err == nil {
				pool := x509.NewCertPool()
				if pool.AppendCertsFromPEM(pem) {
					tr.TLSClientConfig.RootCAs = pool
				} else {
					note += "；CA 载入失败，HTTPS 探针可能因证书校验失败"
				}
			}
		}
	}
	return &http.Client{
		Transport: tr,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, note
}
