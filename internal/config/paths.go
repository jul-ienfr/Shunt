package config

import (
	"path/filepath"

	"shunt/internal/platform"
)

// ResolvePaths applies the [paths] overrides to the platform defaults. Empty
// values keep the default, a leading ~ is expanded, and relative paths resolve
// against the directory containing the configuration file.
func ResolvePaths(cfg Config, configPath string) (platform.Paths, error) {
	paths, err := platform.DefaultPaths()
	if err != nil {
		return paths, err
	}
	if configPath != "" {
		paths.ConfigFile = configPath
	}
	baseDir := filepath.Dir(paths.ConfigFile)
	resolve := func(value string) (string, error) {
		if value == "" {
			return "", nil
		}
		expanded, err := platform.ExpandHome(value)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(expanded) {
			expanded = filepath.Join(baseDir, expanded)
		}
		return filepath.Clean(expanded), nil
	}
	if cfg.Paths.State != "" {
		if paths.StateFile, err = resolve(cfg.Paths.State); err != nil {
			return paths, err
		}
	}
	if cfg.Paths.Runtime != "" {
		if paths.RuntimeFile, err = resolve(cfg.Paths.Runtime); err != nil {
			return paths, err
		}
	}
	if cfg.Paths.CacheDir != "" {
		if paths.CacheDir, err = resolve(cfg.Paths.CacheDir); err != nil {
			return paths, err
		}
	}
	if cfg.Paths.LogDir != "" {
		if paths.LogDir, err = resolve(cfg.Paths.LogDir); err != nil {
			return paths, err
		}
	}
	if cfg.Paths.BackupDir != "" {
		if paths.BackupDir, err = resolve(cfg.Paths.BackupDir); err != nil {
			return paths, err
		}
	}
	return paths, nil
}
