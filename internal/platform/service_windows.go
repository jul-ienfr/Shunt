//go:build windows

package platform

import (
	"fmt"
	"os/exec"
	"strings"
)

func serviceSupported() bool { return true }

// StartupKind names the platform mechanism shown in CLI output.
func StartupKind() string { return "Windows scheduled task" }

func serviceTaskCommand(action, exePath string) (ServiceCommand, error) {
	action, err := normalizeServiceAction(action)
	if err != nil {
		return ServiceCommand{}, err
	}
	quoted := `\"` + exePath + `\" serve --foreground`
	switch action {
	case "install":
		args := []string{"/Create", "/TN", ServiceTaskName, "/SC", "ONLOGON", "/TR", quoted, "/F", "/RL", "LIMITED"}
		return ServiceCommand{Args: args, Manual: "schtasks " + strings.Join(args, " ")}, nil
	case "run":
		args := []string{"/Run", "/TN", ServiceTaskName}
		return ServiceCommand{Args: args, Manual: "schtasks " + strings.Join(args, " ")}, nil
	default:
		args := []string{"/Delete", "/TN", ServiceTaskName, "/F"}
		return ServiceCommand{Args: args, Manual: "schtasks " + strings.Join(args, " ")}, nil
	}
}

func runServiceTask(action, exePath string) (string, error) {
	cmd, err := serviceTaskCommand(action, exePath)
	if err != nil {
		return "", err
	}
	out, err := exec.Command("schtasks", cmd.Args...).CombinedOutput()
	if err != nil {
		return cmd.Manual, fmt.Errorf("schtasks failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return cmd.Manual, nil
}
