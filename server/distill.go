package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// P-Q LLM 蒸馏（知识喂入 / 任务复盘）。
//
// 有全局激活的 LLM profile 就交给模型蒸馏；没有 profile、调用失败、或返回不合法时，
// 调用方回退到结构化输出（原文/统计）。产物一律走审批，模型不直接写知识库。

var errNoLLM = errors.New("未配置激活的 LLM profile")

// distillTimeout 是单次蒸馏的墙钟上限：太短大文档会截断，太长会卡住 HTTP 请求。
const distillTimeout = 120 * time.Second

// distillText 用激活 profile 跑一次非流式收集的补全。
func (s *Server) distillText(ctx context.Context, system, user string) (string, error) {
	if s == nil || s.m == nil || s.m.pg == nil {
		return "", errNoLLM
	}
	p, err := s.m.pg.ActiveProfile()
	if err != nil || p == nil {
		return "", errNoLLM
	}
	prov, _, ok := s.providerForProfile(p.ID)
	if !ok {
		return "", fmt.Errorf("激活 profile %q 不可用", p.Name)
	}
	cctx, cancel := context.WithTimeout(ctx, distillTimeout)
	defer cancel()
	out, err := streamCollectText(cctx, prov, system, user)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// distillJSON 要求模型输出一个 JSON 对象；解析失败返回错误（调用方回退）。
func (s *Server) distillJSON(ctx context.Context, system, user string) (map[string]any, error) {
	text, err := s.distillText(ctx, system, user)
	if err != nil {
		return nil, err
	}
	return extractJSONObject(text)
}

// extractJSONObject 从模型输出里取出第一个 { 到最后一个 } 之间的 JSON（容忍 ``` 围栏）。
func extractJSONObject(text string) (map[string]any, error) {
	i := strings.Index(text, "{")
	j := strings.LastIndex(text, "}")
	if i < 0 || j <= i {
		return nil, fmt.Errorf("响应里没有 JSON 对象：%s", distillClip(text, 200))
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text[i:j+1]), &out); err != nil {
		return nil, fmt.Errorf("JSON 解析失败：%w", err)
	}
	return out, nil
}

// clipRunes 按字符截断（避免把中文切坏）。
func distillClip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}
