package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lin9527ya/Norma/permission"
	actool "github.com/lin9527ya/Norma/tool"
)

type stubTool struct {
	name string
	res  actool.Result
}

func (s stubTool) Name() string        { return s.name }
func (s stubTool) Description() string { return "" }
func (s stubTool) Prompt() string      { return "" }
func (s stubTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (s stubTool) Call(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
	return s.res, nil
}
func (s stubTool) CheckPermissions(context.Context, json.RawMessage, permission.Context) permission.Decision {
	return permission.Decision{}
}
func (s stubTool) IsConcurrencySafe(json.RawMessage) bool { return true }
func (s stubTool) IsReadOnly(json.RawMessage) bool        { return true }

func TestFailureHintOnlyOnError(t *testing.T) {
	failed := WithFailureHint(stubTool{name: "Bash", res: actool.Errorf("command not found: foo")})
	res, err := failed.Call(context.Background(), nil, nil)
	if err != nil || len(res.Content) < 2 {
		t.Fatalf("failure hint missing: %+v %v", res, err)
	}
	ok := WithFailureHint(stubTool{name: "Bash", res: actool.Text("permission denied in scan output")})
	res, err = ok.Call(context.Background(), nil, nil)
	if err != nil || len(res.Content) != 1 {
		t.Fatalf("success result must not gain a hint: %+v %v", res, err)
	}
}
