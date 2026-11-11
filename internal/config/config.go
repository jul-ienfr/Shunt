package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"shunt/internal/platform"
)

// Config is the complete on-disk configuration. Every field is optional in the
// file; Defaults fills the gaps before validation.
type Config struct {
	Schema         int                `toml:"schema"`
	DefaultProfile string             `toml:"default_profile"`
	Relay          Relay              `toml:"relay"`
	Switch         Switch             `toml:"switch"`
	Auto           Auto               `toml:"auto"`
	Profiles       map[string]Profile `toml:"profiles"`
	UI             UI                 `toml:"ui"`
	Paths          Paths              `toml:"paths"`
	Models         Models             `toml:"models"`
	Clients        Clients            `toml:"clients"`
}

type Relay struct {
	Listen                  string `toml:"listen"`
	AllowNonLoopback        bool   `toml:"allow_non_loopback"`
	StatePollMS             int    `toml:"state_poll_ms"`
	ConfigPollMS            int    `toml:"config_poll_ms"`
	MaxInflight             int    `toml:"max_inflight"`
	ConnectTimeoutMS        int    `toml:"connect_timeout_ms"`
	ResponseHeaderTimeoutMS int    `toml:"response_header_timeout_ms"`
	BodyIdleTimeoutMS       int    `toml:"body_idle_timeout_ms"`
	ShutdownGraceMS         int    `toml:"shutdown_grace_ms"`
	TCPKeepaliveS           int    `toml:"tcp_keepalive_s"`
	TraceHeaders            bool   `toml:"trace_headers"`
	LogLevel                string `toml:"log_level"`
	LogFormat               string `toml:"log_format"`
	RequestIDHeader         string `toml:"request_id_header"`
}

type Switch struct {
	OnProfile  string `toml:"on_profile"`
	OffProfile string `toml:"off_profile"`
}

type Auto struct {
	Enabled           bool   `toml:"enabled"`
	Preferred         string `toml:"preferred"`
	ProbeIntervalMS   int    `toml:"probe_interval_ms"`
	ProbeTimeoutMS    int    `toml:"probe_timeout_ms"`
	FailureThreshold  int    `toml:"failure_threshold"`
	RecoveryThreshold int    `toml:"recovery_threshold"`
}

type Profile struct {
	Label                   string   `toml:"label"`
	BaseURL                 string   `toml:"base_url"`
	ProbeURL                string   `toml:"probe_url"`
	ProbeMethod             string   `toml:"probe_method"`
	ConnectTimeoutMS        int      `toml:"connect_timeout_ms"`
	ResponseHeaderTimeoutMS int      `toml:"response_header_timeout_ms"`
	TLSVerify               *bool    `toml:"tls_verify"`
	Priority                int      `toml:"priority"`
	Enabled                 *bool    `toml:"enabled"`
	Tags                    []string `toml:"tags"`
}

func (p Profile) IsEnabled() bool { return p.Enabled == nil || *p.Enabled }
func (p Profile) VerifyTLS() bool { return p.TLSVerify == nil || *p.TLSVerify }

type UI struct {
	Enabled         bool `toml:"enabled"`
	OpenBrowser     bool `toml:"open_browser"`
	MaxRequestBytes int  `toml:"max_request_bytes"`
}

type Paths struct {
	State     string `toml:"state"`
	Runtime   string `toml:"runtime"`
	CacheDir  string `toml:"cache_dir"`
	LogDir    string `toml:"log_dir"`
	BackupDir string `toml:"backup_dir"`
}

type Models struct {
	CacheTTLHours  int    `toml:"cache_ttl_hours"`
	StaleTTLHours  int    `toml:"stale_ttl_hours"`
	PageSize       int    `toml:"page_size"`
	MaxPages       int    `toml:"max_pages"`
	DefaultDialect string `toml:"default_dialect"`
}

type Clients struct {
	Claude ClientClaude `toml:"claude"`
	Codex  ClientCodex  `toml:"codex"`
	DSH    ClientDSH    `toml:"dsh"`
}

type ClientClaude struct {
	SettingsPath string `toml:"settings_path"`
	BaseURLMode  string `toml:"base_url_mode"`
}

type ClientCodex struct {
	ConfigPath  string `toml:"config_path"`
	BaseURLMode string `toml:"base_url_mode"`
}

type ClientDSH struct {
	Home           string `toml:"home"`
	Profile        string `toml:"profile"`
	WriteSupported bool   `toml:"write_supported"`
}

