//go:build !windows && !linux && !darwin

package platform

import "fmt"

func serviceSupported() bool { return false }

// StartupKind names the platform mechanism shown in CLI output.
func StartupKind() string { return "unsupported platform" }

func serviceTaskCommand(action, exePath string) (ServiceCommand, error) {
	return ServiceCommand{}, fmt.Errorf("per-user login startup is not implemented on this platform; run %q manually", exePath+" serve --foreground")
}

func runServiceTask(action, exePath string) (string, error) {
	return "", fmt.Errorf("per-user login startup is not implemented on this platform; run %q manually", exePath+" serve --foreground")
}
