//go:build windows

package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
)

// OpenWindow opens the dashboard in a chromeless Edge or Chrome app window.
// When neither browser is installed it falls back to the default browser.
func OpenWindow(target string) error {
	if exe := findChromium(); exe != "" {
		cmd := exec.Command(exe,
			"--app="+target,
			"--window-size=1200,820",
			"--no-first-run",
			"--no-default-browser-check",
		)
		if err := cmd.Start(); err == nil {
			go func() { _ = cmd.Wait() }()
			return nil
		}
	}
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
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
