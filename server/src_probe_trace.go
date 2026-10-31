package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"sync"
)

type probeRequestTrace struct {
	ID       string `json:"execution_request_id"`
	Method   string `json:"method"`
	URL      string `json:"url"`
	Status   int    `json:"status"`
	Bytes    int64  `json:"body_bytes"`
	SHA256   string `json:"body_sha256"`
	Complete bool   `json:"body_complete"`
	Error    bool   `json:"error"`
}
type probeTraceTransport struct {
	base  http.RoundTripper
	runID string
	mu    sync.Mutex
	rows  []probeRequestTrace
}

func (t *probeTraceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	index := len(t.rows)
	t.rows = append(t.rows, probeRequestTrace{ID: fmt.Sprintf("%s:%d", t.runID, index+1), Method: r.Method, URL: r.URL.String()})
	t.mu.Unlock()
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		t.mu.Lock()
		t.rows[index].Error = true
		t.mu.Unlock()
		return nil, err
	}
	t.mu.Lock()
	t.rows[index].Status = resp.StatusCode
	t.mu.Unlock()
	resp.Body = &probeTraceBody{ReadCloser: resp.Body, h: sha256.New(), owner: t, index: index}
	return resp, nil
}

type probeTraceBody struct {
	io.ReadCloser
	h     hash.Hash
	owner *probeTraceTransport
	index int
	n     int64
	eof   bool
	once  sync.Once
}

func (b *probeTraceBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n += int64(n)
	_, _ = b.h.Write(p[:n])
	if err == io.EOF {
		b.eof = true
	}
	return n, err
}
func (b *probeTraceBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() {
		b.owner.mu.Lock()
		defer b.owner.mu.Unlock()
		r := &b.owner.rows[b.index]
		r.Bytes = b.n
		r.SHA256 = hex.EncodeToString(b.h.Sum(nil))
		r.Complete = b.eof && err == nil
	})
	return err
}
func (t *probeTraceTransport) snapshot() ([]probeRequestTrace, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows := append([]probeRequestTrace(nil), t.rows...)
	complete := len(rows) > 0
	for _, r := range rows {
		if r.Error || r.Status == 0 || !r.Complete {
			complete = false
		}
	}
	return rows, complete
}
