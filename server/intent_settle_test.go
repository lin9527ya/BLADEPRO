package server

import (
	"errors"
	"testing"

	"github.com/lin9527ya/Norma/harness"
	"github.com/lin9527ya/bladepro/agent"
)

func TestSettleIntentState(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		reason  harness.TerminalReason
		wrote   agent.WriteCounts
		outcome string
		prior   bool
		want    string
	}{
		{"empty normal run is no_output", nil, "", agent.WriteCounts{}, "", false, "no_output"},
		{"facts count as output", nil, "", agent.WriteCounts{Facts: 1}, "", false, "done"},
		{"src cell counts as output", nil, "", agent.WriteCounts{SrcCells: 1}, "", false, "done"},
		{"src check counts as output", nil, "", agent.WriteCounts{SrcChecks: 1}, "", false, "done"},
		{"evidence counts as output", nil, "", agent.WriteCounts{Evidence: 1}, "", false, "done"},
		{"prior output makes continuation done", nil, "", agent.WriteCounts{}, "", true, "done"},
		{"semantic negative wins", nil, "", agent.WriteCounts{}, "negative", false, "negative"},
		{"out of scope wins", nil, "", agent.WriteCounts{}, "out-of-scope", false, "out-of-scope"},
		{"error is blocked", errors.New("boom"), "", agent.WriteCounts{Facts: 1}, "", false, "blocked"},
		{"max turns is exhausted", nil, harness.ReasonMaxTurns, agent.WriteCounts{Facts: 1}, "negative", false, "exhausted"},
		{"timeout is exhausted", nil, harness.ReasonTimeout, agent.WriteCounts{}, "", false, "exhausted"},
	}
	for _, tc := range cases {
		if got := settleIntentState(tc.err, tc.reason, tc.wrote, tc.outcome, tc.prior); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}
