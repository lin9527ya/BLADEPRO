package server

import "testing"

// TestSettleStatusFor 钉住收尾终态判定：SRC 任务的 done 只能由收尾门给，planner 的
// met 不能越过门；非 SRC 任务必须保持原行为（met → done）。
func TestSettleStatusFor(t *testing.T) {
	cases := []struct {
		name       string
		met        bool
		src        bool
		gatePassed bool
		want       string
	}{
		{"non-src met is done", true, false, false, "done"},
		{"non-src unmet keeps fallback", false, false, false, "timeout"},
		{"src met and gate passed is done", true, true, true, "done"},
		{"src met but gate blocked keeps fallback", true, true, false, "timeout"},
		{"src unmet keeps fallback even if gate passed", false, true, true, "policy_blocked"},
	}
	for _, tc := range cases {
		fallback := "timeout"
		if tc.want == "policy_blocked" {
			fallback = "policy_blocked"
		}
		if got := settleStatusFor(tc.met, tc.src, tc.gatePassed, fallback); got != tc.want {
			t.Errorf("%s: settleStatusFor(%v,%v,%v,%q)=%q want %q",
				tc.name, tc.met, tc.src, tc.gatePassed, fallback, got, tc.want)
		}
	}
}
