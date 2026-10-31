package server

import (
	"github.com/lin9527ya/Norma/harness"
	"github.com/lin9527ya/bladepro/agent"
)

// settleIntentState is the single rule for a normal worker return. A normal
// return is not automatically "done": a run that wrote nothing (and whose
// earlier turns wrote nothing either) settles no_output so the planner replays
// or changes approach instead of treating the direction as covered.
func settleIntentState(err error, reason harness.TerminalReason, wrote agent.WriteCounts, semanticOutcome string, priorOutput bool) string {
	state := "done"
	switch {
	case err != nil:
		state = "blocked"
	case reason == harness.ReasonMaxTurns || reason == harness.ReasonTimeout:
		state = "exhausted"
	}
	if state != "done" {
		return state
	}
	if semanticOutcome == "negative" || semanticOutcome == "out-of-scope" {
		return semanticOutcome
	}
	if wrote.Total() == 0 && !priorOutput {
		return "no_output"
	}
	return "done"
}
