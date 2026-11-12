package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home dir: %w", err)
	}
	return defaultPathsFor(runtime.GOOS, home, os.Getenv)
}

// defaultPathsFor computes the directory layout for a target OS. It is a pure
// function of the OS, the home directory, and the environment so every layout
// stays testable on any host.
func defaultPathsFor(goos, home string, getenv func(string) string) (Paths, error) {
	switch goos {
	case "windows":
		configRoot := getenv("APPDATA")
		dataRoot := getenv("LOCALAPPDATA")
		if configRoot == "" || dataRoot == "" {
			// Fall back to the standard library resolution, which honours the
			// same variables on Windows.
			var err error
			if configRoot == "" {
				if configRoot, err = os.UserConfigDir(); err != nil {
					return Paths{}, fmt.Errorf("resolve user config dir: %w", err)
				}
			}
			if dataRoot == "" {
				if dataRoot, err = os.UserCacheDir(); err != nil {
					return Paths{}, fmt.Errorf("resolve user data dir: %w", err)
				}
			}
		}
		configDir := filepath.Join(configRoot, "shunt")
		dataDir := filepath.Join(dataRoot, "shunt")
		return assemble(configDir, dataDir, dataDir, dataDir, filepath.Join(dataDir, "backups"), filepath.Join(dataDir, "cache")), nil

	case "darwin":
		support := filepath.Join(home, "Library", "Application Support", "shunt")
		cache := filepath.Join(home, "Library", "Caches", "shunt")
		logs := filepath.Join(home, "Library", "Logs", "shunt")
		return assemble(support, support, support, logs, support, cache), nil

	default:
		// Linux, BSDs, and any other Unix: follow the XDG base directory spec.
		configHome := xdg(getenv, "XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		stateHome := xdg(getenv, "XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
		cacheHome := xdg(getenv, "XDG_CACHE_HOME", filepath.Join(home, ".cache"))
		return assemble(
			filepath.Join(configHome, "shunt"),
			filepath.Join(stateHome, "shunt"),
			filepath.Join(stateHome, "shunt"),
			filepath.Join(stateHome, "shunt", "logs"),
			filepath.Join(stateHome, "shunt", "backups"),
			filepath.Join(cacheHome, "shunt"),
		), nil
	}
}

// assemble keeps the layout rules in one place: config lives in configDir,
// mutable state in stateDir, logs and backups in their own directories.
func assemble(configDir, stateDir, runtimeDir, logDir, backupDir, cacheDir string) Paths {
	return Paths{
		ConfigFile:  filepath.Join(configDir, "config.toml"),
		StateFile:   filepath.Join(stateDir, "state.json"),
		RuntimeFile: filepath.Join(runtimeDir, "runtime.json"),
		CacheDir:    cacheDir,
		LogDir:      logDir,
		BackupDir:   backupDir,
	}
}

func xdg(getenv func(string) string, name, fallback string) string {
	if v := getenv(name); v != "" {
		return v
	}
	return fallback
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
