//go:build windows

package server

import (
	"os"
	"path/filepath"
)

func screenshotBrowserCandidates() []string {
	out := []string{"chrome.exe", "msedge.exe"}
	roots := []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("LocalAppData"),
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		out = append(out,
			filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"),
		)
	}
	return out
}
