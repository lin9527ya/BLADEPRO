package agent

// 逆向/带外检测原生工具集（Ported from quiver, AGPL-3.0；见 docs/third-party/QUIVER.md）：
//   jadx_*   — APK/DEX 反编译（shell 出 jadx.bat，产物落盘可 Read/Bash 检索）
//   dnslog_* — 带外回连检测（直接 HTTP 调 dnslog API）
//   ghidra_* — 原生二进制分析（pyghidra CLI）

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	actool "github.com/lin9527ya/Norma/tool"
)

// ── 配置（包级，server 启动时 wire）──

// ReverseConfig holds paths/URLs for the reverse engineering + feishu toolchain.
type ReverseConfig struct {
	JadxBin    string // jadx.bat path
	JadxOutDir string // where decompiled projects go
	GhidraHome string // GHIDRA_INSTALL_DIR
	DnslogAPI  string // dnslog service base URL
	// 飞书自建应用（照搬用户 Python MCP 的实现语义）
	FeishuAppID     string
	FeishuAppSecret string
	FeishuChatID    string // 默认目标群
}

// ReverseDefaults is wired by the server; nil = tools report unavailable.
var ReverseDefaults *ReverseConfig

func revOK() bool { return ReverseDefaults != nil }

// ── jadx：进程管理 ──

type jadxProject struct {
	Out     string
	Status  string // running|done|failed
	PID     int
	Started time.Time
}

var (
	jadxMu       sync.Mutex
	jadxProjects = map[string]*jadxProject{} // apk path → project
	jadxCurrent  string
)

// jadxDecompile starts a background jadx run.
func (t *ToolSet) jadxDecompile() actool.CoreTool {
	return writeTool("jadx_decompile",
		"(逆向)启动 jadx 反编译一个 APK/DEX/JAR。后台运行，用 jadx_status 查进度。完成后用 jadx_search/jadx_read 检索源码，或直接 Read/Bash 产物目录。",
		obj(map[string]any{
			"path":           str("APK/DEX/JAR 文件路径"),
			"skip_resources": map[string]any{"type": "boolean", "description": "跳过资源解码（更快，默认 true）"},
			"deobf":          map[string]any{"type": "boolean", "description": "启用反混淆重命名（默认 false）"},
		}, "path"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !revOK() || ReverseDefaults.JadxBin == "" {
				return actool.Errorf("jadx 未配置（ReverseDefaults.JadxBin 为空）"), nil
			}
			var a struct {
				Path          string `json:"path"`
				SkipResources bool   `json:"skip_resources"`
				Deobf         bool   `json:"deobf"`
			}
			if err := json.Unmarshal(in, &a); err != nil || a.Path == "" {
				return actool.Errorf("path 必填"), nil
			}
			abs, err := filepath.Abs(a.Path)
			if err != nil {
				return actool.Errorf("路径无效"), nil
			}
			if _, err := os.Stat(abs); os.IsNotExist(err) {
				return actool.Errorf(fmt.Sprintf("文件不存在: %s", abs)), nil
			}

			outDir := filepath.Join(ReverseDefaults.JadxOutDir, filepath.Base(abs)+"_jadx")
			jadxMu.Lock()
			if p, ok := jadxProjects[abs]; ok && p.Status == "running" {
				jadxMu.Unlock()
				return actool.Errorf(fmt.Sprintf("该文件正在反编译中（PID %d），用 jadx_status 查进度", p.PID)), nil
			}
			// 已完成 → 直接复用
			if _, err := os.Stat(filepath.Join(outDir, "sources")); err == nil {
				jadxProjects[abs] = &jadxProject{Out: outDir, Status: "done"}
				jadxCurrent = abs
				jadxMu.Unlock()
				return actool.Text(fmt.Sprintf("already decompiled: %s\nsources: %s\n用 jadx_search / jadx_read 检索", abs, outDir)), nil
			}
			jadxMu.Unlock()

			os.MkdirAll(outDir, 0o755)
			cmd := []string{ReverseDefaults.JadxBin, "-d", outDir, "--log-level", "INFO"}
			if a.SkipResources {
				cmd = append(cmd, "--no-res")
			}
			if a.Deobf {
				cmd = append(cmd, "--deobf")
			}
			cmd = append(cmd, abs)

			proc := exec.Command(cmd[0], cmd[1:]...)
			if err := proc.Start(); err != nil {
				return actool.Errorf(fmt.Sprintf("启动 jadx 失败: %v", err)), nil
			}

			jadxMu.Lock()
			jadxProjects[abs] = &jadxProject{Out: outDir, Status: "running", PID: proc.Process.Pid, Started: time.Now()}
			jadxCurrent = abs
			jadxMu.Unlock()

			// 后台等待完成
			go func() {
				_ = proc.Wait()
				jadxMu.Lock()
				if p, ok := jadxProjects[abs]; ok {
					if _, err := os.Stat(filepath.Join(outDir, "sources")); err == nil {
						p.Status = "done"
					} else {
						p.Status = "failed"
					}
				}
				jadxMu.Unlock()
			}()

			return actool.Text(fmt.Sprintf("decompile started: %s\nout: %s\nPID: %d\n用 jadx_status 查进度；大 APK 需要几分钟", abs, outDir, proc.Process.Pid)), nil
		})
}

