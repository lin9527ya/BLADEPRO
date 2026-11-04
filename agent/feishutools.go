package agent

// QUIVER 飞书原生工具（照用户 Python MCP server.py 翻译成 Go）：
//   feishu_send_text   — 发纯文本
//   feishu_send_card   — 发 Markdown 卡片
//   feishu_send_vuln   — 发 SRC 漏洞报告卡片（按等级配色）
//   feishu_send_image  — 发本地截图
//   feishu_send_file   — 发本地文件
//
// 凭据从 ReverseConfig 读取（server 启动时 wire）；token 缓存 + 失效自动刷新 + 网络重试。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	actool "github.com/lin9527ya/Norma/tool"
)

const feishuBase = "https://open.feishu.cn/open-apis"
const feishuMaxText = 28000

// token 缓存
var (
	feishuTokenMu    sync.Mutex
	feishuTokenCache = struct {
		token  string
		expire time.Time
	}{}
)

func feishuToken(ctx context.Context) (string, error) {
	rc := ReverseDefaults
	if rc == nil || rc.FeishuAppID == "" || rc.FeishuAppSecret == "" {
		return "", fmt.Errorf("飞书未配置（需要 FeishuAppID/FeishuAppSecret）")
	}
	feishuTokenMu.Lock()
	defer feishuTokenMu.Unlock()
	if feishuTokenCache.token != "" && time.Now().Before(feishuTokenCache.expire) {
		return feishuTokenCache.token, nil
	}
	body, _ := json.Marshal(map[string]string{
		"app_id": rc.FeishuAppID, "app_secret": rc.FeishuAppSecret,
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", feishuBase+"/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("获取 token 失败: %v", err)
	}
	defer resp.Body.Close()
	var data struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("解析 token 响应失败: %v", err)
	}
	if data.Code != 0 || data.TenantAccessToken == "" {
		return "", fmt.Errorf("获取 token 失败 %d: %s", data.Code, data.Msg)
	}
	exp := time.Duration(data.Expire) * time.Second
	if exp <= 0 {
		exp = 2 * time.Hour
	}
	feishuTokenCache.token = data.TenantAccessToken
	feishuTokenCache.expire = time.Now().Add(exp - 200*time.Second)
	return data.TenantAccessToken, nil
}

func feishuAPICall(ctx context.Context, method, path string, body any) (map[string]any, error) {
	tok, err := feishuToken(ctx)
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, feishuBase+path, rd)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}
	if code, _ := data["code"].(float64); code != 0 {
		msg, _ := data["msg"].(string)
		return nil, fmt.Errorf("飞书 API 错误 %d: %s", int(code), msg)
	}
	return data, nil
}

func feishuSendMsg(ctx context.Context, msgType string, content map[string]any, chatID string) (map[string]any, error) {
	rc := ReverseDefaults
	cid := chatID
	if cid == "" {
		cid = rc.FeishuChatID
	}
	if cid == "" {
		return nil, fmt.Errorf("未指定 chat_id 且未配置默认群")
	}
	contentJSON, _ := json.Marshal(content)
	return feishuAPICall(ctx, "POST", "/im/v1/messages?receive_id_type=chat_id", map[string]any{
		"receive_id": cid,
		"msg_type":   msgType,
		"content":    string(contentJSON),
	})
}

func feishuClip(text string) string {
	if len(text) > feishuMaxText {
		return text[:feishuMaxText] + "\n...(内容过长已截断)"
	}
	return text
}

var feishuSeverityColor = map[string]string{
	"严重": "red", "critical": "red",
	"高危": "red", "high": "red",
	"中危": "orange", "medium": "orange",
	"低危": "blue", "low": "blue",
	"信息": "grey", "info": "grey",
}

// ── 工具 ──

func (t *ToolSet) feishuSendText() actool.CoreTool {
	return writeTool("feishu_send_text",
		"(飞书)发送纯文本通知。适合长任务完成提醒、进度更新、需要人工介入时呼叫。",
		obj(map[string]any{
			"text":    str("消息内容"),
			"chat_id": str("可选覆盖默认群"),
		}, "text"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !revOK() {
				return actool.Errorf("飞书未配置"), nil
			}
			var a struct {
				Text   string `json:"text"`
				ChatID string `json:"chat_id"`
			}
			json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Text) == "" {
				return actool.Errorf("text 必填"), nil
			}
			_, err := feishuSendMsg(ctx, "text", map[string]any{"text": feishuClip(a.Text)}, a.ChatID)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("发送失败: %v", err)), nil
			}
			return actool.Text("sent"), nil
		})
}

