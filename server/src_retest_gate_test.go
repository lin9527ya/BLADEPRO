package server

import (
	"testing"
	"time"

	"github.com/lin9527ya/bladepro/db"
)

// TestSrcRetestState 钉住提交前复验的判定：只有 completed+reproduced 才放行；
// active 与失败结论都不能放行，且失败结论要能报出最近一次的 verdict。
func TestSrcRetestState(t *testing.T) {
	cases := []struct {
		name       string
		rows       []*db.FindingRetest
		wantPassed bool
		wantActive bool
		wantLast   string
	}{
		{"empty", nil, false, false, ""},
		{"reproduced passes", []*db.FindingRetest{
			{Status: "completed", Verdict: "reproduced"},
		}, true, false, "reproduced"},
		{"running blocks", []*db.FindingRetest{
			{Status: "running"},
		}, false, true, ""},
		{"fixed blocks", []*db.FindingRetest{
			{Status: "completed", Verdict: "fixed"},
		}, false, false, "fixed"},
		{"latest fixed supersedes historical reproduced", []*db.FindingRetest{
			{Status: "completed", Verdict: "fixed"},
			{Status: "completed", Verdict: "reproduced"},
		}, false, false, "fixed"},
	}
	for _, tc := range cases {
		passed, active, last := srcRetestState(tc.rows)
		if passed != tc.wantPassed || active != tc.wantActive || last != tc.wantLast {
			t.Errorf("%s: got passed=%v active=%v last=%q, want %v/%v/%q",
				tc.name, passed, active, last, tc.wantPassed, tc.wantActive, tc.wantLast)
		}
	}
}

func TestSrcRetestRejectsOldEvidenceAndExpiredProof(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-time.Minute)
	old := now.Add(-8 * 24 * time.Hour)
	f := &db.DBFinding{EvidenceVersion: 2}
	r := &db.FindingRetest{Status: "completed", Verdict: "reproduced", EvidenceVersion: 2, FinishedAt: &fresh}
	if !srcRetestCurrent([]*db.FindingRetest{r}, f, now) {
		t.Fatal("fresh matching proof rejected")
	}
	r.EvidenceVersion = 1
	if srcRetestCurrent([]*db.FindingRetest{r}, f, now) {
		t.Fatal("stale version accepted")
	}
	r.EvidenceVersion = 2
	r.FinishedAt = &old
	if srcRetestCurrent([]*db.FindingRetest{r}, f, now) {
		t.Fatal("expired proof accepted")
	}
}

func TestActiveRetestSupersedesPriorSuccess(t *testing.T) {
	passed, active, _ := srcRetestState([]*db.FindingRetest{{Status: "running"}, {Status: "completed", Verdict: "reproduced"}})
	if passed || !active {
		t.Fatal("active retest inherited old success")
	}
}