// jadxStatus reports the current project's status.
func (t *ToolSet) jadxStatus() actool.CoreTool {
	return readTool("jadx_status",
		"(逆向)查询当前 jadx 反编译项目的状态（running/done/failed + 已产出文件数）。",
		obj(map[string]any{}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !revOK() {
				return actool.Errorf("jadx 未配置"), nil
			}
			jadxMu.Lock()
			defer jadxMu.Unlock()
			if jadxCurrent == "" {
				return actool.Text("no project opened; call jadx_decompile first"), nil
			}
			p := jadxProjects[jadxCurrent]
			fileCount := countFiles(filepath.Join(p.Out, "sources"), ".java") + countFiles(filepath.Join(p.Out, "sources"), ".kt")
			return actool.Text(fmt.Sprintf("project: %s\nstatus: %s\nsources: %s\nfiles: %d", jadxCurrent, p.Status, p.Out, fileCount)), nil
		})
}

// jadxSearch greps decompiled sources.
func (t *ToolSet) jadxSearch() actool.CoreTool {
	return readTool("jadx_search",
		"(逆向)在反编译产物里搜索代码（子串或正则）。返回 文件:行号:内容 格式的命中，分页。",
		obj(map[string]any{
			"pattern":  str("搜索内容（子串或正则）"),
			"is_regex": map[string]any{"type": "boolean", "description": "是否为正则（默认 false=子串）"},
			"filter":   str("文件名过滤（如 'webview' 或 'com/tencent'，可选）"),
			"offset":   intp("分页偏移（默认 0）"),
			"count":    intp("每页条数（默认 30）"),
		}, "pattern"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !revOK() {
				return actool.Errorf("jadx 未配置"), nil
			}
			var a struct {
				Pattern string `json:"pattern"`
				IsRegex bool   `json:"is_regex"`
				Filter  string `json:"filter"`
				Offset  int    `json:"offset"`
				Count   int    `json:"count"`
			}
			json.Unmarshal(in, &a)
			if a.Count == 0 {
				a.Count = 30
			}
			jadxMu.Lock()
			if jadxCurrent == "" {
				jadxMu.Unlock()
				return actool.Errorf("no project; call jadx_decompile first"), nil
			}
			p := jadxProjects[jadxCurrent]
			jadxMu.Unlock()
			if p.Status != "done" {
				return actool.Errorf(fmt.Sprintf("project status is %s; wait for done", p.Status)), nil
			}
			hits := grepDir(filepath.Join(p.Out, "sources"), a.Pattern, a.IsRegex, a.Filter, 200)
			start := a.Offset
			if start > len(hits) {
				start = len(hits)
			}
			end := start + a.Count
			if end > len(hits) {
				end = len(hits)
			}
			return actool.Text(fmt.Sprintf("total: %d\noffset: %d\ncount: %d\n\n%s", len(hits), start, end-start, strings.Join(hits[start:end], "\n"))), nil
		})
}