func (t *ToolSet) feishuSendCard() actool.CoreTool {
	return writeTool("feishu_send_card",
		"(飞书)发送 Markdown 卡片通知（带标题栏，适合重要通知）。",
		obj(map[string]any{
			"title":    str("卡片标题"),
			"markdown": str("正文（飞书 lark_md：**加粗** [链接]() 等）"),
			"color":    str("标题栏颜色 blue|green|red|orange|turquoise|grey（默认 blue）"),
			"chat_id":  str("可选覆盖默认群"),
		}, "title", "markdown"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !revOK() {
				return actool.Errorf("飞书未配置"), nil
			}
			var a struct {
				Title    string `json:"title"`
				Markdown string `json:"markdown"`
				Color    string `json:"color"`
				ChatID   string `json:"chat_id"`
			}
			json.Unmarshal(in, &a)
			if a.Title == "" || a.Markdown == "" {
				return actool.Errorf("title 和 markdown 必填"), nil
			}
			if a.Color == "" {
				a.Color = "blue"
			}
			card := map[string]any{
				"config": map[string]any{"wide_screen_mode": true},
				"header": map[string]any{
					"title":    map[string]any{"tag": "plain_text", "content": a.Title},
					"template": a.Color,
				},
				"elements": []any{
					map[string]any{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": feishuClip(a.Markdown)}},
				},
			}
			_, err := feishuSendMsg(ctx, "interactive", card, a.ChatID)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("发送失败: %v", err)), nil
			}
			return actool.Text("sent"), nil
		})
}

func (t *ToolSet) feishuSendVuln() actool.CoreTool {
	return writeTool("feishu_send_vuln",
		"(飞书)发送 SRC 漏洞发现报告卡片——确认漏洞后立即调用，按等级自动配色（严重/高危红、中危橙、低危蓝）。",
		obj(map[string]any{
			"vendor":   str("厂商名"),
			"title":    str("漏洞一句话标题"),
			"severity": str("严重|高危|中危|低危|信息（或 critical/high/medium/low/info）"),
			"detail":   str("漏洞详情（复现步骤/影响面，支持 Markdown）"),
			"url":      str("目标链接（可选）"),
			"chat_id":  str("可选覆盖默认群"),
		}, "vendor", "title", "severity", "detail"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !revOK() {
				return actool.Errorf("飞书未配置"), nil
			}
			var a struct {
				Vendor   string `json:"vendor"`
				Title    string `json:"title"`
				Severity string `json:"severity"`
				Detail   string `json:"detail"`
				URL      string `json:"url"`
				ChatID   string `json:"chat_id"`
			}
			json.Unmarshal(in, &a)
			if a.Vendor == "" || a.Title == "" || a.Severity == "" {
				return actool.Errorf("vendor、title、severity 必填"), nil
			}
			color := feishuSeverityColor[strings.ToLower(a.Severity)]
			if color == "" {
				color = feishuSeverityColor[a.Severity]
			}
			if color == "" {
				color = "turquoise"
			}
			title := fmt.Sprintf("[SRC漏洞] %s - %s", a.Vendor, a.Title)
			if color == "red" {
				title = "🔴 " + title
			}
			md := fmt.Sprintf("**厂商:** %s\n**等级:** %s\n**时间:** %s\n**URL:** %s\n\n%s",
				a.Vendor, a.Severity, time.Now().Format("2006-01-02 15:04:05"), orDash(a.URL), a.Detail)
			if a.URL != "" {
				md += fmt.Sprintf("\n\n[打开目标链接](%s)", a.URL)
			}
			card := map[string]any{
				"config": map[string]any{"wide_screen_mode": true},
				"header": map[string]any{
					"title":    map[string]any{"tag": "plain_text", "content": title},
					"template": color,
				},
				"elements": []any{
					map[string]any{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": feishuClip(md)}},
				},
			}
			_, err := feishuSendMsg(ctx, "interactive", card, a.ChatID)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("发送失败: %v", err)), nil
			}
			return actool.Text("vuln report sent"), nil
		})
}

