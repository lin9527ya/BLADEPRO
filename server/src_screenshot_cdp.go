package server

// 关键步骤截图：当 src_screenshot 带 steps（填表、点按钮、等待渲染）或 full_page
// 时，用 Chromium DevTools Protocol 驱动页面，而不是 CLI 的一次性 --screenshot。
//
// 安全边界：
//   - steps 只允许固定动作（navigate/click/fill/press/wait/scroll），**不执行任意 JS**；
//   - navigate 目标与初始 URL 一样先过任务范围校验（见 validateScreenshotSteps）；
//   - 浏览器仍走录制代理，目标流量与其他 agent 动作同一条出口。

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/db"
)

const (
	cdpStartupTimeout = 15 * time.Second
	cdpCallTimeout    = 15 * time.Second
	cdpMaxSteps       = 20
	cdpMaxWaitMS      = 10_000
	cdpMaxPageHeight  = 20_000
)

// validateScreenshotSteps rejects actions/targets the browser driver will not run.
// It is deliberately strict: a bad navigate URL must fail before launching Chrome,
// not after the page has already reached an unauthorized host.
func validateScreenshotSteps(ctx context.Context, store *db.SrcStore, taskID int64, steps []agent.SrcScreenshotStep) error {
	if len(steps) > cdpMaxSteps {
		return fmt.Errorf("截图步骤最多 %d 步（当前 %d）", cdpMaxSteps, len(steps))
	}
	for i, step := range steps {
		switch strings.ToLower(strings.TrimSpace(step.Action)) {
		case "navigate":
			raw := strings.TrimSpace(step.URL)
			u, err := url.Parse(raw)
			if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("第 %d 步 navigate 需要 http(s) 绝对 URL", i+1)
			}
			inScope, decidable, err := store.SrcHostInScope(ctx, taskID, u.Hostname())
			if err != nil {
				return err
			}
			if decidable && !inScope {
				return fmt.Errorf("第 %d 步 navigate 目标 %q 不在任务范围内", i+1, u.Hostname())
			}
		case "click", "fill", "press":
			if strings.TrimSpace(step.Selector) == "" {
				return fmt.Errorf("第 %d 步 %s 需要 selector", i+1, step.Action)
			}
		case "wait":
			if step.WaitMS < 0 || step.WaitMS > cdpMaxWaitMS {
				return fmt.Errorf("第 %d 步 wait_ms 必须在 0–%d", i+1, cdpMaxWaitMS)
			}
		case "scroll":
			if step.Y < 0 || step.Y > cdpMaxPageHeight {
				return fmt.Errorf("第 %d 步 scroll y 必须在 0–%d", i+1, cdpMaxPageHeight)
			}
		default:
			return fmt.Errorf("第 %d 步动作 %q 无效（navigate/click/fill/press/wait/scroll）", i+1, step.Action)
		}
	}
	return nil
}

// cdpClient is a minimal flattened-session CDP client. One goroutine reads
// responses; commands are serialized by the caller (our step runner).
type cdpClient struct {
	ws      *websocket.Conn
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan cdpResult
	closed  atomic.Bool
}

type cdpResult struct {
	result json.RawMessage
	err    error
}

func dialCDP(rawURL string) (*cdpClient, error) {
	ws, _, err := websocket.DefaultDialer.Dial(rawURL, nil)
	if err != nil {
		return nil, err
	}
	c := &cdpClient{ws: ws, pending: map[int64]chan cdpResult{}}
	go c.readLoop()
	return c, nil
}

