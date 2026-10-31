package server

import (
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/srcmode"
	"github.com/lin9527ya/bladepro/srcprobe"
)

func key(r agent.SrcProbeRequest, doc srcmode.ModeDoc) string { return srcProbeCacheKey(r, doc) }

func TestSrcProbeCacheKey(t *testing.T) {
	doc := srcmode.ModeDoc{}
	if got := key(agent.SrcProbeRequest{Kind: srcprobe.KindPaths}, doc); !strings.HasSuffix(got, "paths:max=200") {
		t.Fatalf("paths default key=%q", got)
	}
	if got := key(agent.SrcProbeRequest{Kind: srcprobe.KindPaths, MaxPaths: 50}, doc); !strings.HasSuffix(got, "paths:max=50") {
		t.Fatalf("paths max key=%q", got)
	}
	if got := key(agent.SrcProbeRequest{Kind: srcprobe.KindJS}, doc); !strings.HasSuffix(got, ":js") {
		t.Fatalf("js key=%q", got)
	}
	a := key(agent.SrcProbeRequest{Kind: srcprobe.KindMethods, Endpoints: []string{"/b", "/a"}}, doc)
	b := key(agent.SrcProbeRequest{Kind: srcprobe.KindMethods, Endpoints: []string{"/a", "/b", "/a"}}, doc)
	if a == "" || a != b {
		t.Fatalf("methods key should be order/duplicate insensitive: %q vs %q", a, b)
	}
	c := key(agent.SrcProbeRequest{Kind: srcprobe.KindMethods, Endpoints: []string{"/a"}}, doc)
	if a == c {
		t.Fatal("methods key must change with endpoint set")
	}
	if got := key(agent.SrcProbeRequest{Kind: srcprobe.KindNon404}, doc); got != "" {
		t.Fatalf("non404 must not be cached, got %q", got)
	}
	// Rule changes must not reuse cached enumeration results.
	changed := srcmode.ModeDoc{RateLimit: 1, CheckTier: "platform", CheckThresholds: map[string]int{"paths": 10}}
	if a == key(agent.SrcProbeRequest{Kind: srcprobe.KindMethods, Endpoints: []string{"/b", "/a"}}, changed) {
		t.Fatal("rule changes must invalidate the probe cache key")
	}
	identityA := key(agent.SrcProbeRequest{Kind: srcprobe.KindMethods, Endpoints: []string{"/a"}, Identity: "A"}, doc)
	identityB := key(agent.SrcProbeRequest{Kind: srcprobe.KindMethods, Endpoints: []string{"/a"}, Identity: "B"}, doc)
	if identityA == identityB {
		t.Fatal("different identities must not share probe cache entries")
	}
}