func (t *ToolSet) feishuSendImage() actool.CoreTool {
	return writeTool("feishu_send_image",
		"(飞书)发送本地截图到群（png/jpg/gif，≤10MB）——漏洞截图证据归档。",
		obj(map[string]any{
			"path":    str("本地图片路径"),
			"chat_id": str("可选覆盖默认群"),
		}, "path"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !revOK() {
				return actool.Errorf("飞书未配置"), nil
			}
			var a struct {
				Path   string `json:"path"`
				ChatID string `json:"chat_id"`
			}
			json.Unmarshal(in, &a)
			imageKey, err := feishuUploadFile(ctx, a.Path, "image", "image", "image_key")
			if err != nil {
				return actool.Errorf(fmt.Sprintf("上传失败: %v", err)), nil
			}
			_, err = feishuSendMsg(ctx, "image", map[string]any{"image_key": imageKey}, a.ChatID)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("发送失败: %v", err)), nil
			}
			return actool.Text("image sent"), nil
		})
}

func (t *ToolSet) feishuSendFile() actool.CoreTool {
	return writeTool("feishu_send_file",
		"(飞书)发送本地文件到群（pdf/doc/xls/ppt/mp4/其他，≤30MB）——测试报告/取证文件归档。",
		obj(map[string]any{
			"path":      str("本地文件路径"),
			"file_type": str("可选强制指定 pdf|doc|xls|ppt|mp4|opus|stream"),
			"chat_id":   str("可选覆盖默认群"),
		}, "path"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if !revOK() {
				return actool.Errorf("飞书未配置"), nil
			}
			var a struct {
				Path     string `json:"path"`
				FileType string `json:"file_type"`
				ChatID   string `json:"chat_id"`
			}
			json.Unmarshal(in, &a)
			ft := a.FileType
			if ft == "" {
				ft = feishuFileType(filepath.Ext(a.Path))
			}
			fileKey, err := feishuUploadFile(ctx, a.Path, "file", ft, "file_key")
			if err != nil {
				return actool.Errorf(fmt.Sprintf("上传失败: %v", err)), nil
			}
			_, err = feishuSendMsg(ctx, "file", map[string]any{"file_key": fileKey}, a.ChatID)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("发送失败: %v", err)), nil
			}
			return actool.Text("file sent"), nil
		})
}

// ── upload helpers ──

func feishuUploadFile(ctx context.Context, path, field, fileType, keyName string) (string, error) {
	tok, err := feishuToken(ctx)
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("文件不存在: %s", path)
	}
	defer f.Close()
	fi, _ := f.Stat()
	if fi.Size() > 30*1024*1024 {
		return "", fmt.Errorf("文件超过 30MB")
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if field == "image" {
		w.WriteField("image_type", "message")
	} else {
		w.WriteField("file_type", fileType)
		w.WriteField("file_name", filepath.Base(path))
	}
	fw, _ := w.CreateFormFile(field, filepath.Base(path))
	io.Copy(fw, f)
	w.Close()

	endpoint := "/im/v1/images"
	if field == "file" {
		endpoint = "/im/v1/files"
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", feishuBase+endpoint, &buf)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var data struct {
		Code int               `json:"code"`
		Msg  string            `json:"msg"`
		Data map[string]string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	if data.Code != 0 {
		return "", fmt.Errorf("上传失败 %d: %s", data.Code, data.Msg)
	}
	key := data.Data[keyName]
	if key == "" {
		return "", fmt.Errorf("上传成功但未返回 %s", keyName)
	}
	return key, nil
}

func feishuFileType(ext string) string {
	switch strings.ToLower(ext) {
	case ".pdf":
		return "pdf"
	case ".doc", ".docx":
		return "doc"
	case ".xls", ".xlsx":
		return "xls"
	case ".ppt", ".pptx":
		return "ppt"
	case ".mp4":
		return "mp4"
	case ".opus":
		return "opus"
	default:
		return "stream"
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// FeishuSendCardDirect sends a card from server-side code (not via LLM tool).
// Used by notifyAskFeishu for approval notifications.
func FeishuSendCardDirect(ctx context.Context, title, markdown, color string) (map[string]any, error) {
	card := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": title},
			"template": color,
		},
		"elements": []any{
			map[string]any{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": markdown}},
		},
	}
	return feishuSendMsg(ctx, "interactive", card, "")
}

// feishuTools returns all feishu communication tools.
func (t *ToolSet) feishuTools() []actool.CoreTool {
	return []actool.CoreTool{
		t.feishuSendText(), t.feishuSendCard(), t.feishuSendVuln(),
		t.feishuSendImage(), t.feishuSendFile(),
	}
}