func (c *cdpClient) close() {
	if c == nil || c.closed.Swap(true) {
		return
	}
	_ = c.ws.Close()
	c.mu.Lock()
	for id, ch := range c.pending {
		ch <- cdpResult{err: fmt.Errorf("CDP 连接已关闭")}
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

func (c *cdpClient) readLoop() {
	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			c.closed.Store(true)
			c.mu.Lock()
			for id, ch := range c.pending {
				ch <- cdpResult{err: fmt.Errorf("CDP 读取失败: %w", err)}
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		var msg struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &msg); err != nil || msg.ID == 0 {
			continue // events / malformed frames are not responses
		}
		c.mu.Lock()
		ch := c.pending[msg.ID]
		delete(c.pending, msg.ID)
		c.mu.Unlock()
		if ch == nil {
			continue
		}
		if msg.Error != nil {
			ch <- cdpResult{err: fmt.Errorf("CDP: %s", msg.Error.Message)}
		} else {
			ch <- cdpResult{result: msg.Result}
		}
	}
}

func (c *cdpClient) call(session, method string, params any) (json.RawMessage, error) {
	if c == nil || c.closed.Load() {
		return nil, fmt.Errorf("CDP 连接不可用")
	}
	id := c.nextID.Add(1)
	ch := make(chan cdpResult, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	msg := map[string]any{"id": id, "method": method}
	if session != "" {
		msg["sessionId"] = session
	}
	if params != nil {
		msg["params"] = params
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if err := c.ws.WriteMessage(websocket.TextMessage, raw); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case res := <-ch:
		return res.result, res.err
	case <-time.After(cdpCallTimeout):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("CDP 命令超时：%s", method)
	}
}

func (c *cdpClient) eval(session, expr string) (string, error) {
	res, err := c.call(session, "Runtime.evaluate", map[string]any{
		"expression": expr, "returnByValue": true, "awaitPromise": true,
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	if out.ExceptionDetails != nil {
		return "", fmt.Errorf("页面脚本执行失败：%s", out.ExceptionDetails.Text)
	}
	switch v := out.Result.Value.(type) {
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case nil:
		return "", nil
	default:
		b, _ := json.Marshal(v)
		return string(b), nil
	}
}

// captureWithCDP starts a headless Chromium, performs the steps, and writes a PNG
// to outPath. It always kills the browser process before returning.
func captureWithCDP(ctx context.Context, browser, target, outPath string, width, height int, fullPage bool, steps []agent.SrcScreenshotStep, proxyAddr string) error {
	profile, err := os.MkdirTemp("", "bladepro-cdp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(profile)
	if agentIso.enabled {
		if err := os.Chown(profile, int(agentIso.uid), int(agentIso.gid)); err != nil {
			return err
		}
	}
	args := []string{
		"--proxy-bypass-list=<-loopback>", "--headless=new", "--disable-gpu", "--hide-scrollbars", "--no-first-run",
		"--no-default-browser-check", "--disable-dev-shm-usage", "--disable-extensions",
		"--remote-debugging-port=0", "--user-data-dir=" + profile,
		fmt.Sprintf("--window-size=%d,%d", width, height),
		"about:blank",
	}
	if os.Geteuid() == 0 {
		args = append(args, "--no-sandbox")
	}
	if addr := strings.TrimSpace(proxyAddr); addr != "" {
		proxy := addr
		if !strings.Contains(proxy, "://") {
			proxy = "http://" + proxy
		}
		args = append(args, "--proxy-server="+proxy, "--ignore-certificate-errors")
	}
	stderr := newTailRing(execTailBytes)
	cmd := exec.CommandContext(ctx, browser, args...)
	hardenAgentCmd(cmd)
	startDetached(cmd)
	cmd.WaitDelay = execWaitDelay
	cmd.Cancel = func() error { killGroup(cmd); return nil }
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 Chromium 失败：%w", err)
	}
	defer func() {
		hardKillGroup(cmd)
		_ = cmd.Wait()
	}()

	port, wsPath, err := waitDevToolsPort(profile, cdpStartupTimeout)
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 800 {
			msg = msg[:800]
		}
		return fmt.Errorf("Chromium 未就绪：%w（%s）", err, msg)
	}
	c, err := dialCDP(fmt.Sprintf("ws://127.0.0.1:%d%s", port, wsPath))
	if err != nil {
		return fmt.Errorf("连接 Chromium DevTools 失败：%w", err)
	}
	defer c.close()

	targetRes, err := c.call("", "Target.createTarget", map[string]any{"url": "about:blank"})
	if err != nil {
		return err
	}
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(targetRes, &created); err != nil || created.TargetID == "" {
		return fmt.Errorf("创建浏览器标签失败：%v", err)
	}
	attachRes, err := c.call("", "Target.attachToTarget", map[string]any{"targetId": created.TargetID, "flatten": true})
	if err != nil {
		return err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(attachRes, &attached); err != nil || attached.SessionID == "" {
		return fmt.Errorf("附加浏览器标签失败：%v", err)
	}
	session := attached.SessionID
	_, _ = c.call(session, "Page.enable", nil)
	_, _ = c.call(session, "Runtime.enable", nil)
	if err := c.navigateWait(session, target); err != nil {
		return err
	}
	for i, step := range steps {
		if err := c.runStep(session, step); err != nil {
			return fmt.Errorf("第 %d 步 %s 失败：%w", i+1, step.Action, err)
		}
	}
	if err := c.waitReady(session, 10*time.Second); err != nil {
		return err
	}
	params := map[string]any{"format": "png", "captureBeyondViewport": fullPage}
	if fullPage {
		metrics, err := c.call(session, "Page.getLayoutMetrics", nil)
		if err != nil {
			return err
		}
		var m struct {
			CSSContentSize struct {
				Width  float64 `json:"width"`
				Height float64 `json:"height"`
			} `json:"cssContentSize"`
		}
		if err := json.Unmarshal(metrics, &m); err != nil {
			return err
		}
		h := m.CSSContentSize.Height
		if h > cdpMaxPageHeight {
			h = cdpMaxPageHeight
		}
		if h > 0 && m.CSSContentSize.Width > 0 {
			params["clip"] = map[string]any{
				"x": 0, "y": 0, "width": m.CSSContentSize.Width, "height": h, "scale": 1,
			}
		}
	}
	shot, err := c.call(session, "Page.captureScreenshot", params)
	if err != nil {
		return err
	}
	var encoded struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(shot, &encoded); err != nil || encoded.Data == "" {
		return fmt.Errorf("截图数据为空")
	}
	png, err := base64.StdEncoding.DecodeString(encoded.Data)
	if err != nil {
		return err
	}
	if len(png) == 0 {
		return fmt.Errorf("截图数据为空")
	}
	return os.WriteFile(outPath, png, 0o600)
}

// waitDevToolsPort reads Chromium's DevToolsActivePort file (port + browser ws path).
func waitDevToolsPort(profile string, timeout time.Duration) (int, string, error) {
	path := filepath.Join(profile, "DevToolsActivePort")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f, err := os.Open(path); err == nil {
			sc := bufio.NewScanner(f)
			var lines []string
			for sc.Scan() && len(lines) < 2 {
				lines = append(lines, strings.TrimSpace(sc.Text()))
			}
			_ = f.Close()
			if len(lines) == 2 {
				port, err := strconv.Atoi(lines[0])
				if err == nil && port > 0 && strings.HasPrefix(lines[1], "/") {
					return port, lines[1], nil
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 0, "", fmt.Errorf("%v 内未生成 DevToolsActivePort", timeout)
}

func (c *cdpClient) waitReady(session string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, err := c.eval(session, "document.readyState")
		if err == nil && state == "complete" {
			// SPA 在 load 之后仍会渲染一轮；给一个短稳定窗口。
			time.Sleep(300 * time.Millisecond)
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("页面在 %v 内未进入 complete", timeout)
}

func (c *cdpClient) navigateWait(session, target string) error {
	if _, err := c.call(session, "Page.navigate", map[string]any{"url": target}); err != nil {
		return err
	}
	return c.waitReady(session, 15*time.Second)
}

func (c *cdpClient) runStep(session string, step agent.SrcScreenshotStep) error {
	switch strings.ToLower(strings.TrimSpace(step.Action)) {
	case "navigate":
		return c.navigateWait(session, step.URL)
	case "wait":
		ms := step.WaitMS
		if ms <= 0 {
			ms = 300
		}
		if ms > cdpMaxWaitMS {
			ms = cdpMaxWaitMS
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return nil
	case "scroll":
		_, err := c.eval(session, fmt.Sprintf("window.scrollTo(0,%d); true", step.Y))
		time.Sleep(200 * time.Millisecond)
		return err
	case "click":
		sel, _ := json.Marshal(step.Selector)
		ok, err := c.eval(session, fmt.Sprintf(`(() => { const el=document.querySelector(%s); if(!el) return false; el.click(); return true; })()`, sel))
		if err != nil {
			return err
		}
		if ok != "true" {
			return fmt.Errorf("selector 未命中：%s", step.Selector)
		}
		time.Sleep(300 * time.Millisecond)
		return nil
	case "fill":
		sel, _ := json.Marshal(step.Selector)
		val, _ := json.Marshal(step.Value)
		ok, err := c.eval(session, fmt.Sprintf(`(() => {
  const el=document.querySelector(%s); if(!el) return false;
  const d=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value');
  if (d && d.set) d.set.call(el, %s); else el.value = %s;
  el.dispatchEvent(new Event('input',{bubbles:true}));
  el.dispatchEvent(new Event('change',{bubbles:true}));
  return true;
})()`, sel, val, val))
		if err != nil {
			return err
		}
		if ok != "true" {
			return fmt.Errorf("selector 未命中：%s", step.Selector)
		}
		return nil
	case "press":
		sel, _ := json.Marshal(step.Selector)
		if _, err := c.eval(session, fmt.Sprintf(`(() => { const el=document.querySelector(%s); if(el) el.focus(); return !!el; })()`, sel)); err != nil {
			return err
		}
		key := strings.TrimSpace(step.Value)
		if key == "" {
			key = "Enter"
		}
		code, vk := keyInfo(key)
		if _, err := c.call(session, "Input.dispatchKeyEvent", map[string]any{
			"type": "keyDown", "key": key, "code": code, "windowsVirtualKeyCode": vk, "nativeVirtualKeyCode": vk,
		}); err != nil {
			return err
		}
		_, err := c.call(session, "Input.dispatchKeyEvent", map[string]any{
			"type": "keyUp", "key": key, "code": code, "windowsVirtualKeyCode": vk, "nativeVirtualKeyCode": vk,
		})
		time.Sleep(200 * time.Millisecond)
		return err
	}
	return fmt.Errorf("未知动作：%s", step.Action)
}

func keyInfo(key string) (code string, vk int) {
	switch strings.ToLower(key) {
	case "enter":
		return "Enter", 13
	case "tab":
		return "Tab", 9
	case "escape", "esc":
		return "Escape", 27
	case "arrowdown":
		return "ArrowDown", 40
	case "arrowup":
		return "ArrowUp", 38
	}
	if len(key) == 1 {
		return strings.ToUpper(key), int(strings.ToUpper(key)[0])
	}
	return key, 0
}
