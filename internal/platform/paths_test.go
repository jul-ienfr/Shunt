package platform

import (
	"path/filepath"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestDefaultPathsWindows(t *testing.T) {
	paths, err := defaultPathsFor("windows", `C:\Users\ada`, env(map[string]string{
		"APPDATA":      "/appdata",
		"LOCALAPPDATA": "/localappdata",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		ConfigFile:  filepath.Join("/appdata", "shunt", "config.toml"),
		StateFile:   filepath.Join("/localappdata", "shunt", "state.json"),
		RuntimeFile: filepath.Join("/localappdata", "shunt", "runtime.json"),
		CacheDir:    filepath.Join("/localappdata", "shunt", "cache"),
		LogDir:      filepath.Join("/localappdata", "shunt"),
		BackupDir:   filepath.Join("/localappdata", "shunt", "backups"),
	}
	if paths != want {
		t.Fatalf("paths = %+v, want %+v", paths, want)
	}
}

func TestDefaultPathsDarwin(t *testing.T) {
	paths, err := defaultPathsFor("darwin", "/Users/ada", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	support := filepath.Join("/Users/ada", "Library", "Application Support", "shunt")
	want := Paths{
		ConfigFile:  filepath.Join(support, "config.toml"),
		StateFile:   filepath.Join(support, "state.json"),
		RuntimeFile: filepath.Join(support, "runtime.json"),
		CacheDir:    filepath.Join("/Users/ada", "Library", "Caches", "shunt"),
		LogDir:      filepath.Join("/Users/ada", "Library", "Logs", "shunt"),
		BackupDir:   support,
	}
	if paths != want {
		t.Fatalf("paths = %+v, want %+v", paths, want)
	}
}

func TestDefaultPathsLinuxXDG(t *testing.T) {
	paths, err := defaultPathsFor("linux", "/home/ada", env(map[string]string{
		"XDG_CONFIG_HOME": "/xdg/config",
		"XDG_STATE_HOME":  "/xdg/state",
		"XDG_CACHE_HOME":  "/xdg/cache",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		ConfigFile:  filepath.Join("/xdg/config", "shunt", "config.toml"),
		StateFile:   filepath.Join("/xdg/state", "shunt", "state.json"),
		RuntimeFile: filepath.Join("/xdg/state", "shunt", "runtime.json"),
		CacheDir:    filepath.Join("/xdg/cache", "shunt"),
		LogDir:      filepath.Join("/xdg/state", "shunt", "logs"),
		BackupDir:   filepath.Join("/xdg/state", "shunt", "backups"),
	}
	if paths != want {
		t.Fatalf("paths = %+v, want %+v", paths, want)
	}
}

func TestDefaultPathsLinuxFallback(t *testing.T) {
	paths, err := defaultPathsFor("freebsd", "/home/ada", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		ConfigFile:  filepath.Join("/home/ada", ".config", "shunt", "config.toml"),
		StateFile:   filepath.Join("/home/ada", ".local", "state", "shunt", "state.json"),
		RuntimeFile: filepath.Join("/home/ada", ".local", "state", "shunt", "runtime.json"),
		CacheDir:    filepath.Join("/home/ada", ".cache", "shunt"),
		LogDir:      filepath.Join("/home/ada", ".local", "state", "shunt", "logs"),
		BackupDir:   filepath.Join("/home/ada", ".local", "state", "shunt", "backups"),
	}
	if paths != want {
		t.Fatalf("paths = %+v, want %+v", paths, want)
	}
}

func TestDefaultPathsIgnoresForeignVariables(t *testing.T) {
	// APPDATA must not leak into a Unix layout and XDG must not leak into macOS.
	paths, err := defaultPathsFor("darwin", "/Users/ada", env(map[string]string{
		"APPDATA":         "/appdata",
		"XDG_CONFIG_HOME": "/xdg/config",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if paths.ConfigFile != filepath.Join("/Users/ada", "Library", "Application Support", "shunt", "config.toml") {
		t.Fatalf("config = %q", paths.ConfigFile)
	}
}
