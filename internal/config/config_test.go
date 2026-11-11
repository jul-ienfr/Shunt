package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestDefaultsAreValid(t *testing.T) {
	cfg := Defaults()
	if err := Validate(&cfg); err != nil {
		t.Fatalf("default config must validate: %v", err)
	}
	if cfg.Relay.Listen != "127.0.0.1:8787" {
		t.Fatalf("default listen = %q", cfg.Relay.Listen)
	}
	if len(cfg.Profiles) != 2 {
		t.Fatalf("default profiles = %d, want 2", len(cfg.Profiles))
	}
}

func TestLoadMinimalTestdata(t *testing.T) {
	cfg, path, err := Load(filepath.Join("testdata", "minimal.toml"))
	if err != nil {
		t.Fatalf("load minimal config: %v", err)
	}
	if !strings.HasSuffix(path, "minimal.toml") {
		t.Fatalf("path = %q", path)
	}
	if cfg.Relay.StatePollMS != 200 || cfg.Relay.MaxInflight != 64 {
		t.Fatalf("defaults not applied: %+v", cfg.Relay)
	}
	if cfg.Profiles["local"].ProbeURL != "http://127.0.0.1:4000/" {
		t.Fatalf("probe_url not derived: %q", cfg.Profiles["local"].ProbeURL)
	}
	if cfg.Models.CacheTTLHours != 12 {
		t.Fatalf("models defaults not applied: %+v", cfg.Models)
	}
}

func TestLoadReplacesDefaultProfileSet(t *testing.T) {
	path := writeTempConfig(t, `
schema = 1
default_profile = "local"
[switch]
on_profile = "remote"
off_profile = "local"
[auto]
preferred = "local"
[profiles.local]
base_url = "http://127.0.0.1:4100"
[profiles.remote]
base_url = "https://example.test"
`)
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Profiles) != 2 {
		t.Fatalf("profiles leaked from defaults: %v", cfg.Profiles)
	}
	if got := cfg.Profiles["local"].ProbeURL; got != "http://127.0.0.1:4100/" {
		t.Fatalf("local probe_url = %q", got)
	}
	if got := cfg.Profiles["remote"].ProbeURL; got != "https://example.test/" {
		t.Fatalf("remote probe_url = %q", got)
	}
	if cfg.Profiles["local"].Priority != 100 {
		t.Fatalf("profile priority default = %d", cfg.Profiles["local"].Priority)
	}
}

func TestLoadUnknownTopLevelKey(t *testing.T) {
	_, _, err := Load(filepath.Join("testdata", "unknown_key.toml"))
	if err == nil {
		t.Fatal("expected unknown key rejection")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("error does not name the key: %v", err)
	}
}

func TestLoadRejectsSchema(t *testing.T) {
	path := writeTempConfig(t, "schema = 2\n")
	if _, _, err := Load(path); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("expected schema error, got %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, _, err := Load(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Fatal("expected missing file error")
	}
}

func TestSaveDefaultRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SaveDefault(path, false); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := SaveDefault(path, false); err == nil {
		t.Fatal("expected refusal to overwrite")
	}
	if err := SaveDefault(path, true); err != nil {
		t.Fatalf("forced overwrite: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `listen = "127.0.0.1:8787"`) {
		t.Fatalf("unexpected saved config: %s", data)
	}
}
