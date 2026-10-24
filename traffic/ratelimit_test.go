package traffic

import (
	"testing"
	"time"
)

func TestTokenBucketTakeTimes(t *testing.T) {
	b := &tokenBucket{rate: 20, burst: 1, tokens: 1, last: time.Now()}
	start := time.Now()
	if !b.take(time.Now().Add(time.Second)) {
		t.Fatal("first token should be available")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Millisecond {
		t.Fatalf("first take waited %v", elapsed)
	}
	start = time.Now()
	if !b.take(time.Now().Add(time.Second)) {
		t.Fatal("second token should arrive within deadline")
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Fatalf("second take elapsed=%v, want ~50ms", elapsed)
	}
}

func TestEgressGovernorFailsOpenAfterMaxWait(t *testing.T) {
	g := newEgressGovernor(EgressLimits{HostQPS: 1, MaxWait: 40 * time.Millisecond})
	g.wait("example.com") // burst=1 → instant
	start := time.Now()
	g.wait("example.com") // next token is 1s away → give up at the 40ms deadline
	elapsed := time.Since(start)
	if elapsed < 30*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Fatalf("wait elapsed=%v, want bounded by MaxWait", elapsed)
	}
	if g.waived.Load() == 0 {
		t.Fatal("timeout/fail-open should be counted")
	}
}

func TestEgressGovernorHostBucketsAreIndependent(t *testing.T) {
	g := newEgressGovernor(EgressLimits{HostQPS: 1, MaxWait: 20 * time.Millisecond})
	if g.hostBucket("a.example") == g.hostBucket("b.example") {
		t.Fatal("different hosts must get different buckets")
	}
	// 用掉 a 的突发额度后，b 的突发额度仍然可用（不受 a 影响）。
	if !g.hostBucket("a.example").take(time.Now().Add(10 * time.Millisecond)) {
		t.Fatal("a burst should be available")
	}
	if !g.hostBucket("b.example").take(time.Now().Add(10 * time.Millisecond)) {
		t.Fatal("b bucket should be independent of a")
	}
}

func TestSetEgressLimitsOffClearsGovernor(t *testing.T) {
	tr := &Traffic{}
	tr.SetEgressLimits(EgressLimits{GlobalQPS: 5})
	if tr.rate.Load() == nil {
		t.Fatal("positive limit should install a governor")
	}
	tr.SetEgressLimits(EgressLimits{})
	if tr.rate.Load() != nil {
		t.Fatal("zero limits should clear the governor")
	}
	// 关闭时 waitEgress 必须立即返回。
	start := time.Now()
	tr.waitEgress("example.com", 0)
	if elapsed := time.Since(start); elapsed > 10*time.Millisecond {
		t.Fatalf("disabled governor waited %v", elapsed)
	}
}

// TestTaskQPSBucketLifecycle pins per-task buckets: set creates one, clearing
// removes it, and a task bucket works even when no global governor is installed.
func TestTaskQPSBucketLifecycle(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.SetTaskQPS(42, 2)
	if _, ok := tr.taskBuckets.Load(int64(42)); !ok {
		t.Fatal("task bucket not installed")
	}
	// 无全局 governor 时也必须走任务桶；初始 burst=2，两次不阻塞。
	start := time.Now()
	tr.waitEgress("example.com", 42)
	tr.waitEgress("example.com", 42)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("initial burst should not block: %v", time.Since(start))
	}
	tr.ClearTaskQPS(42)
	if _, ok := tr.taskBuckets.Load(int64(42)); ok {
		t.Fatal("task bucket not cleared")
	}
}
