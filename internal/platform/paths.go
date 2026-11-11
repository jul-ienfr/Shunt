package platform

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths holds every location the tool reads or writes. Overrides from the
// [paths] configuration section are applied by config.Load, not here.
type Paths struct {
	ConfigFile  string
	StateFile   string
	RuntimeFile string
	CacheDir    string
	LogDir      string
	BackupDir   string
}

// DefaultPaths resolves platform-appropriate directories without creating them.
func DefaultPaths() (Paths, error) {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user config dir: %w", err)
	}
	dataRoot, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user cache dir: %w", err)
	}
	base := filepath.Join(dataRoot, "shunt")
	return Paths{
		ConfigFile:  filepath.Join(configRoot, "shunt", "config.toml"),
		StateFile:   filepath.Join(base, "state.json"),
		RuntimeFile: filepath.Join(base, "runtime.json"),
		CacheDir:    filepath.Join(base, "cache"),
		LogDir:      filepath.Join(base, "logs"),
		BackupDir:   filepath.Join(base, "backups"),
	}, nil
}

// Ensure creates every writable directory with user-only permissions where the
// platform supports them.
func (p Paths) Ensure() error {
	for _, dir := range []string{
		filepath.Dir(p.ConfigFile),
		filepath.Dir(p.StateFile),
		p.CacheDir,
		p.LogDir,
		p.BackupDir,
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}

// ExpandHome expands a leading ~ using the current user's home directory.
func ExpandHome(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" || len(path) > 1 && (path[:2] == "~/" || path[:2] == `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}
