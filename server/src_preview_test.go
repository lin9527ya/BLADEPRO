package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestFindingSrcPreviewExposesFieldsAndIssues(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	td := t.TempDir()
	s := New(context.Background(), m, td, td, td)
	task, err := m.CreateTaskWithOptions("preview-api", "提交预览", db.TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	tid := parseIntDefault(task.ID)
	t.Cleanup(func() { _ = m.PG().DeleteTask(tid) })
	fid, err := m.PG().AddFinding(tid, 0, "authz", "订单越权读取", "low",
		"我用自己的账号读取了其他租户的订单详情。", "", "test", nil)
	if err != nil {
		t.Fatalf("add finding: %v", err)
	}
	tok, err := signJWT(s.auth.signKey.get())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/findings/"+itoa64(fid)+"/src-preview", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Ready     bool     `json:"ready"`
		Markdown  string   `json:"markdown"`
		Plain     string   `json:"plain_text"`
		Fields    []any    `json:"fields"`
		Warnings  []string `json:"warnings"`
		Issues    []string `json:"issues"`
		Steps     int      `json:"steps"`
		Endpoints int      `json:"endpoints"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Ready {
		t.Fatalf("preview must not be ready without platform fields/evidence: %+v", out)
	}
	if !strings.Contains(out.Markdown, "漏洞标题：") || !strings.Contains(out.Plain, "漏洞标题：") {
		t.Fatalf("preview missing 8-block body: %+v", out)
	}
	if len(out.Fields) == 0 {
		t.Fatalf("preview missing parsed platform fields: %+v", out)
	}
	joined := strings.Join(out.Issues, "；")
	if !strings.Contains(joined, "提交信息缺漏洞类型") || !strings.Contains(joined, "资产/URL") {
		t.Fatalf("preview missing platform issues: %s", joined)
	}
}
