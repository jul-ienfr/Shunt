//go:build linux

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const serviceUnitName = ServiceTaskName + ".service"

func serviceSupported() bool { return true }

// StartupKind names the platform mechanism shown in CLI output.
func StartupKind() string { return "systemd user unit" }

// systemdUnitPath returns the per-user unit path without creating anything.
func systemdUnitPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, "systemd", "user", serviceUnitName), nil
}

func renderUnit(exePath string) string {
	// systemd expands % specifiers inside ExecStart.
	escaped := strings.ReplaceAll(exePath, "%", "%%")
	return fmt.Sprintf(`[Unit]
Description=Shunt local relay
After=network-online.target

[Service]
Type=simple
ExecStart="%s" serve --foreground
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`, escaped)
}

func serviceTaskCommand(action, exePath string) (ServiceCommand, error) {
	action, err := normalizeServiceAction(action)
	if err != nil {
		return ServiceCommand{}, err
	}
	path, err := systemdUnitPath()
	if err != nil {
		return ServiceCommand{}, err
	}
	switch action {
	case "install":
		manual := fmt.Sprintf("systemctl --user daemon-reload && systemctl --user enable --now %s.service", ServiceTaskName)
		return ServiceCommand{Manual: manual}, nil
	case "run":
		return ServiceCommand{Manual: fmt.Sprintf("systemctl --user start %s.service", ServiceTaskName)}, nil
	default:
		manual := fmt.Sprintf("systemctl --user disable --now %s.service && rm %q && systemctl --user daemon-reload", ServiceTaskName, path)
		return ServiceCommand{Manual: manual}, nil
	}
}

func runServiceTask(action, exePath string) (string, error) {
	action, err := normalizeServiceAction(action)
	if err != nil {
		return "", err
	}
	cmd, err := serviceTaskCommand(action, exePath)
	if err != nil {
		return "", err
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return cmd.Manual, fmt.Errorf("systemd is not available; run the manual step")
	}
	path, err := systemdUnitPath()
	if err != nil {
		return cmd.Manual, err
	}
	switch action {
	case "install":
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return cmd.Manual, err
		}
		if err := os.WriteFile(path, []byte(renderUnit(exePath)), 0o644); err != nil {
			return cmd.Manual, fmt.Errorf("write unit %s: %w", path, err)
		}
		if out, err := systemctl("--user", "daemon-reload"); err != nil {
			return cmd.Manual, fmt.Errorf("daemon-reload: %v: %s", err, out)
		}
		if out, err := systemctl("--user", "enable", "--now", serviceUnitName); err != nil {
			return cmd.Manual, fmt.Errorf("enable unit: %v: %s", err, out)
		}
	case "run":
		if out, err := systemctl("--user", "start", serviceUnitName); err != nil {
			return cmd.Manual, fmt.Errorf("start unit: %v: %s", err, out)
		}
	default:
		// Stop and disable even if one step fails, then remove the unit.
		_, _ = systemctl("--user", "disable", "--now", serviceUnitName)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return cmd.Manual, fmt.Errorf("remove unit %s: %w", path, err)
		}
		if out, err := systemctl("--user", "daemon-reload"); err != nil {
			return cmd.Manual, fmt.Errorf("daemon-reload: %v: %s", err, out)
		}
	}
	return cmd.Manual, nil
}

func systemctl(args ...string) (string, error) {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
