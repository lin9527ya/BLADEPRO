package server

// 截图自动化：用本机 headless Chromium/Chrome 对资产截图，写入证据产物
// （evidence_artifacts + data/evidence/artifacts/<sha>.bin），并创建一条 fact 证据节点。
// 平台要求截图时，src_mark_check 的 screenshots done 会自动调用这里。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/db"
)

const screenshotTimeout = 35 * time.Second

// screenshotBrowser resolves the headless browser. BLADEPRO_CHROMIUM wins; then
// common Chromium/Chrome names on PATH. Empty = feature unavailable (clear error).
func screenshotBrowser() string {
	if v := strings.TrimSpace(os.Getenv("BLADEPRO_CHROMIUM")); v != "" {
		if p, err := exec.LookPath(v); err == nil {
			return p
		}
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// runSrcScreenshot is the SrcScreenshotter implementation injected into agents.
func (s *Server) runSrcScreenshot(ctx context.Context, req agent.SrcScreenshotRequest) (*agent.SrcScreenshotResult, error) {
	if req.TaskID <= 0 || req.AssetID <= 0 {
		return nil, fmt.Errorf("需要 task_id 与 asset_id")
	}
	doc := s.srcDocForTask(req.TaskID)
	if !doc.IsSrc() {
		return nil, fmt.Errorf("该任务不是 SRC 模式，不能截图")
	}
	if strings.TrimSpace(doc.TimeWindow) != "" && !doc.InWindow(time.Now()) {
		return nil, fmt.Errorf("当前不在平台作业时间窗（%s）内：等窗口开启后再截图", doc.TimeWindow)
	}
	store := s.m.PG().Src()
	asset, err := store.SrcAssetInScope(ctx, req.TaskID, req.AssetID)
	if err != nil {
		return nil, err
	}
	if asset == nil {
		return nil, fmt.Errorf("资产 %d 不在本任务测试范围内", req.AssetID)
	}
	target := strings.TrimSpace(req.URL)
	if target == "" {
		target = srcProbeTargetURL(asset.Label)
	}
	if target == "" {
		return nil, fmt.Errorf("资产 %q 不是可截图的 http(s) URL/域名", asset.Label)
	}
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("截图目标必须是 http/https URL：%q", target)
	}
	inScope, decidable, err := store.SrcHostInScope(ctx, req.TaskID, u.Hostname())
	if err != nil {
		return nil, err
	}
	if decidable && !inScope {
		return nil, fmt.Errorf("目标主机 %q 不在任务范围内", u.Hostname())
	}
	browser := screenshotBrowser()
	if browser == "" {
		return nil, fmt.Errorf("未找到 chromium/google-chrome（可用 BLADEPRO_CHROMIUM 指定路径）")
	}
	width, height := req.Width, req.Height
	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 900
	}
	if width < 320 {
		width = 320
	}
	if width > 3840 {
		width = 3840
	}
	if height < 240 {
		height = 240
	}
	if height > 4320 {
		height = 4320
	}

	if len(req.Steps) > 0 {
		if err := validateScreenshotSteps(ctx, store, req.TaskID, req.Steps); err != nil {
			return nil, err
		}
	}
	tmp, err := os.MkdirTemp("", "bladepro-shot-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "shot.png")
	if len(req.Steps) > 0 || req.FullPage {
		runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		if err := captureWithCDP(runCtx, browser, target, out, width, height, req.FullPage, req.Steps, s.m.ProxyAddr()); err != nil {
			return nil, fmt.Errorf("浏览器截图失败：%w", err)
		}
	} else {
		profile := filepath.Join(tmp, "profile")
		args := []string{
			"--headless=new", "--disable-gpu", "--hide-scrollbars", "--no-first-run",
			"--no-default-browser-check", "--disable-dev-shm-usage",
			"--user-data-dir=" + profile,
			fmt.Sprintf("--window-size=%d,%d", width, height),
			"--virtual-time-budget=8000",
			"--screenshot=" + out,
		}
		if os.Geteuid() == 0 {
			args = append(args, "--no-sandbox")
		}
		if addr := strings.TrimSpace(s.m.ProxyAddr()); addr != "" {
			args = append(args, "--proxy-server=http://"+addr, "--ignore-certificate-errors")
		}
		args = append(args, target)
		runCtx, cancel := context.WithTimeout(ctx, screenshotTimeout)
		defer cancel()
		cmd := exec.CommandContext(runCtx, browser, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(output))
			if len(msg) > 800 {
				msg = msg[:800]
			}
			return nil, fmt.Errorf("浏览器截图失败：%v（%s）", err, msg)
		}
	}
	f, err := os.Open(out)
	if err != nil {
		return nil, fmt.Errorf("截图文件不存在：%w", err)
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if stat.Size() == 0 {
		return nil, fmt.Errorf("截图文件为空")
	}
	// 先落证据产物；MIME 由 evidence store 嗅探，截图必须是 image/*。
	artifact, err := s.evidenceStore().PutArtifact(ctx, db.EvidenceArtifact{
		TaskID: req.TaskID, AssetID: req.AssetID, Kind: "screenshot",
		Filename: fmt.Sprintf("screenshot-%d-%d.png", req.TaskID, time.Now().Unix()),
	}, f)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(artifact.MIME, "image/") {
		return nil, fmt.Errorf("截图产物不是图片（嗅探 MIME=%s）", artifact.MIME)
	}
	note := strings.TrimSpace(req.Note)
	if note == "" {
		note = "证据截图"
	}
	t, ok := s.m.Task(strconv.FormatInt(req.TaskID, 10))
	if !ok {
		return nil, fmt.Errorf("任务不在运行态，无法写证据节点")
	}
	payload := map[string]any{
		"summary":     "证据截图：" + note,
		"detail":      fmt.Sprintf("URL: %s\n产物: evidence #%d\nSHA256: %s\nMIME: %s\n大小: %d bytes", target, artifact.ID, artifact.SHA256, artifact.MIME, artifact.Size),
		"artifact_id": artifact.ID,
		"sha256":      artifact.SHA256,
		"mime":        artifact.MIME,
		"size":        artifact.Size,
		"url":         target,
		"asset_ids":   []int64{req.AssetID},
	}
	nodeID, err := t.Store.AddNode(db.KindFact, payload, 8, "confirmed", "screenshot", []int64{req.AssetID})
	if err != nil {
		return nil, fmt.Errorf("写证据节点失败：%w", err)
	}
	if err := s.m.PG().SetEvidenceArtifactNode(ctx, artifact.ID, nodeID); err != nil {
		return nil, err
	}
	artifact.NodeID = nodeID
	return &agent.SrcScreenshotResult{
		NodeID: nodeID, ArtifactID: artifact.ID, URL: target,
		MIME: artifact.MIME, Size: artifact.Size, Width: width, Height: height,
		Note: note,
	}, nil
}

// getEvidenceArtifact serves one stored artifact (screenshot/attachment).
func (s *Server) getEvidenceArtifact(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if id <= 0 {
		writeErr(w, 400, "bad artifact id")
		return
	}
	rc, a, err := s.evidenceStore().OpenArtifact(r.Context(), id)
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, 404, "artifact not found")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	defer rc.Close()
	if a.MIME != "" {
		w.Header().Set("Content-Type", a.MIME)
	}
	w.Header().Set("Content-Length", strconv.FormatInt(a.Size, 10))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Disposition", `inline; filename="`+strings.ReplaceAll(a.Filename, `"`, "")+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}
