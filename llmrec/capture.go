package llmrec

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/lin9527ya/Norma/llm"
)

type captureContextKey struct{}

// Capture collects the untouched wire bodies of one logical LLM call. The
// Recorder creates it and puts it on the context; the HTTP transport that norma
// dials through (agent.quotaAwareTransport) finds it there and fills it in.
//
// This exists because everything the Recorder itself sees is already normalized:
// llm.CompletionRequest is re-serialized rather than the body buildBody() sent,
// and the response arrives as decoded StreamEvents, not the SSE frames. For
// debugging a live provider, the bytes on the wire are the only ground truth.
//
// norma's doStream retries the request-establishment phase, so one Stream can
// issue several HTTP attempts. Each attempt is kept: the discarded ones (see
// norma/llm/retry.go, which closes non-final bodies unread) are exactly what
// makes rate-limit and gateway failures diagnosable.
//
// The transport writes from norma's stream-reading goroutine while the Recorder
// snapshots at stream end, so all state is mutex-guarded.
type Capture struct {
	mu        sync.Mutex
	request   string
	attempts  []*attempt
	usageOnly bool
}

type attempt struct {
	status int
	body   strings.Builder
	// tail keeps only the last usageTailMax bytes for usage-only captures. SSE
	// usage arrives at the end, so a bounded tail is enough and never grows with
	// the response body.
	tail []byte
}

const usageTailMax = 128 << 10

// NewCapture returns a context carrying a fresh Capture, plus the Capture itself.
func NewCapture(ctx context.Context) (context.Context, *Capture) {
	c := &Capture{}
	return context.WithValue(ctx, captureContextKey{}, c), c
}

// NewUsageCapture returns a lightweight capture that keeps only the response
// tail needed to recover vendor usage fields (DeepSeek reports cache hits as
// prompt_cache_hit_tokens, which the provider's normalized Usage does not carry).
// It never stores the request/response bodies, so it is safe to enable on every
// call even when llm_record is off.
func NewUsageCapture(ctx context.Context) (context.Context, *Capture) {
	c := &Capture{usageOnly: true}
	return context.WithValue(ctx, captureContextKey{}, c), c
}

// CaptureFrom returns the Capture attached to ctx, or nil when raw capture is
// off. Callers must tolerate nil — recording is a toggle, and non-recorded
// providers dial through the same transport.
func CaptureFrom(ctx context.Context) *Capture {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(captureContextKey{}).(*Capture)
	return c
}

// SetRequest stores the outgoing request body. Retries re-send identical bytes,
// so only the first attempt's body is kept.
func (c *Capture) SetRequest(body string) {
	if c == nil || body == "" || c.usageOnly {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.request == "" {
		c.request = body
	}
}

// TeeResponse opens a new attempt and wraps rc so everything read from it is
// mirrored into that attempt. It tees rather than reads because a successful
// response is an SSE stream that must keep streaming to the caller.
func (c *Capture) TeeResponse(status int, rc io.ReadCloser) io.ReadCloser {
	if c == nil || rc == nil {
		return rc
	}
	a := &attempt{status: status}
	c.mu.Lock()
	c.attempts = append(c.attempts, a)
	c.mu.Unlock()
	return &teeBody{rc: rc, c: c, a: a}
}

// ApplyProviderUsage fills cache-read tokens from vendor-specific wire fields when
// the provider's normalized Usage left them at zero. Currently this recovers
// DeepSeek's prompt_cache_hit_tokens (and OpenAI-style cached_tokens as fallback);
// it never overwrites a non-zero value the provider already parsed.
func (c *Capture) ApplyProviderUsage(u llm.Usage) llm.Usage {
	if c == nil || u.CacheReadTokens > 0 {
		return u
	}
	if hit, ok := c.cacheHitTokens(); ok && hit > 0 {
		u.CacheReadTokens = hit
	}
	return u
}

var (
	deepSeekCacheHitRe = regexp.MustCompile(`"prompt_cache_hit_tokens"\s*:\s*(\d+)`)
	cachedTokensRe     = regexp.MustCompile(`"cached_tokens"\s*:\s*(\d+)`)
)

// cacheHitTokens scans the last successful response tail and returns the last
// occurrence of a vendor cache-hit counter.
func (c *Capture) cacheHitTokens() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.attempts) - 1; i >= 0; i-- {
		a := c.attempts[i]
		if a == nil || a.status != 200 {
			continue
		}
		text := string(a.tail)
		if text == "" && a.body.Len() > 0 {
			// Full capture path (llm_record on): usage is at the end of the body,
			// so scan only the tail instead of a potentially huge response.
			body := a.body.String()
			if len(body) > 2*usageTailMax {
				body = body[len(body)-usageTailMax:]
			}
			text = body
		}
		if text == "" {
			continue
		}
		if m := lastMatch(deepSeekCacheHitRe, text); m != "" {
			if n, err := strconv.Atoi(m); err == nil {
				return n, true
			}
		}
		if m := lastMatch(cachedTokensRe, text); m != "" {
			if n, err := strconv.Atoi(m); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func lastMatch(re *regexp.Regexp, s string) string {
	all := re.FindAllStringSubmatch(s, -1)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1][1]
}

// RawRequest returns the request body as sent, or "" if nothing was captured.
func (c *Capture) RawRequest() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.request
}

// RawResponse returns the response bytes as received. A single attempt yields
// the untouched original (copy-pasteable straight into a replay); multiple
// attempts are concatenated behind per-attempt header lines so a retry sequence
// stays readable.
func (c *Capture) RawResponse() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch len(c.attempts) {
	case 0:
		return ""
	case 1:
		return c.attempts[0].body.String()
	}
	var b strings.Builder
	for i, a := range c.attempts {
		fmt.Fprintf(&b, "===== attempt %d/%d — HTTP %d =====\n", i+1, len(c.attempts), a.status)
		body := a.body.String()
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Attempt is one HTTP round trip's status code and response bytes.
type Attempt struct {
	Status int
	Body   string
}

// Attempts returns every round trip in order. Unlike RawResponse — which drops
// the header line for a lone attempt so the bytes stay replayable — this always
// carries the status code, for callers that must report "HTTP 401 + body".
func (c *Capture) Attempts() []Attempt {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Attempt, 0, len(c.attempts))
	for _, a := range c.attempts {
		out = append(out, Attempt{Status: a.status, Body: a.body.String()})
	}
	return out
}

// teeBody mirrors reads into a Capture attempt, guarded by the Capture's mutex
// so a snapshot taken mid-stream never races the writer.
type teeBody struct {
	rc io.ReadCloser
	c  *Capture
	a  *attempt
}

func (t *teeBody) Read(p []byte) (int, error) {
	n, err := t.rc.Read(p)
	if n > 0 {
		t.c.mu.Lock()
		if t.c.usageOnly {
			t.a.tail = append(t.a.tail, p[:n]...)
			if len(t.a.tail) > 2*usageTailMax {
				// Keep the tail (usage fields are at the end), not the head. Trim at
				// 2x so a long SSE stream does not copy 128KB on every read.
				t.a.tail = append([]byte(nil), t.a.tail[len(t.a.tail)-usageTailMax:]...)
			}
		} else {
			t.a.body.Write(p[:n])
		}
		t.c.mu.Unlock()
	}
	return n, err
}

func (t *teeBody) Close() error { return t.rc.Close() }
