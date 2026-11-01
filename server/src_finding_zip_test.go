package server

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestBuildSrcFindingZipContainsReadmeAndReports(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	td := t.TempDir()
	s := New(context.Background(), m, td, td, td)
	task, err := m.CreateTaskWithOptions("single-zip", "单条交付包", db.TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	t.Cleanup(func() { _ = m.PG().DeleteTask(parseIntDefault(task.ID)) })
	fid, err := m.PG().AddFinding(parseIntDefault(task.ID), 0, "authz", "订单越权读取", "high",
		"我用自己的账号读取了其他租户的订单详情。", "", "test", nil)
	if err != nil {
		t.Fatalf("add finding: %v", err)
	}
	f, err := m.PG().GetFinding(fid)
	if err != nil || f == nil {
		t.Fatalf("get finding: %v", err)
	}
	blob, err := s.buildSrcFindingZip(context.Background(), f)
	if err != nil {
		t.Fatalf("build zip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	files := map[string]bool{}
	for _, zf := range zr.File {
		files[zf.Name] = true
	}
	if !files["提交说明.md"] {
		t.Fatal("missing 提交说明.md")
	}
	hasMD, hasDOCX := false, false
	for name := range files {
		if strings.HasPrefix(name, "findings/") && strings.HasSuffix(name, ".md") {
			hasMD = true
		}
		if strings.HasPrefix(name, "findings/") && strings.HasSuffix(name, ".docx") {
			hasDOCX = true
		}
	}
	if !hasMD || !hasDOCX {
		t.Fatalf("zip files=%v, want finding md+docx", files)
	}
	// README 必须说清楚这是单条交付包，而不是任务级全量包。
	for _, zf := range zr.File {
		if zf.Name != "提交说明.md" {
			continue
		}
		rc, _ := zf.Open()
		raw, _ := io.ReadAll(rc)
		rc.Close()
		if !strings.Contains(string(raw), "SRC 单条交付包") {
			t.Fatalf("readme=%s", raw)
		}
	}
}
