package agent

// 多模态看图工具（Ported from quiver, AGPL-3.0；旁路调用，不改 norma SDK）：
//   view_image(path, question?) — 读本地图片 → base64 → 直接 HTTP 调 vision API → 返回模型描述
//
// 走 Anthropic Messages API（content_block type=image），走当前激活的 LLM 配置。
// 如果当前配置的模型不支持看图（纯文本模型），报错提示换 vision 模型。

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	actool "github.com/lin9527ya/Norma/tool"
)

// VisionConfig holds the vision API settings (wired from ReverseConfig or standalone).
type VisionConfig struct {
	BaseURL string // LLM API base URL (anthropic 格式)
	APIKey  string
	Model   string // 必须支持 vision 的模型（如 claude-sonnet-4-20250514）
}

// VisionDefaults is wired by the server; nil = tool unavailable.
var VisionDefaults *VisionConfig

var visionMimeTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

const visionMaxImageMB = 5

// viewImage builds the multimodal image reading tool.
func (t *ToolSet) viewImage() actool.CoreTool {
	return readTool("view_image",
		"(看图)用视觉模型分析一张本地图片。支持 png/jpg/jpeg/gif/webp（≤5MB）。适合：网页截图分析UI漏洞、验证码识别、架构图/流程图理解、App界面截图分析、流量截图中的敏感信息提取。",
		obj(map[string]any{
			"path":     str("图片文件路径（绝对或相对工作目录）"),
			"question": str("想从图片中了解什么（可选，默认'描述图片中的关键信息'）"),
		}, "path"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if VisionDefaults == nil || VisionDefaults.APIKey == "" {
				return actool.Errorf("view_image 不可用：未配置 vision 模型（在系统设置里配 vision_base_url / vision_api_key / vision_model）"), nil
			}
			var a struct {
				Path     string `json:"path"`
				Question string `json:"question"`
			}
			if err := json.Unmarshal(in, &a); err != nil || strings.TrimSpace(a.Path) == "" {
				return actool.Errorf("path 必填"), nil
			}
			if a.Question == "" {
				a.Question = "描述图片中的关键信息，重点关注：页面上的敏感数据、UI元素、错误信息、验证码内容、网络架构等安全测试相关的内容。"
			}

			// 读文件
			abs, err := filepath.Abs(a.Path)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("路径无效: %v", err)), nil
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("文件不存在或不可读: %s", abs)), nil
			}
			ext := strings.ToLower(filepath.Ext(abs))
			mime, ok := visionMimeTypes[ext]
			if !ok {
				return actool.Errorf(fmt.Sprintf("不支持的图片格式 %s（支持 png/jpg/jpeg/gif/webp）", ext)), nil
			}
			if len(data) > visionMaxImageMB*1024*1024 {
				return actool.Errorf(fmt.Sprintf("图片超过 %dMB 限制", visionMaxImageMB)), nil
			}

			// 裸调 Anthropic Messages API（不走 norma，绕过 SDK 限制）
			desc, err := callVisionAPI(ctx, base64.StdEncoding.EncodeToString(data), mime, a.Question)
			if err != nil {
				return actool.Errorf(fmt.Sprintf("vision 调用失败: %v", err)), nil
			}
			return actool.Text(desc), nil
		})
}

// callVisionAPI sends an image + question to the Anthropic Messages API.
func callVisionAPI(ctx context.Context, imageB64, mimeType, question string) (string, error) {
	vc := VisionDefaults
	baseURL := vc.BaseURL
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	// 兼容：如果用户填了完整 endpoint（含 /v1/messages），直接用
	url := baseURL
	if !strings.Contains(url, "/messages") {
		if strings.Contains(url, "/v1") {
			url += "messages"
		} else {
			url += "v1/messages"
		}
	}

	payload := map[string]any{
		"model":      vc.Model,
		"max_tokens": 1024,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{
					"type": "image",
					"source": map[string]any{
						"type":       "base64",
						"media_type": mimeType,
						"data":       imageB64,
					},
				},
				{
					"type": "text",
					"text": question,
				},
			},
		}},
	}
	body, _ := json.Marshal(payload)

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", vc.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("解析响应失败: %v", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("API 错误 %s: %s", result.Error.Type, result.Error.Message)
	}
	var sb strings.Builder
	for _, c := range result.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("模型未返回文本（可能不支持图片输入）")
	}
	return sb.String(), nil
}

// visionTools returns the vision tool set.
func (t *ToolSet) visionTools() []actool.CoreTool {
	return []actool.CoreTool{t.viewImage()}
}
