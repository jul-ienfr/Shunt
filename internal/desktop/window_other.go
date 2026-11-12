//go:build !windows

package desktop

import (
	"os/exec"
	"runtime"
)

// OpenWindow opens the dashboard with the platform's default browser.
func OpenWindow(target string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", target).Start()
	}
	return exec.Command("xdg-open", target).Start()
}
