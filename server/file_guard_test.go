package server

import (
	actool "github.com/lin9527ya/Norma/tool"
	"path/filepath"
	"testing"
)

func TestDefaultFileBoundaryDoesNotNeedPrivilegeDrop(t *testing.T) {
	old := fileGuard
	t.Cleanup(func() { fileGuard = old; actool.SetPathGuard(nil) })
	t.Setenv("BLADEPRO_AGENT_READ_ROOTS", "")
	data, key, skills := t.TempDir(), t.TempDir(), t.TempDir()
	if err := InstallFileRootGuard(data, key, skills); err != nil {
		t.Fatal(err)
	}
	if err := agentPathGuard(filepath.Join(data, "out.txt")); err != nil {
		t.Fatal(err)
	}
	if err := agentPathGuard(filepath.Join(key, "jwt.key")); err == nil {
		t.Fatal("key path accepted")
	}
	if err := InstallFileRootGuard(key, key, skills); err == nil {
		t.Fatal("key directory accepted as file root")
	}
}
