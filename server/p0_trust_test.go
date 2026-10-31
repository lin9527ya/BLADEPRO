package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

func p0Fixture(t *testing.T, host string) (*Manager, *Task, int64) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("BLADEPRO_LLM_API_KEY", "")
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	doc := srcmode.DefaultDoc()
	doc.RequiredAreas = map[string][]string{"service": {"paths"}}
	doc.SkipCheckGate = true
	doc.SkipReportGate = true
	doc.RateLimit = 100
	raw, _ := json.Marshal(doc)
	kind, domain, network := "subdomain", host, ""
	if host == "127.0.0.1" {
		kind = "ip"
		domain = ""
		network = "127.0.0.1/32"
	}
	task, err := m.CreateTaskWithOptions("P0 fixture", "verify", db.TaskCreateOptions{Mode: "src", ModeDoc: raw, ScopeRows: []db.TaskScopeInput{{Kind: kind, Domain: domain, Net: network}}})
	if err != nil {
		t.Fatal(err)
	}
	tid := parseIntDefault(task.ID)
	t.Cleanup(func() { _ = m.pg.DeleteTask(tid) })
	ip := ""
	if host == "127.0.0.1" {
		ip = host
	}
	var asset int64
	if err := m.pg.QueryRow(`INSERT INTO assets(type,url,domain,ip) VALUES('service',$1,NULLIF($2,''),NULLIF($3,'')) RETURNING id`, "http://"+host, domain, ip).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM assets WHERE id=$1`, asset) })
	return m, task, asset
}

func TestProbeReceiptCannotBeBorrowedOrInflated(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer target.Close()
	m, task, asset := p0Fixture(t, "127.0.0.1")
	if _, err := m.pg.Exec(`UPDATE assets SET url=$1 WHERE id=$2`, target.URL, asset); err != nil {
		t.Fatal(err)
	}
	s := &Server{m: m}
	res, err := s.runSrcProbe(t.Context(), agent.SrcProbeRequest{TaskID: parseIntDefault(task.ID), AssetID: asset, Kind: "methods", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.ProbeRunID == "" || res.EvidenceNodeID <= 0 {
		t.Fatal("host receipt not published")
	}
	check := db.SrcCheck{TaskID: parseIntDefault(task.ID), AssetID: asset, CheckKey: "method_matrix", State: db.SrcCheckDone, ProbeRunID: res.ProbeRunID, EvidenceNodeID: res.EvidenceNodeID, Metrics: res.Metrics}
	if err := m.pg.Src().ValidateProbeCheck(t.Context(), check); err != nil {
		t.Fatal(err)
	}
	check.TaskID++
	if err := m.pg.Src().ValidateProbeCheck(t.Context(), check); err == nil {
		t.Fatal("cross-task receipt accepted")
	}
	check.TaskID--
	check.Metrics = map[string]int{"methods": 999}
	if err := m.pg.Src().ValidateProbeCheck(t.Context(), check); err == nil {
		t.Fatal("inflated metrics accepted")
	}
	check.Metrics = res.Metrics
	if _, err := m.pg.Exec(`UPDATE src_probe_runs SET expires_at=now()-interval '1 second' WHERE id=$1`, res.ProbeRunID); err != nil {
		t.Fatal(err)
	}
	if err := m.pg.Src().ValidateProbeCheck(t.Context(), check); err == nil {
		t.Fatal("expired receipt accepted")
	}
}

func TestAtomicSrcCompletionFencesLateGoalsAndInvalidatesChangedProof(t *testing.T) {
	m, task, asset := p0Fixture(t, "atomic-"+uuid.NewString()+".example")
	node, err := task.Store.AddNode(db.KindFact, map[string]any{"summary": "controlled test proof"}, 1, "confirmed", "test", []int64{asset})
	if err != nil {
		t.Fatal(err)
	}
	cell := db.SrcCell{TaskID: parseIntDefault(task.ID), AssetID: asset, Area: "paths", Item: "*", State: db.SrcStateClear, EvidenceNodeID: node, Reason: "verified", UpdatedBy: "test"}
	if _, err := m.pg.Src().UpsertSrcCell(t.Context(), &cell); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(m)
	won, err := engine.completeSrcTask(t.Context(), task)
	if err != nil || !won {
		t.Fatalf("completion=%v %v", won, err)
	}
	if _, err := task.Store.AddNode(db.KindGoal, map[string]any{"summary": "late goal"}, 1, "open", "test", nil); err == nil {
		t.Fatal("late goal crossed completion fence")
	}
	cell.Reason = "changed proof"
	if _, err := m.pg.Src().UpsertSrcCell(t.Context(), &cell); err != nil {
		t.Fatal(err)
	}
	row, err := m.pg.GetTask(parseIntDefault(task.ID))
	if err != nil || row.Status != "paused" || !row.Paused {
		t.Fatalf("modified completion remained valid: %+v %v", row, err)
	}
	var proofs int
	if err := m.pg.QueryRow(`SELECT count(*) FROM src_completion_proofs WHERE task_id=$1`, parseIntDefault(task.ID)).Scan(&proofs); err != nil || proofs != 1 {
		t.Fatalf("proof history=%d %v", proofs, err)
	}
}

func TestShutdownWaitsForOwnedLoops(t *testing.T) {
	m, _, _ := p0Fixture(t, "lifecycle-"+uuid.NewString()+".example")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	td := t.TempDir()
	s := New(ctx, m, td, td, td)
	stopped := make(chan struct{})
	s.loops.start(func() { <-s.ctx.Done(); close(stopped) })
	deadline, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	if left := s.Shutdown(deadline); left != 0 {
		t.Fatalf("unfinished background work=%d", left)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("shutdown returned before owned loop stopped")
	}
}

func TestManagementMCPMaskRoundTrip(t *testing.T) {
	m, _, _ := p0Fixture(t, "mask-"+uuid.NewString()+".example")
	s := &Server{m: m}
	id, err := m.pg.SaveMCP(&db.MCPServer{Name: "mask-" + uuid.NewString(), Transport: "http", URL: "https://mcp.example/", Env: json.RawMessage(`{"Authorization":"Bearer test-secret-value","REGION":"eu"}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.pg.DeleteMCP(id) })
	w := httptest.NewRecorder()
	s.pgListMCP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(w.Body.String(), "test-secret-value") {
		t.Fatal("credential leaked in management response")
	}
	var body struct {
		Servers []*db.MCPServer `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var chosen *db.MCPServer
	for _, r := range body.Servers {
		if r.ID == id {
			chosen = r
		}
	}
	if chosen == nil {
		t.Fatal("MCP missing")
	}
	chosen.Name += "-edited"
	raw, _ := json.Marshal(chosen)
	w = httptest.NewRecorder()
	s.pgSaveMCP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(raw))))
	if w.Code != 200 {
		t.Fatalf("save=%d %s", w.Code, w.Body.String())
	}
	restored, err := m.pg.GetMCP(id)
	if err != nil || !strings.Contains(string(restored.Env), "test-secret-value") {
		t.Fatal("masked round-trip overwrote credential", err)
	}
	_ = fmt.Sprint(id)
}

func TestCompletionAndConcurrentGoalCannotBothCommit(t *testing.T) {
	m, task, _ := p0Fixture(t, "race-"+uuid.NewString()+".example")
	locked, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		won, err := m.pg.CompleteSrcTask(t.Context(), parseIntDefault(task.ID), func(_ *db.DB, _ *db.Task) (json.RawMessage, error) {
			close(locked)
			<-release
			return json.RawMessage(`{"test":"publication fence"}`), nil
		})
		if err == nil && !won {
			err = fmt.Errorf("completion lost")
		}
		result <- err
	}()
	<-locked
	goal := make(chan error, 1)
	go func() {
		_, err := task.Store.AddNode(db.KindGoal, map[string]any{"summary": "concurrent goal"}, 1, "open", "test", nil)
		goal <- err
	}()
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := <-goal; err == nil {
		t.Fatal("concurrent goal committed after done")
	}
	open, err := task.Store.HasOpenGoal()
	if err != nil || open {
		t.Fatal("completion with open goal", err)
	}
}

func TestForceCompleteSrcTaskWritesManualOverrideProof(t *testing.T) {
	m, task, _ := p0Fixture(t, "force-"+uuid.NewString()+".example")
	engine := NewEngine(m)
	won, rep, err := engine.forceCompleteSrcTask(t.Context(), task, "人工确认接受剩余缺口")
	if err != nil || !won {
		t.Fatalf("force completion=%v %v", won, err)
	}
	_ = rep
	row, err := m.pg.GetTask(parseIntDefault(task.ID))
	if err != nil || row.Status != "done" || row.CompletedAt == nil {
		t.Fatalf("task not completed: %+v %v", row, err)
	}
	var proof string
	if err := m.pg.QueryRow(`SELECT proof::text FROM src_completion_proofs WHERE task_id=$1`, parseIntDefault(task.ID)).Scan(&proof); err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(proof), &parsed); err != nil {
		t.Fatalf("override proof is not JSON: %v (%s)", err, proof)
	}
	if parsed["manual_override"] != true || parsed["reason"] != "人工确认接受剩余缺口" {
		t.Fatalf("override proof missing marker/reason: %s", proof)
	}
	won, _, err = engine.forceCompleteSrcTask(t.Context(), task, "第二次")
	if err != nil || won {
		t.Fatalf("second force completion should lose CAS: won=%v err=%v", won, err)
	}
}
