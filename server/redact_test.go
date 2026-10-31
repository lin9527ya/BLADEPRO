package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactPocArtifacts(t *testing.T) {
	headers := redactHeaderLines([]string{
		"Host: api.example.test",
		"Authorization: Bearer secret-token",
		"Cookie: session=abc123",
		"X-API-Key: key123",
		"Content-Type: application/json",
	})
	joined := strings.Join(headers, "\n")
	if strings.Contains(joined, "secret-token") || strings.Contains(joined, "abc123") || strings.Contains(joined, "key123") {
		t.Fatalf("credentials leaked: %s", joined)
	}
	for _, keep := range []string{"Host: api.example.test", "Content-Type: application/json"} {
		if !strings.Contains(joined, keep) {
			t.Fatalf("non-sensitive header lost: %s", keep)
		}
	}
	if got := redactURL("https://api.example.test/x?token=secret&id=7"); strings.Contains(got, "secret") || !strings.Contains(got, "id=7") {
		t.Fatalf("URL redaction bad: %s", got)
	}
	body := redactPocBody([]string{"Content-Type: application/json"}, []byte(`{"user":"a","password":"p@ss","token":"abc"}`))
	if strings.Contains(string(body), "p@ss") || strings.Contains(string(body), "abc") {
		t.Fatalf("body credential leaked: %s", body)
	}
	if !strings.Contains(string(body), `"user":"a"`) {
		t.Fatalf("non-sensitive body field lost: %s", body)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("redacted JSON is no longer valid: %v (%s)", err, body)
	}
	if got := redactPocBody([]string{"Content-Type: application/octet-stream"}, []byte("raw-secret")); string(got) != "raw-secret" {
		t.Fatalf("binary body should be untouched: %s", got)
	}
}
