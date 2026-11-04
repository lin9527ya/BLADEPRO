package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lin9527ya/bladepro/db"
)

type divergeProfile struct {
	id   int64
	name string
}

// P-Q 多模型发散（移植自 quiver divergence_brainstorm）：
// 把同一个问题并行发给最多 4 个 LLM profile，合并并标注提出模型。
// 工具在 agent/diverge.go；这里提供宿主实现（复用现有 profile/provider 基建）。
func (s *Server) divergeDefault() func(question string, profiles []string) (string, error) {
	return func(question string, names []string) (string, error) {
		question = strings.TrimSpace(question)
		if question == "" {
			return "", fmt.Errorf("question 不能为空")
		}
		all, err := s.m.pg.ListProfiles()
		if err != nil {
			return "", err
		}
		byName := map[string]divergeProfile{}
		var picks []divergeProfile
		for _, p := range all {
			ref := divergeProfile{id: p.ID, name: p.Name}
			byName[strings.ToLower(strings.TrimSpace(p.Name))] = ref
		}
		if len(names) == 0 {
			// 默认：全局激活 profile 优先，再补前两个，最多 3 个。
			if act, err := s.m.pg.ActiveProfile(); err == nil && act != nil {
				picks = append(picks, divergeProfile{id: act.ID, name: act.Name})
			}
			for _, p := range all {
				if len(picks) >= 3 {
					break
				}
				dup := false
				for _, x := range picks {
					if x.id == p.ID {
						dup = true
					}
				}
				if !dup {
					picks = append(picks, divergeProfile{id: p.ID, name: p.Name})
				}
			}
		} else {
			for _, n := range names {
				if len(picks) >= 4 {
					break
				}
				if it, ok := byName[strings.ToLower(strings.TrimSpace(n))]; ok {
					picks = append(picks, it)
				}
			}
		}
		if len(picks) == 0 {
			return "", fmt.Errorf("没有可用的 LLM profile（先在系统 LLM 页配置）")
		}
		type res struct {
			name string
			text string
			err  error
		}
		out := make([]res, len(picks))
		var wg sync.WaitGroup
		for i, it := range picks {
			wg.Add(1)
			go func(i int, ref divergeProfile) {
				defer wg.Done()
				prov, _, ok := s.providerForProfile(ref.id)
				if !ok {
					out[i] = res{name: ref.name, err: fmt.Errorf("profile %s 不可用", ref.name)}
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				text, err := streamCollectText(ctx, prov,
					"你是 SRC 赏金测试的规划顾问。针对下面的问题给出进攻视角的建议：可行的测试方向、可验证的假设、应避免的弯路。只输出要点，不要客套。",
					question)
				out[i] = res{name: ref.name, text: strings.TrimSpace(text), err: err}
			}(i, it)
		}
		wg.Wait()
		var b strings.Builder
		b.WriteString("## 多模型发散结果\n")
		okCount := 0
		for _, r := range out {
			if r.err != nil || r.text == "" {
				continue
			}
			okCount++
			fmt.Fprintf(&b, "\n### %s\n%s\n", r.name, r.text)
		}
		if okCount == 0 {
			return "", fmt.Errorf("所有 profile 都失败（检查 LLM 配置）")
		}
		b.WriteString("\n（以上为多模型并行输出，未去重；采信前逐条判断。）")
		return b.String(), nil
	}
}

// 编译期使用 db 包（profile 类型来自 db）。
var _ = db.LLMProfile{}
