package traffic

import (
	"encoding/base64"
	"testing"
)

// TestProxyTaskID pins the CONNECT credential contract: worker Bash/WebFetch
// proxy URLs carry bp-task-<id> as the Basic-auth username, and the recorder
// turns that into exchanges.task_id. Unknown credentials must degrade to 0
// (unattributed) instead of dropping the capture.
func TestProxyTaskID(t *testing.T) {
	basic := func(user string) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":x"))
	}
	cases := []struct {
		name   string
		header string
		want   int64
	}{
		{"task identity", basic("bp-task-42"), 42},
		{"case-insensitive scheme", "basic " + base64.StdEncoding.EncodeToString([]byte("bp-task-7:x")), 7},
		{"no userinfo", basic("someone"), 0},
		{"zero task", basic("bp-task-0"), 0},
		{"bad number", basic("bp-task-abc"), 0},
		{"not basic", "Bearer abc", 0},
		{"malformed base64", "Basic !!!!", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := proxyTaskID(tc.header); got != tc.want {
				t.Fatalf("proxyTaskID(%q)=%d want %d", tc.header, got, tc.want)
			}
		})
	}
}
