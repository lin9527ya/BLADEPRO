//go:build !windows

package server

import "context"

// 非 Windows 平台没有进程树内存看门狗（执行层已有 ulimit 限制）；保持空实现以便跨平台构建。
func startProcMemWatchdog(ctx context.Context, s *Server) {}
