package db

import "testing"

func TestIsSrcCheckState(t *testing.T) {
	for _, s := range []string{SrcCheckDone, SrcCheckNA, SrcCheckBudgetStop} {
		if !IsSrcCheckState(s) {
			t.Fatalf("%s 应是合法收尾项终态", s)
		}
	}
	for _, s := range []string{"", "doing", "DONE", "tested_clear"} {
		if IsSrcCheckState(s) {
			t.Fatalf("%q 不该被当做收尾项终态（终态只有三个）", s)
		}
	}
}