// Defaults returns the documented starting configuration.
func Defaults() Config {
	yes := true
	return Config{
		Schema:         1,
		DefaultProfile: "local",
		Relay: Relay{
			Listen:                  "127.0.0.1:8787",
			StatePollMS:             200,
			ConfigPollMS:            1000,
			MaxInflight:             64,
			ConnectTimeoutMS:        2000,
			ResponseHeaderTimeoutMS: 120000,
			BodyIdleTimeoutMS:       0,
			ShutdownGraceMS:         10000,
			TCPKeepaliveS:           30,
			LogLevel:                "info",
			LogFormat:               "text",
			RequestIDHeader:         "X-Shunt-Request-Id",
		},
		Switch: Switch{OnProfile: "remote", OffProfile: "local"},
		Auto: Auto{
			Enabled:           false,
			Preferred:         "local",
			ProbeIntervalMS:   10000,
			ProbeTimeoutMS:    2000,
			FailureThreshold:  3,
			RecoveryThreshold: 2,
		},
		Profiles: map[string]Profile{
			"local": {
				Label:                   "Local proxy",
				BaseURL:                 "http://127.0.0.1:4000",
				ProbeURL:                "http://127.0.0.1:4000/",
				ProbeMethod:             "GET",
				ConnectTimeoutMS:        500,
				ResponseHeaderTimeoutMS: 120000,
				Priority:                10,
				Enabled:                 &yes,
				TLSVerify:               &yes,
				Tags:                    []string{"local"},
			},
			"remote": {
				Label:                   "Tailnet MSI",
				BaseURL:                 "https://msi.tailc56568.ts.net",
				ProbeURL:                "https://msi.tailc56568.ts.net/",
				ProbeMethod:             "GET",
				ConnectTimeoutMS:        2000,
				ResponseHeaderTimeoutMS: 120000,
				Priority:                20,
				Enabled:                 &yes,
				TLSVerify:               &yes,
				Tags:                    []string{"tailnet"},
			},
		},
		UI: UI{Enabled: true, MaxRequestBytes: 65536},
		Models: Models{
			CacheTTLHours:  12,
			StaleTTLHours:  168,
			PageSize:       1000,
			MaxPages:       10,
			DefaultDialect: "anthropic",
		},
		Clients: Clients{
			Claude: ClientClaude{SettingsPath: "~/.claude/settings.json", BaseURLMode: "origin"},
			Codex:  ClientCodex{ConfigPath: "~/.codex/config.toml", BaseURLMode: "v1"},
			DSH:    ClientDSH{Home: "~/.dsh", Profile: "web", WriteSupported: false},
		},
	}
}

// Load reads and validates a configuration file. An empty path uses the
// platform default.
func Load(path string) (Config, string, error) {
	if path == "" {
		paths, err := platform.DefaultPaths()
		if err != nil {
			return Config{}, "", err
		}
		path = paths.ConfigFile
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, path, fmt.Errorf("read config %s: %w", path, err)
	}
	if key, unknown := FindUnknownKey(data); unknown {
		return Config{}, path, fmt.Errorf("parse config %s: unknown key %s", path, key)
	}
	cfg := Defaults()
	var presence map[string]any
	if err := toml.Unmarshal(data, &presence); err == nil {
		if _, ok := presence["profiles"]; ok {
			// A file that declares profiles owns the whole set; start empty so
			// missing per-profile keys receive defaults instead of leaking the
			// built-in local/remote definitions into the operator's map.
			cfg.Profiles = nil
		}
	}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, path, fmt.Errorf("parse config %s: %w", path, err)
	}
	ApplyDefaults(&cfg)
	if err := Validate(&cfg); err != nil {
		return Config{}, path, fmt.Errorf("validate config %s: %w", path, err)
	}
	return cfg, path, nil
}

