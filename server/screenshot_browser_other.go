//go:build !windows

package server

func screenshotBrowserCandidates() []string {
	return []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"}
}
