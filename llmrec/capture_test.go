package llmrec

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/lin9527ya/Norma/llm"
)

func TestCaptureFromNilAndMissing(t *testing.T) {
	var nilCtx context.Context // the transport reaches here on any unrecorded call
	if CaptureFrom(nilCtx) != nil {
		t.Fatal("nil context should yield no capture")
	}
	if CaptureFrom(context.Background()) != nil {
		t.Fatal("bare context should yield no capture")
	}
}

// A nil *Capture is the "recording off" path: the transport calls these on every
// request, so they must all be no-ops rather than panics.
func TestNilCaptureIsInert(t *testing.T) {
	var c *Capture
	c.SetRequest("body")
	if got := c.RawRequest(); got != "" {
		t.Fatalf("RawRequest()=%q want empty", got)
	}
	if got := c.RawResponse(); got != "" {
		t.Fatalf("RawResponse()=%q want empty", got)
	}
	rc := io.NopCloser(strings.NewReader("x"))
	if c.TeeResponse(200, rc) != rc {
		t.Fatal("nil capture must pass the body through untouched")
	}
}

func TestCaptureSingleAttemptKeepsBytesVerbatim(t *testing.T) {
	ctx, c := NewCapture(context.Background())
	if CaptureFrom(ctx) != c {
		t.Fatal("capture not retrievable from its own context")
	}
	c.SetRequest(`{"model":"x"}`)
	// Retries re-send identical bytes; only the first is kept.
	c.SetRequest(`{"model":"ignored"}`)

	const sse = "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {}\n\n"
	body := c.TeeResponse(200, io.NopCloser(strings.NewReader(sse)))
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != sse {
		t.Fatal("tee altered the stream delivered to the caller")
	}
	if err := body.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if c.RawRequest() != `{"model":"x"}` {
		t.Fatalf("RawRequest()=%q", c.RawRequest())
	}
	// A lone attempt must stay byte-identical — no headers, no framing added, so
	// it can be replayed as-is.
	if c.RawResponse() != sse {
		t.Fatalf("RawResponse()=%q want verbatim SSE", c.RawResponse())
	}
}

// norma's doStream discards the bodies of retried attempts (retry.go closes them
// unread), so the capture is the only place a 429 body survives.
func TestCaptureRetriedAttemptsAreAllKept(t *testing.T) {
	_, c := NewCapture(context.Background())

	first := c.TeeResponse(429, io.NopCloser(strings.NewReader(`{"error":"rate_limit"}`)))
	if _, err := io.ReadAll(first); err != nil {
		t.Fatalf("read first: %v", err)
	}
	second := c.TeeResponse(200, io.NopCloser(strings.NewReader("data: ok\n")))
	if _, err := io.ReadAll(second); err != nil {
		t.Fatalf("read second: %v", err)
	}

	raw := c.RawResponse()
	if !strings.Contains(raw, `{"error":"rate_limit"}`) {
		t.Fatalf("dropped the retried attempt body: %q", raw)
	}
	if !strings.Contains(raw, "data: ok") {
		t.Fatalf("dropped the final attempt body: %q", raw)
	}
	if !strings.Contains(raw, "attempt 1/2 — HTTP 429") || !strings.Contains(raw, "attempt 2/2 — HTTP 200") {
		t.Fatalf("attempts not delimited: %q", raw)
	}
	if strings.Index(raw, "rate_limit") > strings.Index(raw, "data: ok") {
		t.Fatal("attempts stored out of order")
	}
}

func TestCaptureEmptyWhenNoHTTPHappened(t *testing.T) {
	_, c := NewCapture(context.Background())
	if c.RawRequest() != "" || c.RawResponse() != "" {
		t.Fatal("unused capture should be empty")
	}
}

// TestUsageCaptureRecoversDeepSeekCacheTokens pins the wire-field fallback:
// DeepSeek reports cache hits as prompt_cache_hit_tokens, which the provider's
// normalized Usage does not carry. The usage-only capture must recover it from
// the SSE tail without storing the response body.
func TestUsageCaptureRecoversDeepSeekCacheTokens(t *testing.T) {
	ctx, c := NewUsageCapture(context.Background())
	if CaptureFrom(ctx) != c {
		t.Fatal("usage capture not attached to context")
	}
	if c.RawResponse() != "" {
		t.Fatal("usage-only capture must not expose a body")
	}
	c.SetRequest("huge request should not be stored")
	if c.RawRequest() != "" {
		t.Fatal("usage-only capture must not store requests")
	}
	sse := "data: {\"usage\":{\"prompt_tokens\":100,\"prompt_cache_hit_tokens\":80,\"prompt_cache_miss_tokens\":20}}\n\ndata: [DONE]\n"
	body := c.TeeResponse(200, io.NopCloser(strings.NewReader(sse)))
	if _, err := io.ReadAll(body); err != nil {
		t.Fatal(err)
	}
	got := c.ApplyProviderUsage(llm.Usage{InputTokens: 100, OutputTokens: 7})
	if got.CacheReadTokens != 80 || got.InputTokens != 100 || got.OutputTokens != 7 {
		t.Fatalf("usage=%+v", got)
	}
	// Provider-parsed value wins.
	if got := c.ApplyProviderUsage(llm.Usage{InputTokens: 100, CacheReadTokens: 5}); got.CacheReadTokens != 5 {
		t.Fatalf("must not overwrite provider cache read: %+v", got)
	}
	// OpenAI-style cached_tokens fallback.
	_, c2 := NewUsageCapture(context.Background())
	body2 := c2.TeeResponse(200, io.NopCloser(strings.NewReader(`{"usage":{"prompt_tokens":50,"prompt_tokens_details":{"cached_tokens":30}}}`)))
	_, _ = io.ReadAll(body2)
	if got := c2.ApplyProviderUsage(llm.Usage{InputTokens: 50}); got.CacheReadTokens != 30 {
		t.Fatalf("cached_tokens fallback=%+v", got)
	}
}
