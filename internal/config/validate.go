package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

var profileIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidProfileID reports whether an id matches the profile ID grammar. Listener
// names use the same grammar.
func ValidProfileID(id string) bool { return profileIDPattern.MatchString(id) }

// Validate checks the entire configuration and returns every problem it finds.
func Validate(cfg *Config) error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	if cfg.Schema != 1 {
		add("schema must be 1, got %d", cfg.Schema)
	}
	if cfg.Relay.StatePollMS < 50 || cfg.Relay.StatePollMS > 2000 {
		add("relay.state_poll_ms must be 50..2000")
	}
	if cfg.Relay.ConfigPollMS < 200 || cfg.Relay.ConfigPollMS > 10000 {
		add("relay.config_poll_ms must be 200..10000")
	}
	if cfg.Relay.MaxInflight < 1 || cfg.Relay.MaxInflight > 1024 {
		add("relay.max_inflight must be 1..1024")
	}
	if cfg.Relay.ConnectTimeoutMS <= 0 {
		add("relay.connect_timeout_ms must be greater than zero")
	}
	if cfg.Relay.ResponseHeaderTimeoutMS < 1000 || cfg.Relay.ResponseHeaderTimeoutMS > 600000 {
		add("relay.response_header_timeout_ms must be 1000..600000")
	}
	if cfg.Relay.BodyIdleTimeoutMS != 0 {
		add("relay.body_idle_timeout_ms must be 0 (body timeouts are disabled)")
	}
	if cfg.Relay.ShutdownGraceMS < 0 {
		add("relay.shutdown_grace_ms must not be negative")
	}
	if cfg.Relay.TCPKeepaliveS < 0 || cfg.Relay.TCPKeepaliveS > 3600 {
		add("relay.tcp_keepalive_s must be 0..3600")
	}
	switch strings.ToLower(cfg.Relay.LogLevel) {
	case "error", "warn", "info", "debug", "trace":
	default:
		add("relay.log_level must be error, warn, info, debug, or trace")
	}
	switch strings.ToLower(cfg.Relay.LogFormat) {
	case "text", "json":
	default:
		add("relay.log_format must be text or json")
	}
	if cfg.Relay.RequestIDHeader == "" {
		add("relay.request_id_header must not be empty")
	}
	if cfg.UI.MaxRequestBytes < 1024 || cfg.UI.MaxRequestBytes > 1048576 {
		add("ui.max_request_bytes must be 1024..1048576")
	}
	if cfg.Models.CacheTTLHours <= 0 {
		add("models.cache_ttl_hours must be positive")
	}
	if cfg.Models.StaleTTLHours <= cfg.Models.CacheTTLHours {
		add("models.stale_ttl_hours must be greater than cache_ttl_hours")
	}
	if cfg.Models.PageSize < 1 || cfg.Models.PageSize > 1000 {
		add("models.page_size must be 1..1000")
	}
	if cfg.Models.MaxPages < 1 || cfg.Models.MaxPages > 100 {
		add("models.max_pages must be 1..100")
	}
	if cfg.Models.DefaultDialect != "anthropic" && cfg.Models.DefaultDialect != "openai" {
		add("models.default_dialect must be anthropic or openai")
	}
	if err := ValidateListen(cfg.Relay.Listen, cfg.Relay.AllowNonLoopback); err != nil {
		add("%v", err)
	}
	if len(cfg.Profiles) == 0 {
		add("at least one profile is required")
	}
	enabled := map[string]bool{}
	for id, p := range cfg.Profiles {
		if !profileIDPattern.MatchString(id) {
			add("profile id %q must match ^[a-z][a-z0-9-]{0,31}$", id)
		}
		if err := validateProfile(id, p); err != nil {
			add("%v", err)
		}
		if p.IsEnabled() {
			enabled[id] = true
		}
	}
	if len(enabled) == 0 {
		add("at least one enabled profile is required")
	}
	if len(cfg.Listeners) > 0 {
		validateListeners(cfg, enabled, add)
	}
	if !enabled[cfg.DefaultProfile] {
		add("default_profile %q must name an enabled profile", cfg.DefaultProfile)
	}
	if !enabled[cfg.Switch.OnProfile] {
		add("switch.on_profile %q must name an enabled profile", cfg.Switch.OnProfile)
	}
	if !enabled[cfg.Switch.OffProfile] {
		add("switch.off_profile %q must name an enabled profile", cfg.Switch.OffProfile)
	}
	if !enabled[cfg.Auto.Preferred] {
		add("auto.preferred %q must name an enabled profile", cfg.Auto.Preferred)
	}
	if cfg.Auto.ProbeIntervalMS <= 0 || cfg.Auto.ProbeIntervalMS > 600000 {
		add("auto.probe_interval_ms must be 1..600000")
	}
	if cfg.Auto.ProbeTimeoutMS <= 0 || cfg.Auto.ProbeTimeoutMS > 60000 {
		add("auto.probe_timeout_ms must be 1..60000")
	}
	if cfg.Auto.FailureThreshold < 1 || cfg.Auto.FailureThreshold > 100 {
		add("auto.failure_threshold must be 1..100")
	}
	if cfg.Auto.RecoveryThreshold < 1 || cfg.Auto.RecoveryThreshold > 100 {
		add("auto.recovery_threshold must be 1..100")
	}
	for name, path := range map[string]string{
		"paths.state":      cfg.Paths.State,
		"paths.runtime":    cfg.Paths.Runtime,
		"paths.cache_dir":  cfg.Paths.CacheDir,
		"paths.log_dir":    cfg.Paths.LogDir,
		"paths.backup_dir": cfg.Paths.BackupDir,
	} {
		if path != "" && !filepath.IsAbs(path) && !strings.HasPrefix(path, "~") {
			add("%s must be absolute or start with ~", name)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("configuration invalid:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

// validateListeners checks the optional [[listeners]] table. Names and canonical
// addresses must be unique so every entry port is unambiguous.
func validateListeners(cfg *Config, enabledProfiles map[string]bool, add func(string, ...any)) {
	names := map[string]bool{}
	addrs := map[string]string{}
	enabledCount := 0
	for i, l := range cfg.Listeners {
		label := fmt.Sprintf("listeners[%d]", i)
		if !profileIDPattern.MatchString(l.Name) {
			add("%s.name %q must match ^[a-z][a-z0-9-]{0,31}$", label, l.Name)
		}
		if names[l.Name] {
			add("%s.name %q is duplicated", label, l.Name)
		}
		names[l.Name] = true
		if l.IsEnabled() {
			enabledCount++
		}
		if err := ValidateListen(l.Listen, cfg.Relay.AllowNonLoopback); err != nil {
			add("%s (%s): %v", label, l.Name, err)
			continue
		}
		if canonical, err := NormalizeListen(l.Listen); err == nil {
			if prev, ok := addrs[canonical]; ok {
				add("%s (%s) duplicates the address of listener %q: %s", label, l.Name, prev, canonical)
			}
			addrs[canonical] = l.Name
		}
		if l.Profile != "" && !enabledProfiles[l.Profile] {
			add("%s (%s): profile %q must name an enabled profile", label, l.Name, l.Profile)
		}
	}
	if enabledCount == 0 {
		add("at least one enabled listener is required")
	}
}

// ValidateInitURLs checks the two origins written by init. It runs before the
// file is created so a bad flag or environment value cannot leave a broken
// configuration behind.
func ValidateInitURLs(urls InitURLs) error {
	var problems []string
	check := func(name, raw string) {
		u, err := url.Parse(raw)
		if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil {
			problems = append(problems, name+" must be an absolute http(s) origin")
			return
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			problems = append(problems, name+" scheme must be http or https")
		}
		if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			problems = append(problems, name+" must not contain a path, query, or fragment")
		}
	}
	check("local URL", urls.Local)
	check("remote URL", urls.Remote)
	if len(problems) > 0 {
		return fmt.Errorf("invalid init endpoints:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

func validateProfile(id string, p Profile) error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("profile %q: %s", id, fmt.Sprintf(format, args...)))
	}
	base, err := url.Parse(p.BaseURL)
	if err != nil || !base.IsAbs() || base.Host == "" || base.User != nil {
		add("base_url must be an absolute http(s) origin")
	} else {
		if base.Scheme != "http" && base.Scheme != "https" {
			add("base_url scheme must be http or https")
		}
		if base.Path != "" && base.Path != "/" {
			add("base_url must not contain a path")
		}
		if base.RawQuery != "" || base.Fragment != "" {
			add("base_url must not contain a query or fragment")
		}
		if base.Scheme == "https" && !p.VerifyTLS() {
			add("tls_verify must be true for an https base_url")
		}
	}
	if !p.VerifyTLS() {
		add("tls_verify must be true; TLS verification cannot be disabled")
	}
	if p.ProbeURL == "" {
		add("probe_url must not be empty")
	} else {
		probe, err := url.Parse(p.ProbeURL)
		if err != nil || !probe.IsAbs() || probe.Host == "" {
			add("probe_url must be an absolute URL")
		} else if base != nil && base.IsAbs() {
			if !strings.EqualFold(probe.Scheme, base.Scheme) || !strings.EqualFold(probe.Host, base.Host) {
				add("probe_url must use the same origin as base_url")
			}
		}
	}
	if p.ProbeMethod != "GET" && p.ProbeMethod != "HEAD" {
		add("probe_method must be GET or HEAD")
	}
	if p.ConnectTimeoutMS <= 0 {
		add("connect_timeout_ms must be greater than zero")
	}
	if p.ResponseHeaderTimeoutMS < 1000 || p.ResponseHeaderTimeoutMS > 600000 {
		add("response_header_timeout_ms must be 1000..600000")
	}
	if p.Priority < 0 {
		add("priority must not be negative")
	}
	if strings.TrimSpace(p.ProxyURL) != "" {
		proxy, err := url.Parse(p.ProxyURL)
		if err != nil || !proxy.IsAbs() || proxy.Host == "" {
			add("proxy_url must be an absolute http(s) URL")
		} else if proxy.Scheme != "http" && proxy.Scheme != "https" {
			add("proxy_url scheme must be http or https")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}
