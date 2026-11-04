//go:build windows

package server

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// P-Q 进程树内存看门狗（移植自 quiver watchdog，Windows 专用）：
// 每 20 秒扫描本进程的后代进程，按「直接子进程 → 整棵子树」汇总工作集（WorkingSet），
// 超过 worker_proc_mem_limit_mb（默认 8192，0=关）就 taskkill /T /F 整支子树。
//
// 只保护本进程的后代，不碰系统其它进程；杀之前写日志（含 pid 与 MB），便于事后核对。

type procMemCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

var (
	modPsapi             = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemory = modPsapi.NewProc("GetProcessMemoryInfo")
)

func processWorkingSet(pid uint32) uintptr {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var c procMemCounters
	c.CB = uint32(unsafe.Sizeof(c))
	r, _, _ := procGetProcessMemory.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.CB))
	if r == 0 {
		return 0
	}
	return c.WorkingSetSize
}

// procTree 返回 父进程 → 子进程列表（只含当前快照里的进程）。
func procTree() map[uint32][]uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap) //nolint:errcheck
	out := map[uint32][]uint32{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := windows.Process32First(snap, &e); err != nil {
		return out
	}
	for {
		out[e.ParentProcessID] = append(out[e.ParentProcessID], e.ProcessID)
		if err := windows.Process32Next(snap, &e); err != nil {
			break
		}
	}
	return out
}

// descendants 返回某进程的全部后代（不含自身）。
func descendants(tree map[uint32][]uint32, root uint32) []uint32 {
	var out []uint32
	queue := append([]uint32{}, tree[root]...)
	seen := map[uint32]bool{}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] || pid == root {
			continue
		}
		seen[pid] = true
		out = append(out, pid)
		queue = append(queue, tree[pid]...)
	}
	return out
}

// startProcMemWatchdog 常驻扫描；limit 为 0 时跳过本轮（每轮现读设置）。
func startProcMemWatchdog(ctx context.Context, s *Server) {
	self := uint32(os.Getpid())
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		limitMB := 8192
		if v, _, _ := s.m.pg.GetSetting(settingWorkerProcMemLimitMB); strings.TrimSpace(v) != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				limitMB = n
			}
		}
		if limitMB <= 0 {
			continue // 关闭
		}
		tree := procTree()
		if len(tree) == 0 {
			continue
		}
		limit := uintptr(limitMB) * 1024 * 1024
		for _, child := range tree[self] {
			subtree := descendants(tree, child)
			var total uintptr
			for _, pid := range subtree {
				total += processWorkingSet(pid)
			}
			total += processWorkingSet(child)
			if total <= limit {
				continue
			}
			log.Printf("[watchdog] 子进程 %d 子树内存 %.0fMB 超过上限 %dMB，整支终止（%d 个进程）",
				child, float64(total)/1024/1024, limitMB, len(subtree)+1)
			_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(int(child))).Run()
		}
	}
}
