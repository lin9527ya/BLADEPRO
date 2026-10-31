package server

// 截图自动化端到端：headless 截图 → 证据产物 → fact 节点 → screenshots 收尾项校验。
// 没有 chromium/chrome 的环境跳过（CI 可选安装）。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

func TestRunSrcScreenshotCreatesVerifiedArtifact(t *testing.T) {
	if screenshotBrowser() == "" {
		t.Skip("no chromium/chrome available")
	}
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	td := t.TempDir()
	s := New(context.Background(), m, td, td, td)

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><h1>screenshot target</h1></body></html>"))
	}))
	defer page.Close()

	managed, err := m.CreateTaskWithOptions("screenshot", "截图", db.TaskCreateOptions{Mode: "src"})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	taskID := parseIntDefault(managed.ID)
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM tasks WHERE id=$1`, taskID) })
	var assetID int64
	if err := m.pg.QueryRow(`INSERT INTO assets(type, url, ip)
VALUES ('service',$1,'127.0.0.1') RETURNING id`, page.URL).Scan(&assetID); err != nil {
		t.Fatalf("asset: %v", err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM assets WHERE id=$1`, assetID) })
	if _, err := m.pg.Exec(`INSERT INTO task_scope(task_id, kind, net, source, reason)
VALUES ($1,'ip','127.0.0.1/32','manual','screenshot test')`, taskID); err != nil {
		t.Fatalf("scope: %v", err)
	}

	ctx := context.Background()
	res, err := s.runSrcScreenshot(ctx, agent.SrcScreenshotRequest{
		TaskID: taskID, AssetID: assetID, Note: "测试截图",
	})
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if res.NodeID <= 0 || res.ArtifactID <= 0 || res.MIME != "image/png" || res.Size == 0 {
		t.Fatalf("result=%+v", res)
	}
	art, err := m.pg.GetEvidenceArtifact(ctx, res.ArtifactID)
	if err != nil || art == nil || art.NodeID != res.NodeID || art.TaskID != taskID {
		t.Fatalf("artifact=%+v err=%v", art, err)
	}
	// screenshots done 必须挂真实图片产物。
	doc := srcmode.DefaultDoc()
	chk := db.SrcCheck{
		TaskID: taskID, AssetID: assetID, CheckKey: "screenshots",
		State: db.SrcCheckDone, EvidenceNodeID: res.NodeID, UpdatedBy: "test",
	}
	if err := srcmode.ValidateCheck(ctx, m.pg.Src(), taskID, doc, chk); err != nil {
		t.Fatalf("validate screenshot done: %v", err)
	}
	// 普通 fact 节点不算数。
	tk, _ := m.Task(strconv.FormatInt(taskID, 10))
	nodeID, err := tk.Store.AddNode(db.KindFact, map[string]any{"summary": "普通事实"}, 1, "confirmed", "test", []int64{assetID})
	if err != nil {
		t.Fatalf("plain node: %v", err)
	}
	chk.EvidenceNodeID = nodeID
	if err := srcmode.ValidateCheck(ctx, m.pg.Src(), taskID, doc, chk); err == nil {
		t.Fatal("plain fact node must not satisfy screenshots")
	}
	// Another asset's screenshot must not satisfy this asset's check.
	var otherAsset int64
	if err := m.pg.QueryRow(`INSERT INTO assets(type, url, ip)
VALUES ('service',$1,'127.0.0.1') RETURNING id`, page.URL+"?other").Scan(&otherAsset); err != nil {
		t.Fatalf("other asset: %v", err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM assets WHERE id=$1`, otherAsset) })
	chk.AssetID, chk.EvidenceNodeID = otherAsset, res.NodeID
	if err := srcmode.ValidateCheck(ctx, m.pg.Src(), taskID, doc, chk); err == nil {
		t.Fatal("screenshot of another asset satisfied this asset's check")
	}
	_ = fmt.Sprint(time.Now()) // keep imports stable if the test is shortened later
}
