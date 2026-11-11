package config

import (
	"sort"

	"github.com/pelletier/go-toml/v2"
)

var topLevelKeys = map[string]bool{
	"schema":          true,
	"default_profile": true,
	"relay":           true,
	"switch":          true,
	"auto":            true,
	"profiles":        true,
	"ui":              true,
	"paths":           true,
	"models":          true,
	"clients":         true,
}

var relayKeys = map[string]bool{
	"listen":                     true,
	"allow_non_loopback":         true,
	"state_poll_ms":              true,
	"config_poll_ms":             true,
	"max_inflight":               true,
	"connect_timeout_ms":         true,
	"response_header_timeout_ms": true,
	"body_idle_timeout_ms":       true,
	"shutdown_grace_ms":          true,
	"tcp_keepalive_s":            true,
	"trace_headers":              true,
	"log_level":                  true,
	"log_format":                 true,
	"request_id_header":          true,
}

var switchKeys = map[string]bool{
	"on_profile":  true,
	"off_profile": true,
}

var autoKeys = map[string]bool{
	"enabled":            true,
	"preferred":          true,
	"probe_interval_ms":  true,
	"probe_timeout_ms":   true,
	"failure_threshold":  true,
	"recovery_threshold": true,
}

var profileKeys = map[string]bool{
	"label":                      true,
	"base_url":                   true,
	"probe_url":                  true,
	"probe_method":               true,
	"connect_timeout_ms":         true,
	"response_header_timeout_ms": true,
	"tls_verify":                 true,
	"priority":                   true,
	"enabled":                    true,
	"tags":                       true,
}

var uiKeys = map[string]bool{
	"enabled":           true,
	"open_browser":      true,
	"max_request_bytes": true,
}

var pathsKeys = map[string]bool{
	"state":      true,
	"runtime":    true,
	"cache_dir":  true,
	"log_dir":    true,
	"backup_dir": true,
}

var modelsKeys = map[string]bool{
	"cache_ttl_hours": true,
	"stale_ttl_hours": true,
	"page_size":       true,
	"max_pages":       true,
	"default_dialect": true,
}

var clientsKeys = map[string]bool{
	"claude": true,
	"codex":  true,
	"dsh":    true,
}

var clientSections = map[string]map[string]bool{
	"claude": {"settings_path": true, "base_url_mode": true},
	"codex":  {"config_path": true, "base_url_mode": true},
	"dsh":    {"home": true, "profile": true, "write_supported": true},
}

// FindUnknownKey reports the first unknown key in a TOML document with a
// stable, location-qualified name. The strict decoder only says that fields are
// missing, which is not actionable; this walker names the typo.
func FindUnknownKey(data []byte) (string, bool) {
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return "", false
	}
	if key, bad := unknownIn(doc, topLevelKeys, ""); bad {
		return key, true
	}
	sections := []struct {
		name    string
		allowed map[string]bool
	}{
		{"relay", relayKeys},
		{"switch", switchKeys},
		{"auto", autoKeys},
		{"ui", uiKeys},
		{"paths", pathsKeys},
		{"models", modelsKeys},
	}
	for _, section := range sections {
		if raw, ok := doc[section.name].(map[string]any); ok {
			if key, bad := unknownIn(raw, section.allowed, section.name+"."); bad {
				return key, true
			}
		}
	}
	if raw, ok := doc["profiles"].(map[string]any); ok {
		for _, id := range sortedKeys(raw) {
			profile, ok := raw[id].(map[string]any)
			if !ok {
				continue
			}
			if key, bad := unknownIn(profile, profileKeys, "profiles."+id+"."); bad {
				return key, true
			}
		}
	}
	if raw, ok := doc["clients"].(map[string]any); ok {
		if key, bad := unknownIn(raw, clientsKeys, "clients."); bad {
			return key, true
		}
		for name, allowed := range clientSections {
			if section, ok := raw[name].(map[string]any); ok {
				if key, bad := unknownIn(section, allowed, "clients."+name+"."); bad {
					return key, true
				}
			}
		}
	}
	return "", false
}

func unknownIn(m map[string]any, allowed map[string]bool, prefix string) (string, bool) {
	for _, key := range sortedKeys(m) {
		if !allowed[key] {
			return prefix + key, true
		}
	}
	return "", false
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
