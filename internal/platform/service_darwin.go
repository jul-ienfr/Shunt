//go:build darwin

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const serviceLabel = "com.shunt.relay"

func serviceSupported() bool { return true }

// StartupKind names the platform mechanism shown in CLI output.
func StartupKind() string { return "launchd agent" }

// launchAgentPath returns the per-user LaunchAgent plist path.
func launchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist"), nil
}

func renderPlist(exePath string) string {
	escape := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>serve</string>
		<string>--foreground</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<false/>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`, serviceLabel, escape.Replace(exePath))
}

func serviceTaskCommand(action, exePath string) (ServiceCommand, error) {
	action, err := normalizeServiceAction(action)
	if err != nil {
		return ServiceCommand{}, err
	}
	path, err := launchAgentPath()
	if err != nil {
		return ServiceCommand{}, err
	}
	switch action {
	case "install":
		return ServiceCommand{Manual: fmt.Sprintf("launchctl load -w %q", path)}, nil
	case "run":
		return ServiceCommand{Manual: fmt.Sprintf("launchctl start %s", serviceLabel)}, nil
	default:
		return ServiceCommand{Manual: fmt.Sprintf("launchctl unload -w %q && rm %q", path, path)}, nil
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
	if _, err := exec.LookPath("launchctl"); err != nil {
		return cmd.Manual, fmt.Errorf("launchctl is not available; run the manual step")
	}
	path, err := launchAgentPath()
	if err != nil {
		return cmd.Manual, err
	}
	switch action {
	case "install":
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return cmd.Manual, err
		}
		if err := os.WriteFile(path, []byte(renderPlist(exePath)), 0o644); err != nil {
			return cmd.Manual, fmt.Errorf("write plist %s: %w", path, err)
		}
		out, err := launchctl("load", "-w", path)
		if err != nil {
			return cmd.Manual, fmt.Errorf("launchctl load: %v: %s", err, out)
		}
	case "run":
		if out, err := launchctl("start", serviceLabel); err != nil {
			return cmd.Manual, fmt.Errorf("launchctl start: %v: %s", err, out)
		}
	default:
		_, _ = launchctl("unload", "-w", path)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return cmd.Manual, fmt.Errorf("remove plist %s: %w", path, err)
		}
	}
	return cmd.Manual, nil
}

func launchctl(args ...string) (string, error) {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
