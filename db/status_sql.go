package db

import (
	"fmt"
	"strings"
)

// TerminalStatusSQL returns the terminal-status set as a SQL literal, e.g.
// ('done','failed','timeout','policy_blocked'). Callers must only interpolate the
// returned constant string; user input never reaches this helper.
func TerminalStatusSQL() string {
	quoted := make([]string, 0, len(TerminalStatuses))
	for _, status := range TerminalStatuses {
		quoted = append(quoted, "'"+strings.ReplaceAll(status, "'", "''")+"'")
	}
	return "(" + strings.Join(quoted, ",") + ")"
}

// ValidateTerminalStatuses guards against accidental unsafe values before they are
// interpolated into SQL. The set is code-owned, so this should never fire.
func ValidateTerminalStatuses() error {
	for _, status := range TerminalStatuses {
		if status == "" || strings.ContainsAny(status, `"'\;() `) {
			return fmt.Errorf("invalid terminal status %q", status)
		}
	}
	return nil
}
