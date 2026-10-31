package traffic

import (
	"testing"
	"time"
)

func TestCaptureIDSurvivesSequenceRestart(t *testing.T) {
	at := time.Unix(1700000000, 10)
	if newExchangeID(at, 1) == newExchangeID(at.Add(time.Nanosecond), 1) {
		t.Fatal("same-second restart overwrites capture")
	}
	if newExchangeID(at, 1) == newExchangeID(at, 10001) {
		t.Fatal("sequence wrap overwrites capture")
	}
}

func TestSinceTaskFiltersBeforeLimit(t *testing.T) {
	tr, _ := openTraffic(t)
	tr.record(newFlow("api.capture-test.example", "GET", "/mine", nil, []byte("mine")))
	rows, err := tr.Since("api.capture-test.example", 0, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("captures=%v %v", rows, err)
	}
	if _, err := tr.db.Exec(`UPDATE exchanges SET task_id=11 WHERE id=?`, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		tr.record(newFlow("api.capture-test.example", "GET", "/other", nil, []byte("other")))
	}
	own, err := tr.SinceTask(11, "api.capture-test.example", 0, 1)
	if err != nil || len(own) != 1 || own[0].URL != rows[0].URL {
		t.Fatalf("task filter applied after limit: %+v %v", own, err)
	}
	if _, err := tr.SinceTask(0, "api.capture-test.example", 0, 1); err == nil {
		t.Fatal("unknown task accepted as evidence")
	}
}

func TestStrictTaskRateDoesNotWaiveTimeout(t *testing.T) {
	tr, _ := openTraffic(t)
	tr.SetEgressLimits(EgressLimits{GlobalQPS: 0.001, MaxWait: time.Millisecond})
	tr.SetTaskQPS(1, 1)
	if err := tr.waitEgress("api.example", 1); err != nil {
		t.Fatal(err)
	}
	if err := tr.waitEgress("api.example", 1); err == nil {
		t.Fatal("strict task timeout waived")
	}
}
