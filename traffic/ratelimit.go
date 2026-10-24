package traffic

// 出站速率治理：在录制代理的 Request 钩子上按「全局 → 单 host」两级令牌桶排队。
//
// 放在代理层而不是逐个工具里，是因为 Bash/curl、run_execution、WebFetch、browser MCP
// 最终都从同一个出口出去；逐个工具加限速既覆盖不全，也会被脚本/变量拼接绕过。
// 只有经过录制代理的流量会被治理（capture 关闭且未配置全局代理时，目标流量直连，
// 不经过这里）——这是部署边界，不是静默降级：设置页会写明。
//
// 排队而不是报错：超过 MaxWait 后放行（fail-open）并计入 waived。限速的目的是避免
// 把目标打挂/触发平台风控，不是让 agent 的任务无故失败。

import (
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// EgressLimits is the runtime rate-governance config. Zero QPS = that level is off.
type EgressLimits struct {
	GlobalQPS float64       // 所有 host 合计
	HostQPS   float64       // 单个 host
	MaxWait   time.Duration // 单个请求最长排队时间（0 = 默认 10s）
}

const (
	defaultEgressMaxWait = 10 * time.Second
	maxEgressHostBuckets = 8192
)

// tokenBucket is a small lock-protected token bucket. Capacity is one second of
// traffic (rate), i.e. a short burst is allowed before steady-state throttling.
type tokenBucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newTokenBucket(rate float64) *tokenBucket {
	return &tokenBucket{rate: rate, burst: math.Max(1, rate), tokens: math.Max(1, rate)}
}

// take blocks until a token is available or the deadline passes. It returns
// false on timeout and leaves the bucket untouched (the caller then fails open).
func (b *tokenBucket) take(deadline time.Time) bool {
	for {
		b.mu.Lock()
		now := time.Now()
		if b.last.IsZero() {
			b.last = now
		}
		b.tokens = math.Min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
		b.last = now
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return true
		}
		need := time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
		b.mu.Unlock()
		if need < time.Millisecond {
			need = time.Millisecond
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		if need > remaining {
			need = remaining
		}
		time.Sleep(need)
	}
}

// egressGovernor owns the global bucket and the lazy per-host buckets.
type egressGovernor struct {
	limits EgressLimits
	global *tokenBucket
	mu     sync.Mutex
	hosts  map[string]*tokenBucket
	waived atomic.Int64
}

func newEgressGovernor(l EgressLimits) *egressGovernor {
	if l.MaxWait <= 0 {
		l.MaxWait = defaultEgressMaxWait
	}
	g := &egressGovernor{limits: l, hosts: map[string]*tokenBucket{}}
	if l.GlobalQPS > 0 {
		g.global = newTokenBucket(l.GlobalQPS)
	}
	return g
}

// hostBucket returns the bucket for host, creating it lazily. The map is capped so
// a scanner spraying random hostnames cannot grow it without bound; hitting the cap
// resets the per-host state (the global bucket still holds the overall rate).
func (g *egressGovernor) hostBucket(host string) *tokenBucket {
	g.mu.Lock()
	defer g.mu.Unlock()
	if b := g.hosts[host]; b != nil {
		return b
	}
	if len(g.hosts) >= maxEgressHostBuckets {
		g.hosts = make(map[string]*tokenBucket, maxEgressHostBuckets)
	}
	b := newTokenBucket(g.limits.HostQPS)
	g.hosts[host] = b
	return b
}

func (g *egressGovernor) wait(host string) {
	deadline := time.Now().Add(g.limits.MaxWait)
	if g.limits.HostQPS > 0 && host != "" {
		if !g.hostBucket(host).take(deadline) {
			g.waived.Add(1)
		}
	}
	if g.global != nil {
		if !g.global.take(deadline) {
			g.waived.Add(1)
		}
	}
}

// SetEgressLimits installs (or clears, when both QPS are <= 0) the runtime
// governor. Existing buckets are discarded: the new rates start from a fresh
// one-second burst, which is the least surprising behavior after a rate change.
func (t *Traffic) SetEgressLimits(l EgressLimits) {
	if t == nil {
		return
	}
	if l.GlobalQPS < 0 {
		l.GlobalQPS = 0
	}
	if l.HostQPS < 0 {
		l.HostQPS = 0
	}
	if l.GlobalQPS <= 0 && l.HostQPS <= 0 {
		t.rate.Store(nil)
		return
	}
	t.rate.Store(newEgressGovernor(l))
}

// SetTaskQPS installs (or clears, qps<=0) a per-task token bucket. The task rate
// is applied in addition to the global/host limits, so a noisy task cannot
// consume a shared host's budget faster than the operator allows.
func (t *Traffic) SetTaskQPS(taskID int64, qps float64) {
	if t == nil || taskID <= 0 {
		return
	}
	if qps <= 0 {
		t.taskBuckets.Delete(taskID)
		return
	}
	t.taskBuckets.Store(taskID, newTokenBucket(qps))
}

// ClearTaskQPS drops a task's bucket on terminal/delete.
func (t *Traffic) ClearTaskQPS(taskID int64) {
	if t == nil || taskID <= 0 {
		return
	}
	t.taskBuckets.Delete(taskID)
}

// waitEgress blocks per the current limits. Nil governor + no task bucket = off,
// so the proxy is unchanged unless the operator configured a rate.
func (t *Traffic) waitEgress(host string, taskID int64) {
	if t == nil {
		return
	}
	host = strings.ToLower(strings.TrimSpace(host))
	g := t.rate.Load()
	if g != nil {
		g.wait(host)
	}
	if taskID > 0 {
		if v, ok := t.taskBuckets.Load(taskID); ok {
			deadline := time.Now().Add(defaultEgressMaxWait)
			if g != nil && g.limits.MaxWait > 0 {
				deadline = time.Now().Add(g.limits.MaxWait)
			}
			if !v.(*tokenBucket).take(deadline) && g != nil {
				g.waived.Add(1)
			}
		}
	}
}
