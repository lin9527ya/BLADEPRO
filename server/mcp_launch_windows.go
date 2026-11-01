//go:build windows

package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// stdioLaunch resolves Windows command shims before handing the command to the
// MCP SDK. CreateProcess cannot execute .cmd/.bat directly, so npx/npm-style
// servers must go through cmd.exe. Native .exe servers stay direct.
func stdioLaunch(command string, args []string) (string, []string, error) {
	resolved := strings.TrimSpace(command)
	if resolved == "" {
		return "", nil, fmt.Errorf("stdio MCP 命令为空")
	}
	if p, err := exec.LookPath(resolved); err == nil {
		resolved = p
	} else if strings.EqualFold(resolved, "npx") || strings.EqualFold(resolved, "npm") ||
		strings.EqualFold(resolved, "pnpm") || strings.EqualFold(resolved, "yarn") {
		return "", nil, fmt.Errorf("未找到 %s：Windows 上请安装 Node.js 并确保 %s.cmd 在 PATH 中", resolved, resolved)
	}
	switch strings.ToLower(filepath.Ext(resolved)) {
	case ".cmd", ".bat":
		comspec := strings.TrimSpace(os.Getenv("ComSpec"))
		if comspec == "" {
			comspec = "cmd.exe"
		}
		argv := []string{"/d", "/s", "/c", resolved}
		argv = append(argv, args...)
		return comspec, argv, nil
	}
	return resolved, args, nil
}
