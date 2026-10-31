package server

import (
	"context"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lin9527ya/bladepro/db"
)

// LogLine is one captured backend log entry exposed by the /api/logs endpoints.
type LogLine struct {
	Seq   int64  `json:"seq"`
	DBID  int64  `json:"db_id,omitempty"` // server_logs.id; 0 for pre-persistence entries
	TS    string `json:"ts"`
	Level string `json:"level"` // info | warn | error
	Tag   string `json:"tag"`   // the leading [tag] (pg / planner / activity / …), if any
	Text  string `json:"text"`
}

// dbWriteReq carries a log line to the async DB writer.
type dbWriteReq struct {
	seq int64
	ll  LogLine
}

// logSinkT tees the standard logger into an in-memory ring buffer and fans new
// lines out to SSE subscribers, so the UI can show a live backend log stream.
// It still passes everything through to stderr (the terminal keeps working).
type logSinkT struct {
	mu   sync.Mutex
	ring []LogLine
	cap  int
	seq  int64
	subs map[chan LogLine]struct{}
	out  io.Writer // passthrough (stderr)

	dbDone        chan struct{}
	dbCancel      context.CancelFunc
	dbDropped     atomic.Int64
	dbWriteFailed atomic.Int64
	dbCh          chan dbWriteReq // buffered async channel; nil until SetDB is called
}

var logSink = &logSinkT{cap: 3000, subs: map[chan LogLine]struct{}{}, out: os.Stderr}

// StartLogCapture redirects the standard log package through the in-memory sink
// (still writing to stderr). Call once at startup, as early as possible.
//
// 注意这里**不要**顺手 slog.SetDefault：那会把 log 包的输出整个劫持进 slog
// （见 slogbridge.go 里 appLogger 的说明）。slog 侧由 appLogger 独立接入本 sink。
func StartLogCapture() { log.SetOutput(logSink) }

// emit 收下一条**级别与 tag 已确定**的记录（slog 桥用）。与 Write 的区别：不再走
// parseLog 的关键词猜测——slog 的调用点本来就写明了级别与 tag。
func (s *logSinkT) emit(level, tag, text string) {
	_, _ = s.out.Write([]byte(time.Now().Format("2006/01/02 15:04:05") + " " + text + "\n"))
	s.add(LogLine{TS: time.Now().Format(time.RFC3339), Level: level, Tag: tag, Text: text})
}

// SetDB wires a postgres DB into the sink once (idempotent). It:
//  1. Restores the last 100 log rows from DB into the ring so the /logs page
//     shows history immediately after restart.
//  2. Starts an async goroutine that persists every subsequent log line to DB.
func (s *logSinkT) SetDB(ctx context.Context, pg *db.DB) {
	if pg == nil || ctx.Err() != nil {
		return
	}
	ch := make(chan dbWriteReq, 2000)
	done := make(chan struct{})
	writeCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	oldCh, oldDone, oldCancel := s.dbCh, s.dbDone, s.dbCancel
	if oldCh != nil {
		close(oldCh)
	}
	s.dbCh = ch
	s.dbDone = done
	s.dbCancel = cancel
	s.mu.Unlock()
	goSafe("logsink/writer", nil, func() {
		defer close(done)
		defer cancel()
		for req := range ch {
			if writeCtx.Err() != nil {
				s.dbDropped.Add(int64(len(ch)) + 1)
				return
			}
			qctx, qcancel := context.WithTimeout(writeCtx, 2*time.Second)
			id, err := pg.InsertLogContext(qctx, req.ll.Level, req.ll.Tag, req.ll.Text)
			qcancel()
			if err != nil {
				s.dbWriteFailed.Add(1)
				_, _ = os.Stderr.Write([]byte("[logsink] db write failed\n"))
				continue
			}
			s.mu.Lock()
			for i := range s.ring {
				if s.ring[i].Seq == req.seq {
					s.ring[i].DBID = id
					break
				}
			}
			s.mu.Unlock()
		}
	})
	if oldDone != nil {
		stopCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		select {
		case <-oldDone:
		case <-stopCtx.Done():
			if oldCancel != nil {
				oldCancel()
			}
		}
		stop()
	}
	restoreCtx, restoreCancel := context.WithTimeout(writeCtx, 2*time.Second)
	defer restoreCancel()
	if logs, err := pg.RecentLogsContext(restoreCtx, 100); err == nil {
		s.mu.Lock()
		for _, l := range logs {
			s.seq++
			s.ring = append(s.ring, LogLine{Seq: s.seq, DBID: l.ID, TS: l.CreatedAt.Format(time.RFC3339), Level: l.Level, Tag: l.Tag, Text: l.Text})
		}
		if len(s.ring) > s.cap {
			s.ring = s.ring[len(s.ring)-s.cap:]
		}
		s.mu.Unlock()
	}

}

