package agent

import (
	"github.com/lin9527ya/Norma/mcp"
	actool "github.com/lin9527ya/Norma/tool"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMCPStdioAppliesHostExecutionHook(t *testing.T) {
	called := false
	actool.SetExecHook(func(cmd *exec.Cmd) { called = true; cmd.Path = filepath.Join(t.TempDir(), "denied-command") })
	t.Cleanup(func() { actool.SetExecHook(nil) })
	if _, err := mcp.NewStdioClient(t.Context(), "fixture", "unused", nil); err == nil {
		t.Fatal("host hook did not block child startup")
	}
	if !called {
		t.Fatal("MCP subprocess bypassed host execution hook")
	}
}
