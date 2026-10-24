package server

// 执行层出口白名单：录制代理在转发任务流量前按 task_scope 再校验一次目标 host。
//
// 为什么需要这一层：命令门是正则，编码/变量拼接/落盘脚本/子进程间接调用都能绕过它；
// 但所有走 HTTP(S) 的出站最终都经过录制代理。按连接上的 task 身份做 host 白名单，
// 才是不依赖命令文本的兜底。
//
// 边界：
//   - 只拦能归因到任务的连接（worker/planner/main 的 Bash、WebFetch、探针）；browser
//     MCP 是全局连接，没有 task 身份，不在这层覆盖内。
//   - task_scope 只有 company/icp/keyword 时无法从 host 反推，判定为“判不了”并放行，
//     与 RoE 范围门同一口径；DB 查询失败也放行（不能因一次抖动把整个任务断网）。
//   - 本地目标（loopback）放行，避免拦住平台自身/本地靶场。

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	proxyScopeTTL      = 30 * time.Second
	proxyScopeMaxItems = 4096
)

type proxyScopeDecision struct {
	allowed bool
	reason  string
	at      time.Time
}

type proxyScopeCache struct {
	mu      sync.Mutex
	entries map[string]proxyScopeDecision
}

func (s *Server) installTrafficScopeGuard() {
	if s == nil || s.m == nil {
		return
	}
	if tr := s.m.Traffic(); tr != nil {
		tr.SetScopeGuard(s.checkProxyTarget)
	}
}

func (s *Server) checkProxyTarget(taskID int64, host string) error {
	if s == nil || s.m == nil || s.m.pg == nil || taskID <= 0 {
		return nil
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || isLocalProxyHost(host) {
		return nil
	}
	key := fmt.Sprintf("%d|%s", taskID, host)
	now := time.Now()
	s.proxyScope.mu.Lock()
	if d, ok := s.proxyScope.entries[key]; ok && now.Sub(d.at) < proxyScopeTTL {
		s.proxyScope.mu.Unlock()
		if d.allowed {
			return nil
		}
		return fmt.Errorf("%s", d.reason)
	}
	s.proxyScope.mu.Unlock()

	allowed, reason := s.resolveProxyTarget(taskID, host)
	s.proxyScope.mu.Lock()
	if s.proxyScope.entries == nil || len(s.proxyScope.entries) >= proxyScopeMaxItems {
		s.proxyScope.entries = map[string]proxyScopeDecision{}
	}
	s.proxyScope.entries[key] = proxyScopeDecision{allowed: allowed, reason: reason, at: now}
	s.proxyScope.mu.Unlock()
	if allowed {
		return nil
	}
	return fmt.Errorf("%s", reason)
}

func (s *Server) resolveProxyTarget(taskID int64, host string) (bool, string) {
	// 时间窗是授权边界的时间维度：窗口外不发动作。查不到任务快照时不额外拦。
	if doc := s.srcDocForTask(taskID); doc.TimeWindow != "" && !doc.InWindow(time.Now()) {
		return false, fmt.Sprintf("当前不在授权作业时间窗（%s）内", doc.TimeWindow)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	inScope, decidable, err := s.m.PG().Src().SrcHostInScope(ctx, taskID, host)
	if err != nil || !decidable {
		// 判不了/查不动 -> 放行：与 RoE 范围门的 fail-open 语义一致，命令门仍有一层。
		return true, ""
	}
	if inScope {
		return true, ""
	}
	return false, fmt.Sprintf("目标 %s 不在任务 #%d 的授权范围内", host, taskID)
}

func isLocalProxyHost(host string) bool {
	h := strings.Trim(strings.ToLower(host), "[]")
	if h == "localhost" || h == "::1" || h == "0.0.0.0" || strings.HasPrefix(h, "127.") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}