// jadxRead reads a decompiled file paginated.
func (t *ToolSet) jadxRead() actool.CoreTool {
	return readTool("jadx_read",
		"(逆向)读取反编译产物中的一个文件（分页）。接受 jadx_search 返回的文件路径，或点分类名（如 com.example.MainActivity）。",
		obj(map[string]any{
			"path":   str("文件路径（jadx_search 的返回值，或 com.example.Class 形式）"),
			"offset": intp("行偏移（默认 0）"),
			"limit":  intp("行数（默认 200）"),
		}, "path"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !revOK() {
				return actool.Errorf("jadx 未配置"), nil
			}
			var a struct {
				Path   string `json:"path"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			json.Unmarshal(in, &a)
			if a.Limit == 0 {
				a.Limit = 200
			}
			jadxMu.Lock()
			if jadxCurrent == "" {
				jadxMu.Unlock()
				return actool.Errorf("no project; call jadx_decompile first"), nil
			}
			p := jadxProjects[jadxCurrent]
			jadxMu.Unlock()

			fp := resolveInProject(p.Out, a.Path)
			if fp == "" {
				return actool.Errorf(fmt.Sprintf("file not found in project: %s", a.Path)), nil
			}
			data, err := os.ReadFile(fp)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("read: %v", err)), nil
			}
			lines := strings.Split(string(data), "\n")
			start := a.Offset
			if start > len(lines) {
				start = len(lines)
			}
			end := start + a.Limit
			if end > len(lines) {
				end = len(lines)
			}
			return actool.Text(fmt.Sprintf("file: %s\ntotal_lines: %d\noffset: %d\n\n%s", fp, len(lines), start, strings.Join(lines[start:end], "\n"))), nil
		})
}

// ── dnslog：带外回连检测 ──

type dnslogSession struct {
	Key    string `json:"key"`
	Token  string `json:"token"`
	Domain string `json:"domain"`
	Dns    string `json:"dns,omitempty"`
	Http   string `json:"http,omitempty"`
	Ldap   string `json:"ldap,omitempty"`
	Rmi    string `json:"rmi,omitempty"`
}

var dnslogLast *dnslogSession

func (t *ToolSet) dnslogSession() actool.CoreTool {
	return readTool("dnslog_session",
		"(带外检测)新建一个 dnslog 回连会话，返回域名和 DNS/HTTP/LDAP/RMI 四个通道地址。把地址拼进 payload 里，用 dnslog_records 查回连。",
		obj(map[string]any{}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !revOK() || ReverseDefaults.DnslogAPI == "" {
				return actool.Errorf("dnslog 未配置（ReverseDefaults.DnslogAPI 为空）"), nil
			}
			body, err := httpPostJSON(ctx, ReverseDefaults.DnslogAPI+"/api/session", nil, 10*time.Second)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("创建会话失败: %v", err)), nil
			}
			var resp struct {
				Key        string            `json:"key"`
				Token      string            `json:"token"`
				BaseDomain string            `json:"baseDomain"`
				Payloads   map[string]string `json:"payloads"`
			}
			if err := json.Unmarshal(body, &resp); err != nil {
				return actool.Errorf(fmt.Sprintf("解析响应失败: %v", err)), nil
			}
			domain := resp.Payloads["domain"]
			if domain == "" {
				domain = resp.Token + "." + resp.BaseDomain
			}
			s := &dnslogSession{
				Key: resp.Key, Token: resp.Token, Domain: domain,
				Dns: resp.Payloads["dns"], Http: resp.Payloads["http"],
				Ldap: resp.Payloads["ldap"], Rmi: resp.Payloads["rmi"],
			}
			dnslogLast = s
			b, _ := json.MarshalIndent(s, "", "  ")
			return actool.Text(string(b)), nil
		})
}

func (t *ToolSet) dnslogRecords() actool.CoreTool {
	return readTool("dnslog_records",
		"(带外检测)查询 dnslog 回连记录（DNS/HTTP/LDAP/RMI）。验证 SSRF/RCE/XXE 等带外漏洞是否触发。",
		obj(map[string]any{
			"types": str("通道过滤：dns,http,ldap,rmi 逗号分隔（留空=全部）"),
			"limit": intp("条数上限（默认 50）"),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !revOK() || ReverseDefaults.DnslogAPI == "" {
				return actool.Errorf("dnslog 未配置"), nil
			}
			if dnslogLast == nil {
				return actool.Errorf("no session; call dnslog_session first"), nil
			}
			var a struct {
				Types string `json:"types"`
				Limit int    `json:"limit"`
			}
			json.Unmarshal(in, &a)
			if a.Limit == 0 {
				a.Limit = 50
			}
			types := []string{"all"}
			if a.Types != "" {
				types = strings.FieldsFunc(a.Types, func(r rune) bool { return r == ',' || r == ' ' })
			}
			var all []map[string]any
			for _, svc := range types {
				url := fmt.Sprintf("%s/api/records?service=%s&limit=%d", ReverseDefaults.DnslogAPI, svc, a.Limit)
				body, err := httpGetJSON(ctx, url, dnslogLast.Key, 10*time.Second)
				if err != nil {
					continue
				}
				var resp struct {
					Records []map[string]any `json:"records"`
				}
				if json.Unmarshal(body, &resp) == nil {
					all = append(all, resp.Records...)
				}
			}
			b, _ := json.MarshalIndent(map[string]any{"total": len(all), "records": all}, "", "  ")
			return actool.Text(string(b)), nil
		})
}

// ── ghidra：原生二进制分析 ──

func (t *ToolSet) ghidraAnalyze() actool.CoreTool {
	return writeTool("ghidra_analyze",
		"(逆向)用 Ghidra 分析一个原生二进制（SO/EXE/DLL）。后台运行，产物存到 Ghidra 项目目录。",
		obj(map[string]any{
			"path": str("二进制文件路径（.so/.exe/.dll）"),
		}, "path"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !revOK() || ReverseDefaults.GhidraHome == "" {
				return actool.Errorf("ghidra 未配置（ReverseDefaults.GhidraHome 为空）"), nil
			}
			var a struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(in, &a); err != nil || a.Path == "" {
				return actool.Errorf("path 必填"), nil
			}
			abs, _ := filepath.Abs(a.Path)
			if _, err := os.Stat(abs); os.IsNotExist(err) {
				return actool.Errorf(fmt.Sprintf("文件不存在: %s", abs)), nil
			}
			// pyghidra headless 分析
			script := fmt.Sprintf(`
import pyghidra
pyghidra.start()
program = pyghidra.open_program("%s")
print("analyzed:", program.getName())
print("functions:", program.getFunctionManager().getFunctionCount())
pyghidra.close_program()
`, filepath.ToSlash(abs))
			cmd := exec.CommandContext(ctx, "python", "-c", script)
			cmd.Env = append(os.Environ(), "GHIDRA_INSTALL_DIR="+ReverseDefaults.GhidraHome)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				return actool.Errorf(fmt.Sprintf("ghidra 分析失败: %v\n%s", err, stderr.String()[:min(500, stderr.Len())])), nil
			}
			return actool.Text(fmt.Sprintf("analyzed: %s\n%s\n%s", abs, stdout.String(), "用 ghidra_decompile 查看具体函数；或直接 Read 产物目录")), nil
		})
}

// ── 工具注册 ──

// reverseTools returns all reverse engineering / OAST tools.
func (t *ToolSet) reverseTools() []actool.CoreTool {
	return []actool.CoreTool{
		t.jadxDecompile(), t.jadxStatus(), t.jadxSearch(), t.jadxRead(),
		t.dnslogSession(), t.dnslogRecords(),
		t.ghidraAnalyze(),
	}
}

// ── helpers ──

func hideWindow() *os.ProcAttr { return nil } // cross-platform: os.ProcAttr has no HideWindow; exec handles it

func countFiles(root, ext string) int {
	n := 0
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(path, ext) {
			n++
		}
		return nil
	})
	return n
}

func grepDir(root, pattern string, isRegex bool, fileFilter string, maxHits int) []string {
	var re *regexp.Regexp
	if isRegex {
		re, _ = regexp.Compile(pattern)
	}
	needle := strings.ToLower(pattern)
	var hits []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if !strings.HasSuffix(name, ".java") && !strings.HasSuffix(name, ".kt") && !strings.HasSuffix(name, ".smali") {
			return nil
		}
		if fileFilter != "" && !strings.Contains(strings.ToLower(path), strings.ToLower(fileFilter)) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(data), "\n") {
			var match bool
			if re != nil {
				match = re.MatchString(line)
			} else {
				match = strings.Contains(strings.ToLower(line), needle)
			}
			if match {
				trimmed := strings.TrimSpace(line)
				if len(trimmed) > 300 {
					trimmed = trimmed[:300] + "…"
				}
				hits = append(hits, fmt.Sprintf("%s:%d:%s", filepath.ToSlash(rel), i+1, trimmed))
				if len(hits) >= maxHits {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	sort.Strings(hits)
	return hits
}

func resolveInProject(outDir, rel string) string {
	rel = strings.TrimSpace(rel)
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.TrimPrefix(rel, "/")
	if !strings.Contains(rel, "/") && !strings.HasSuffix(rel, ".xml") {
		rel = strings.ReplaceAll(rel, ".", "/")
	}
	if !strings.HasSuffix(rel, ".java") && !strings.HasSuffix(rel, ".kt") && !strings.HasSuffix(rel, ".smali") && !strings.HasSuffix(rel, ".xml") {
		rel += ".java"
	}
	candidates := []string{
		filepath.Join(outDir, "sources", rel),
		filepath.Join(outDir, rel),
		filepath.Join(outDir, "resources", rel),
	}
	for _, c := range candidates {
		abs, _ := filepath.Abs(c)
		if safe, _ := filepath.Rel(outDir, abs); !strings.HasPrefix(safe, "..") {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	return ""
}

func httpPostJSON(ctx context.Context, url string, body []byte, timeout time.Duration) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return buf.Bytes(), nil
}

func httpGetJSON(ctx context.Context, url, key string, timeout time.Duration) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("x-dnslog-key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return buf.Bytes(), nil
}
