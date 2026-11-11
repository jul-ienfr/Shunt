package platform

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ServiceTaskName is the per-user scheduled task used to start the relay at
// login.
const ServiceTaskName = "shunt-relay"

// ServiceCommand describes a scheduled-task operation and the manual command to
// show when it fails.
type ServiceCommand struct {
	Args   []string
	Manual string
}

// ServiceSupported reports whether per-user startup installation is available.
func ServiceSupported() bool { return runtime.GOOS == "windows" }

// ServiceTaskCommand builds the schtasks command for create/run/delete. It
// never requests elevation.
func ServiceTaskCommand(action, exePath string) (ServiceCommand, error) {
	if !ServiceSupported() {
		return ServiceCommand{}, fmt.Errorf("automatic login startup is only implemented for Windows; run %q manually", exePath+" serve --foreground")
	}
	if strings.TrimSpace(exePath) == "" {
		exe, err := os.Executable()
		if err != nil {
			return ServiceCommand{}, err
		}
		exePath = exe
	}
	quoted := `\"` + exePath + `\" serve --foreground`
	switch strings.ToLower(action) {
	case "install", "create":
		args := []string{"/Create", "/TN", ServiceTaskName, "/SC", "ONLOGON", "/TR", quoted, "/F", "/RL", "LIMITED"}
		return ServiceCommand{Args: args, Manual: "schtasks " + strings.Join(args, " ")}, nil
	case "run":
		args := []string{"/Run", "/TN", ServiceTaskName}
		return ServiceCommand{Args: args, Manual: "schtasks " + strings.Join(args, " ")}, nil
	case "delete", "uninstall":
		args := []string{"/Delete", "/TN", ServiceTaskName, "/F"}
		return ServiceCommand{Args: args, Manual: "schtasks " + strings.Join(args, " ")}, nil
	default:
		return ServiceCommand{}, fmt.Errorf("unknown service action %q", action)
	}
}

// RunServiceTask executes an schtasks action without elevation. The manual
// command is returned on failure so the operator can run it deliberately.
func RunServiceTask(action, exePath string) (string, error) {
	cmd, err := ServiceTaskCommand(action, exePath)
	if err != nil {
		return "", err
	}
	out, err := exec.Command("schtasks", cmd.Args...).CombinedOutput()
	if err != nil {
		return cmd.Manual, fmt.Errorf("schtasks failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return cmd.Manual, nil
}
