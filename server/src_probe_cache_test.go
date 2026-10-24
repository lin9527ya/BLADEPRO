package server

import (
	"testing"

	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/srcprobe"
)

func TestSrcProbeCacheKey(t *testing.T) {
	if got := srcProbeCacheKey(agent.SrcProbeRequest{Kind: srcprobe.KindPaths}); got != "paths:max=200" {
		t.Fatalf("paths default key=%q", got)
	}
	if got := srcProbeCacheKey(agent.SrcProbeRequest{Kind: srcprobe.KindPaths, MaxPaths: 50}); got != "paths:max=50" {
		t.Fatalf("paths max key=%q", got)
	}
	if got := srcProbeCacheKey(agent.SrcProbeRequest{Kind: srcprobe.KindJS}); got != "js" {
		t.Fatalf("js key=%q", got)
	}
	a := srcProbeCacheKey(agent.SrcProbeRequest{Kind: srcprobe.KindMethods, Endpoints: []string{"/b", "/a"}})
	b := srcProbeCacheKey(agent.SrcProbeRequest{Kind: srcprobe.KindMethods, Endpoints: []string{"/a", "/b", "/a"}})
	if a == "" || a != b {
		t.Fatalf("methods key should be order/duplicate insensitive: %q vs %q", a, b)
	}
	c := srcProbeCacheKey(agent.SrcProbeRequest{Kind: srcprobe.KindMethods, Endpoints: []string{"/a"}})
	if a == c {
		t.Fatal("methods key must change with endpoint set")
	}
	// non404 depends on the task's traffic; never cached.
	if got := srcProbeCacheKey(agent.SrcProbeRequest{Kind: srcprobe.KindNon404}); got != "" {
		t.Fatalf("non404 must not be cached, got %q", got)
	}
}
