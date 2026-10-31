package intercept

import (
	"encoding/json"
	"strings"
)

var builtinActionTools = map[string]bool{
	"Bash": true, "shell_open": true, "shell_send": true, "run_execution": true,
	"WebFetch": true, "web_search": true, "Write": true, "Edit": true, "MultiEdit": true,
	"src_probe": true, "src_screenshot": true, "create_mcp": true, "update_mcp": true,
	"create_custom_tool": true, "update_custom_tool": true,
}
var builtinReadTools = map[string]bool{
	"Read": true, "LS": true, "Glob": true, "Grep": true, "graph_overview": true,
	"list_facts": true, "list_assets": true, "src_state": true, "get_worker_output": true,
	"record_fact": true, "report_finding": true, "src_mark_cell": true, "src_mark_check": true,
	"src_prepare_submission": true, "conclude_intent": true, "wait_execution": true,
	"get_finding_retest_context": true, "record_finding_retest_result": true,
}

func BuiltinActionTool(name string) bool { return builtinActionTools[name] }
func BuiltinReadTool(name string) bool   { return builtinReadTools[name] }

// Unknown external tools cannot prove they are read-only. Classification failure
// remains conservative, so it cannot disable the emergency stop/time-window gate.
func (i *Interceptor) actionSurface(name string, input json.RawMessage) (string, bool) {
	if builtinActionTools[name] {
		return strings.TrimSpace(shellCommandOf(name, input) + " " + rawInputText(input)), true
	}
	if strings.HasPrefix(name, "mcp__") {
		return rawInputText(input), true
	}
	if builtinReadTools[name] {
		return "", false
	}
	if i.db == nil {
		return rawInputText(input), true
	}
	tool, err := i.db.GetTool(name)
	if err != nil || tool == nil {
		return rawInputText(input), true
	}
	if tool.Kind == "command" || tool.Kind == "script" || tool.Kind == "http" {
		return rawInputText(input) + " " + string(tool.Exec), true
	}
	return "", false
}
func (i *Interceptor) IsActionTool(name string, input json.RawMessage) bool {
	_, action := i.actionSurface(name, input)
	return action
}
func rawInputText(raw []byte) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	var out strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			out.WriteString(x)
			out.WriteByte(' ')
		case []any:
			for _, v := range x {
				walk(v)
			}
		case map[string]any:
			for _, v := range x {
				walk(v)
			}
		}
	}
	walk(value)
	return out.String()
}