// ApplyDefaults fills zero values left by a partial configuration file.
func ApplyDefaults(cfg *Config) {
	if cfg.Schema == 0 {
		cfg.Schema = 1
	}
	if cfg.Relay.Listen == "" {
		cfg.Relay.Listen = "127.0.0.1:8787"
	}
	if cfg.Relay.StatePollMS == 0 {
		cfg.Relay.StatePollMS = 200
	}
	if cfg.Relay.ConfigPollMS == 0 {
		cfg.Relay.ConfigPollMS = 1000
	}
	if cfg.Relay.MaxInflight == 0 {
		cfg.Relay.MaxInflight = 64
	}
	if cfg.Relay.ConnectTimeoutMS == 0 {
		cfg.Relay.ConnectTimeoutMS = 2000
	}
	if cfg.Relay.ResponseHeaderTimeoutMS == 0 {
		cfg.Relay.ResponseHeaderTimeoutMS = 120000
	}
	if cfg.Relay.ShutdownGraceMS == 0 {
		cfg.Relay.ShutdownGraceMS = 10000
	}
	if cfg.Relay.RequestIDHeader == "" {
		cfg.Relay.RequestIDHeader = "X-Shunt-Request-Id"
	}
	if cfg.Relay.LogLevel == "" {
		cfg.Relay.LogLevel = "info"
	}
	if cfg.Relay.LogFormat == "" {
		cfg.Relay.LogFormat = "text"
	}
	if cfg.Switch.OnProfile == "" {
		cfg.Switch.OnProfile = "remote"
	}
	if cfg.Switch.OffProfile == "" {
		cfg.Switch.OffProfile = "local"
	}
	if cfg.Auto.ProbeIntervalMS == 0 {
		cfg.Auto.ProbeIntervalMS = 10000
	}
	if cfg.Auto.ProbeTimeoutMS == 0 {
		cfg.Auto.ProbeTimeoutMS = 2000
	}
	if cfg.Auto.FailureThreshold == 0 {
		cfg.Auto.FailureThreshold = 3
	}
	if cfg.Auto.RecoveryThreshold == 0 {
		cfg.Auto.RecoveryThreshold = 2
	}
	if cfg.DefaultProfile == "" {
		cfg.DefaultProfile = "local"
	}
	if cfg.Auto.Preferred == "" {
		cfg.Auto.Preferred = cfg.DefaultProfile
	}
	if cfg.UI.MaxRequestBytes == 0 {
		cfg.UI.MaxRequestBytes = 65536
	}
	if cfg.Models.CacheTTLHours == 0 {
		cfg.Models.CacheTTLHours = 12
	}
	if cfg.Models.StaleTTLHours == 0 {
		cfg.Models.StaleTTLHours = 168
	}
	if cfg.Models.PageSize == 0 {
		cfg.Models.PageSize = 1000
	}
	if cfg.Models.MaxPages == 0 {
		cfg.Models.MaxPages = 10
	}
	if cfg.Models.DefaultDialect == "" {
		cfg.Models.DefaultDialect = "anthropic"
	}
	if cfg.Profiles == nil {
		cfg.Profiles = Defaults().Profiles
	}
	yes := true
	for id, p := range cfg.Profiles {
		if p.Label == "" {
			p.Label = id
		}
		if p.Priority == 0 {
			p.Priority = 100
		}
		if p.ProbeMethod == "" {
			p.ProbeMethod = "GET"
		}
		if p.ConnectTimeoutMS == 0 {
			p.ConnectTimeoutMS = cfg.Relay.ConnectTimeoutMS
		}
		if p.ResponseHeaderTimeoutMS == 0 {
			p.ResponseHeaderTimeoutMS = cfg.Relay.ResponseHeaderTimeoutMS
		}
		if p.Enabled == nil {
			p.Enabled = &yes
		}
		if p.TLSVerify == nil {
			p.TLSVerify = &yes
		}
		if p.ProbeURL == "" && p.BaseURL != "" {
			p.ProbeURL = strings.TrimRight(p.BaseURL, "/") + "/"
		}
		cfg.Profiles[id] = p
	}
}

// Profile returns the named profile and whether it exists.
func (c Config) Profile(id string) (Profile, bool) {
	p, ok := c.Profiles[id]
	return p, ok
}

// EnabledProfileIDs returns the enabled profile IDs in ascending order.
func (c Config) EnabledProfileIDs() []string {
	ids := make([]string, 0, len(c.Profiles))
	for id, p := range c.Profiles {
		if p.IsEnabled() {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// SaveDefault writes the commented default configuration. It refuses to
// overwrite an existing file unless force is true.
func SaveDefault(path string, force bool) error {
	if path == "" {
		paths, err := platform.DefaultPaths()
		if err != nil {
			return err
		}
		path = paths.ConfigFile
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config already exists at %s (use --force to overwrite)", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return platform.WriteFileAtomic(path, []byte(DefaultTOML), 0o600)
}

const DefaultTOML = `schema = 1
default_profile = "local"

[relay]
listen = "127.0.0.1:8787"
allow_non_loopback = false
state_poll_ms = 200
config_poll_ms = 1000
max_inflight = 64
connect_timeout_ms = 2000
response_header_timeout_ms = 120000
body_idle_timeout_ms = 0
shutdown_grace_ms = 10000
tcp_keepalive_s = 30
trace_headers = false
log_level = "info"
log_format = "text"
request_id_header = "X-Shunt-Request-Id"

[switch]
on_profile = "remote"
off_profile = "local"

[auto]
enabled = false
preferred = "local"
probe_interval_ms = 10000
probe_timeout_ms = 2000
failure_threshold = 3
recovery_threshold = 2

[profiles.local]
label = "Local proxy"
base_url = "http://127.0.0.1:4000"
probe_url = "http://127.0.0.1:4000/"
probe_method = "GET"
connect_timeout_ms = 500
priority = 10
enabled = true
tags = ["local"]

[profiles.remote]
label = "Tailnet MSI"
base_url = "https://msi.tailc56568.ts.net"
probe_url = "https://msi.tailc56568.ts.net/"
probe_method = "GET"
connect_timeout_ms = 2000
tls_verify = true
priority = 20
enabled = true
tags = ["tailnet"]

[ui]
enabled = true
open_browser = false
max_request_bytes = 65536

[paths]
state = ""
runtime = ""
cache_dir = ""
log_dir = ""
backup_dir = ""

[models]
cache_ttl_hours = 12
stale_ttl_hours = 168
page_size = 1000
max_pages = 10
default_dialect = "anthropic"

[clients.claude]
settings_path = "~/.claude/settings.json"
base_url_mode = "origin"

[clients.codex]
config_path = "~/.codex/config.toml"
base_url_mode = "v1"

[clients.dsh]
home = "~/.dsh"
profile = "web"
write_supported = false
`
