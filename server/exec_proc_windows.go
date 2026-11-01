//go:build windows

package server

import (
	"os/exec"
	"strconv"
	"syscall"
)

// Windows：新进程组（Ctrl-C 不波及）。任务取消必须杀整棵进程树；只 Kill cmd.exe
// 会把真正的扫描器/浏览器/Node 子进程留在后台，继续占用端口和文件。
func startDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// shellCommandArgs mirrors the agent Bash tool on Windows (PowerShell), so a
// command behaves the same in run_execution and Bash. Keep the exit code of the
// last native command instead of PowerShell's success status.
func shellCommandArgs(command string) []string {
	return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command + "; exit $LASTEXITCODE"}
}

func killGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	killer := exec.Command("taskkill", "/PID", pid, "/T", "/F")
	killer.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = killer.Run()
	// Fallback for locked-down hosts where taskkill is blocked or the PID already
	// belonged to a wrapper that exited.
	_ = cmd.Process.Kill()
}

func hardKillGroup(cmd *exec.Cmd) { killGroup(cmd) }
