package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lin9527ya/bladepro/agent"
)

// TestCaptureWithCDPSteps runs real Chromium interactions: fill an input, click
// a button that rewrites the page, then capture. Skipped when no headless browser
// is installed (CI images often lack one).
func TestCaptureWithCDPSteps(t *testing.T) {
	browser := screenshotBrowser()
	if browser == "" {
		t.Skip("no headless chromium/chrome available")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><body>
<input id="q" value="">
<button id="go" onclick="document.getElementById('out').textContent='HELLO '+document.getElementById('q').value">go</button>
<div id="out">EMPTY</div>
</body></html>`))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "shot.png")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	steps := []agent.SrcScreenshotStep{
		{Action: "fill", Selector: "#q", Value: "world"},
		{Action: "click", Selector: "#go"},
		{Action: "wait", WaitMS: 300},
	}
	if err := captureWithCDP(ctx, browser, srv.URL, out, 800, 600, true, steps, ""); err != nil {
		t.Fatalf("capture: %v", err)
	}
	info, err := os.Stat(out)
	if err != nil || info.Size() < 100 {
		t.Fatalf("screenshot missing/too small: %v size=%d", err, info.Size())
	}
	f, _ := os.Open(out)
	defer f.Close()
	var magic [8]byte
	_, _ = f.Read(magic[:])
	if string(magic[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("not a PNG: %q", magic[:])
	}
}
