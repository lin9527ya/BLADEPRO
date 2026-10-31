package mcphttp

import (
	"os"
	"testing"
)

// Existing transport fixtures deliberately host their MCP endpoints on loopback.
func TestMain(m *testing.M) { _ = os.Setenv(AllowLocalEnv, "1"); os.Exit(m.Run()) }