// CloseDB detaches producers, drains queued rows and cancels writes at the shared
// shutdown deadline. The caller must invoke it before closing the database pool.
func (s *logSinkT) CloseDB(ctx context.Context) bool {
	s.mu.Lock()
	ch, done, cancel := s.dbCh, s.dbDone, s.dbCancel
	s.dbCh = nil
	if ch != nil {
		close(ch)
	}
	s.mu.Unlock()
	if done == nil {
		return true
	}
	select {
	case <-done:
		if cancel != nil {
			cancel()
		}
		return true
	case <-ctx.Done():
		if cancel != nil {
			cancel()
		}
		return false
	}
}

func (s *logSinkT) DBLoss() (dropped, failed int64) {
	return s.dbDropped.Load(), s.dbWriteFailed.Load()
}

// Write implements io.Writer for the log package: one call per log.Printf line.
func (s *logSinkT) Write(p []byte) (int, error) {
	_, _ = s.out.Write(p) // keep the terminal output
	line := strings.TrimRight(string(p), "\n")
	if strings.TrimSpace(line) != "" {
		s.add(parseLog(line))
	}
	return len(p), nil
}

func (s *logSinkT) add(l LogLine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	l.Seq = s.seq
	s.ring = append(s.ring, l)
	if len(s.ring) > s.cap {
		s.ring = s.ring[len(s.ring)-s.cap:]
	}
	if s.dbCh != nil {
		select {
		case s.dbCh <- dbWriteReq{seq: l.Seq, ll: l}:
		default:
			s.dbDropped.Add(1)
		}
	}
	// Sending and unsubscribe/close share the lock, so no stale subscriber can be
	// closed between taking a snapshot and delivering a live frame.
	for sub := range s.subs {
		select {
		case sub <- l:
		default:
		}
	}
}

// recent returns ring lines with Seq > since (capped to limit), and the latest seq.
func (s *logSinkT) recent(since int64, limit int) (lines []LogLine, cursor int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cursor = s.seq
	if limit <= 0 || limit > s.cap {
		limit = s.cap
	}
	for _, l := range s.ring {
		if l.Seq > since {
			lines = append(lines, l)
		}
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines, cursor
}

func (s *logSinkT) subscribe() (<-chan LogLine, func()) {
	ch := make(chan LogLine, 256)
	s.mu.Lock()
	if s.subs == nil {
		s.subs = map[chan LogLine]struct{}{}
	}
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.subs, ch)
			s.mu.Unlock()
			close(ch)
		})
	}
}

// parseLog pulls a level + [tag] out of a standard-logger line; the timestamp is
// the capture time (RFC3339).
func parseLog(line string) LogLine {
	msg := line
	// strip the "2006/01/02 15:04:05" LstdFlags prefix if present
	if len(msg) >= 20 && msg[4] == '/' && msg[7] == '/' && msg[10] == ' ' {
		msg = strings.TrimSpace(msg[19:])
	}
	tag := ""
	if strings.HasPrefix(msg, "[") {
		if i := strings.IndexByte(msg, ']'); i > 1 {
			tag = msg[1:i]
		}
	}
	return LogLine{TS: time.Now().Format(time.RFC3339), Level: levelOf(msg), Tag: tag, Text: msg}
}

func levelOf(msg string) string {
	low := strings.ToLower(msg)
	for _, k := range []string{"fatal", "panic", "error", "err:", "失败", "丢弃", "拒绝", "✕", "不可达"} {
		if strings.Contains(low, k) {
			return "error"
		}
	}
	for _, k := range []string{"warn", "disabled", "禁用", "skip", "stopped", "⚠", "重试"} {
		if strings.Contains(low, k) {
			return "warn"
		}
	}
	return "info"
}
