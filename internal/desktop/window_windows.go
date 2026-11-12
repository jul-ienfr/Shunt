//go:build windows

package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// OpenWindow opens the dashboard in a chromeless Edge or Chrome app window.
// The window uses its own browser profile so a signed-in session, extension, or
// policy attached to the default profile cannot leave it blank, and it bypasses
// any system proxy for loopback addresses. When no Chromium window can be
// started it falls back to the default browser.
func OpenWindow(target string) error {
	if exe := findChromium(); exe != "" {
		args := []string{
			"--app=" + target,
			"--window-size=1200,820",
			"--no-first-run",
			"--no-default-browser-check",
			"--proxy-bypass-list=127.0.0.1;localhost;[::1]",
		}
		if dir := windowProfileDir(); dir != "" {
			args = append(args, "--user-data-dir="+dir)
		}
		cmd := exec.Command(exe, args...)
		if err := cmd.Start(); err == nil {
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				if err == nil {
					return nil
				}
			case <-time.After(750 * time.Millisecond):
				go func() { <-done }()
				return nil
			}
		}
	}
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
}

// windowProfileDir returns a stable private Chromium profile for the dashboard.
// It is created under the per-user cache directory, not next to the binary.
func windowProfileDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		return ""
	}
	dir := filepath.Join(base, "shunt", "ui-profile")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return dir
}

// findChromium returns the first installed Edge or Chrome executable.
func findChromium() string {
	var candidates []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
		base := os.Getenv(env)
		if base == "" {
			continue
		}
		candidates = append(candidates,
			filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
		)
	}
	for _, name := range []string{"msedge.exe", "chrome.exe"} {
		if path, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, path)
		}
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
