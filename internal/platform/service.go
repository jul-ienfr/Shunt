package platform

import (
	"fmt"
	"os"
	"strings"
)

// ServiceTaskName is the per-user startup entry used to start the relay at
// login: a scheduled task on Windows, a systemd user unit on Linux, and a
// launchd agent on macOS.
const ServiceTaskName = "shunt-relay"

// ServiceCommand describes a startup operation and the manual command to show
// when it fails.
type ServiceCommand struct {
	Args   []string
	Manual string
}

// ServiceSupported reports whether per-user startup installation is available
// on the current platform.
func ServiceSupported() bool { return serviceSupported() }

// ServiceTaskCommand builds the command for create/run/delete. It never
// requests elevation.
func ServiceTaskCommand(action, exePath string) (ServiceCommand, error) {
	exe, err := resolveExe(exePath)
	if err != nil {
		return ServiceCommand{}, err
	}
	return serviceTaskCommand(action, exe)
}

// RunServiceTask installs, starts, or removes the per-user startup entry
// without elevation. The manual command is returned on failure so the operator
// can run it deliberately.
func RunServiceTask(action, exePath string) (string, error) {
	exe, err := resolveExe(exePath)
	if err != nil {
		return "", err
	}
	return runServiceTask(action, exe)
}

func resolveExe(exePath string) (string, error) {
	if strings.TrimSpace(exePath) != "" {
		return exePath, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return exe, nil
}

func normalizeServiceAction(action string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "install", "create":
		return "install", nil
	case "run", "start":
		return "run", nil
	case "delete", "uninstall", "remove":
		return "delete", nil
	default:
		return "", fmt.Errorf("unknown service action %q", action)
	}
}
