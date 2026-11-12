package relay

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"shunt/internal/config"
	"shunt/internal/platform"
	"shunt/internal/state"
)

// AddProfile creates an upstream profile and hot-applies it. The new profile is
// not probed: an upstream that is temporarily down can still be configured, and
// the health tracker reports it as unreachable.
func (s *Server) AddProfile(id, label, scheme, host string, port int) (string, error) {
	id = strings.TrimSpace(id)
	if !config.ValidProfileID(id) {
		return "", &ApplyError{Code: CodeInvalidEndpoint, Field: "id", Message: "profile id must match ^[a-z][a-z0-9-]{0,31}$"}
	}
	if _, exists := s.Config().Profiles[id]; exists {
		return "", &ApplyError{Code: CodeInvalidEndpoint, Field: "id", Message: fmt.Sprintf("profile %q already exists", id)}
	}
	origin, err := profileOrigin(scheme, host, port)
	if err != nil {
		return "", err
	}
	if _, err := s.applyConfigEdit(CodeInvalidEndpoint, "host", func(text string) (string, error) {
		return config.AddProfile(text, id, strings.TrimSpace(label), origin, origin+"/")
	}); err != nil {
		return "", err
	}
	s.logger.Info("gui added profile", "profile", id, "base_url", origin)
	return origin, nil
}

// RemoveProfile deletes an unused profile. A profile pinned by an entry port is
// refused; references from the default, the switch aliases, auto mode, and the
// current intent are retargeted to another enabled profile so the configuration
// stays valid and routing never silently points at a missing profile.
func (s *Server) RemoveProfile(id string) error {
	cfg := s.Config()
	removed, ok := cfg.Profiles[id]
	if !ok {
		return &ApplyError{Code: CodeProfileNotFound, Field: "profile", Message: fmt.Sprintf("profile %q does not exist", id)}
	}
	for _, listener := range cfg.Listeners {
		if listener.Profile == id {
			return &ApplyError{
				Code:    CodeProfileInUse,
				Field:   "profile",
				Message: fmt.Sprintf("profile %q is still used by listener %s", id, listener.Name),
			}
		}
	}
	replacement := ""
	for _, candidate := range []string{cfg.DefaultProfile, s.selector.Load().Profile} {
		if candidate != id && profileEnabled(cfg, candidate) {
			replacement = candidate
			break
		}
	}
	if replacement == "" {
		for _, candidate := range cfg.EnabledProfileIDs() {
			if candidate != id {
				replacement = candidate
				break
			}
		}
	}
	if replacement == "" {
		return &ApplyError{Code: CodeProfileInUse, Field: "profile", Message: "cannot remove the last enabled profile"}
	}
	if _, err := s.applyConfigEdit(CodeInvalidEndpoint, "profile", func(text string) (string, error) {
		out, err := config.RemoveProfile(text, id)
		if err != nil {
			return "", err
		}
		if cfg.DefaultProfile == id {
			out, err = config.SetTopLevel(out, "default_profile", config.TOMLString(replacement))
			if err != nil {
				return "", err
			}
		}
		for _, edit := range []struct {
			section string
			key     string
			value   string
		}{
			{section: "switch", key: "on_profile", value: cfg.Switch.OnProfile},
			{section: "switch", key: "off_profile", value: cfg.Switch.OffProfile},
			{section: "auto", key: "preferred", value: cfg.Auto.Preferred},
		} {
			if edit.value != id {
				continue
			}
			out, err = config.EditValue(out, edit.section, edit.key, config.TOMLString(replacement))
			if err != nil {
				return "", err
			}
		}
		return out, nil
	}); err != nil {
		return err
	}

	// The profile may also be named by state.json. Retarget it so startup does
	// not warn about a missing profile on the next run.
	intent := s.State()
	changed := false
	if intent.Selected != nil && *intent.Selected == id {
		intent.Selected = &replacement
		changed = true
	}
	if intent.Preferred != nil && *intent.Preferred == id {
		intent.Preferred = &replacement
		changed = true
	}
	if changed {
		intent.UpdatedBy = "gui"
		intent.Reason = "profile removed"
		saved, saveErr := state.Save(s.StatePath, intent)
		if saveErr != nil {
			s.logger.Warn("could not retarget state after profile removal", "profile", id, "replacement", replacement, "error", saveErr)
		} else {
			s.applyIntent(saved, saved.Revision, "gui")
		}
	}
	s.logger.Info("gui removed profile", "profile", id, "replacement", replacement, "disabled", !removed.IsEnabled())
	return nil
}

func profileEnabled(cfg config.Config, id string) bool {
	p, ok := cfg.Profiles[id]
	return ok && p.IsEnabled()
}

// AddListener creates another entry port, materializing the legacy [relay]
// listen entry first so adding a port never drops the current one.
func (s *Server) AddListener(name, ip string, port int, profile string, confirmLAN bool) (string, error) {
	name = strings.TrimSpace(name)
	if !config.ValidProfileID(name) {
		return "", &ApplyError{Code: CodeInvalidEntry, Field: "name", Message: "listener name must match ^[a-z][a-z0-9-]{0,31}$"}
	}
	addr, loopback, err := listenerAddr(ip, port, confirmLAN)
	if err != nil {
		return "", err
	}
	profile = strings.TrimSpace(profile)
	cfg := s.Config()
	if profile != "" {
		p, ok := cfg.Profiles[profile]
		if !ok {
			return "", &ApplyError{Code: CodeProfileNotFound, Field: "profile", Message: fmt.Sprintf("profile %q does not exist", profile)}
		}
		if !p.IsEnabled() {
			return "", &ApplyError{Code: CodeProfileDisabled, Field: "profile", Message: fmt.Sprintf("profile %q is disabled", profile)}
		}
	}
	for _, listener := range cfg.Listeners {
		if listener.Name == name {
			return "", &ApplyError{Code: CodeInvalidEntry, Field: "name", Message: fmt.Sprintf("listener %q already exists", name)}
		}
	}
	if _, err := s.applyConfigEdit(CodeInvalidEntry, "listen", func(text string) (string, error) {
		out := text
		var editErr error
		if !cfg.ExplicitListeners() && name != "default" {
			out, editErr = config.AddListener(out, "default", cfg.Relay.Listen, "")
			if editErr != nil {
				return "", editErr
			}
		}
		out, editErr = config.AddListener(out, name, addr, profile)
		if editErr != nil {
			return "", editErr
		}
		if !loopback {
			out, editErr = config.SetAllowNonLoopback(out, true)
		}
		return out, editErr
	}); err != nil {
		return "", err
	}
	s.logger.Info("gui added listener", "listener", name, "listen", addr, "profile", profile)
	return addr, nil
}

// RemoveListener deletes one named entry port. The last enabled port cannot be
// removed, and the legacy single entry is edited from the entry form instead.
func (s *Server) RemoveListener(name string) error {
	cfg := s.Config()
	if !cfg.ExplicitListeners() {
		return &ApplyError{Code: CodeInvalidEntry, Field: "name", Message: "the legacy [relay] listen entry cannot be removed; change it with the entry form"}
	}
	found := false
	enabled := 0
	for _, listener := range cfg.Listeners {
		if listener.Name == name {
			found = true
		}
		if listener.IsEnabled() {
			enabled++
		}
	}
	if !found {
		return &ApplyError{Code: CodeListenerNotFound, Field: "name", Message: fmt.Sprintf("listener %q does not exist", name)}
	}
	if enabled <= 1 {
		return &ApplyError{Code: CodeInvalidEntry, Field: "name", Message: "cannot remove the last entry port"}
	}
	if _, err := s.applyConfigEdit(CodeInvalidEntry, "name", func(text string) (string, error) {
		return config.RemoveListener(text, name)
	}); err != nil {
		return err
	}
	s.logger.Info("gui removed listener", "listener", name)
	return nil
}

// SetListenerAddr moves one named entry port. The new socket is bound before
// the old one is retired by the listener set.
func (s *Server) SetListenerAddr(name, ip string, port int, confirmLAN bool) (string, error) {
	cfg := s.Config()
	if !cfg.ExplicitListeners() {
		return "", &ApplyError{Code: CodeInvalidEntry, Field: "name", Message: "the legacy entry is changed with the entry form"}
	}
	found := false
	for _, listener := range cfg.Listeners {
		if listener.Name == name {
			found = true
			break
		}
	}
	if !found {
		return "", &ApplyError{Code: CodeListenerNotFound, Field: "name", Message: fmt.Sprintf("listener %q does not exist", name)}
	}
	addr, loopback, err := listenerAddr(ip, port, confirmLAN)
	if err != nil {
		return "", err
	}
	if _, err := s.applyConfigEdit(CodeInvalidEntry, "listen", func(text string) (string, error) {
		out, err := config.SetListenerListen(text, name, addr)
		if err != nil {
			return "", err
		}
		if !loopback {
			out, err = config.SetAllowNonLoopback(out, true)
		}
		return out, err
	}); err != nil {
		return "", err
	}
	s.logger.Info("gui moved listener", "listener", name, "listen", addr)
	return addr, nil
}

// applyConfigEdit reads the file, applies one edit, validates the whole
// document, writes it atomically, and hot-applies it. A bind failure rolls the
// file back so the next watcher pass cannot retry a topology that does not fit
// the running process.
func (s *Server) applyConfigEdit(code, field string, edit func(string) (string, error)) (config.Config, error) {
	if s.ConfigPath == "" {
		return config.Config{}, &ApplyError{Code: CodeConfigWriteFailed, Message: "no configuration path"}
	}
	original, err := os.ReadFile(s.ConfigPath)
	if err != nil {
		return config.Config{}, &ApplyError{Code: CodeConfigWriteFailed, Message: err.Error(), Err: err}
	}
	out, err := edit(string(original))
	if err != nil {
		return config.Config{}, &ApplyError{Code: code, Field: field, Message: err.Error(), Err: err}
	}
	cfg, err := config.Parse([]byte(out))
	if err != nil {
		return config.Config{}, &ApplyError{Code: code, Field: field, Message: err.Error(), Err: err}
	}
	if err := platform.WriteFileAtomic(s.ConfigPath, []byte(out), 0o600); err != nil {
		return config.Config{}, &ApplyError{Code: CodeConfigWriteFailed, Message: err.Error(), Err: err}
	}
	if err := s.applyConfig(cfg); err != nil {
		rollbackErr := platform.WriteFileAtomic(s.ConfigPath, original, 0o600)
		if rollbackErr != nil {
			s.logger.Error("configuration rollback failed", "error", rollbackErr)
		}
		return config.Config{}, &ApplyError{Code: CodeBindFailed, Field: field, Message: err.Error(), Err: err}
	}
	return cfg, nil
}

// listenerAddr validates an IP and port and returns the canonical address.
// loopback reports whether the address is loopback, so the caller knows whether
// allow_non_loopback must be enabled.
func listenerAddr(ip string, port int, confirmLAN bool) (addr string, loopback bool, err error) {
	if port < 1024 || port > 65535 {
		return "", false, &ApplyError{Code: CodeInvalidEntry, Field: "port", Message: "port must be 1024..65535"}
	}
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return "", false, &ApplyError{Code: CodeInvalidEntry, Field: "ip", Message: "ip must be an IP address"}
	}
	if parsed.IsUnspecified() {
		return "", false, &ApplyError{Code: CodeInvalidEntry, Field: "ip", Message: "wildcard binds are not allowed"}
	}
	loopback = parsed.IsLoopback()
	if !loopback {
		if !config.IsPrivateIP(parsed) {
			return "", false, &ApplyError{Code: CodeInvalidEntry, Field: "ip", Message: "only loopback or private local addresses are allowed"}
		}
		if !confirmLAN {
			return "", false, &ApplyError{Code: CodeLANNotConfirmed, Field: "ip", Message: "a non-loopback bind requires confirm_lan: true"}
		}
	}
	addr = net.JoinHostPort(parsed.String(), strconv.Itoa(port))
	if err := config.ValidateListen(addr, true); err != nil {
		return "", false, &ApplyError{Code: CodeInvalidEntry, Field: "ip", Message: err.Error(), Err: err}
	}
	return addr, loopback, nil
}

// profileOrigin validates the scheme, host, and port of a profile endpoint.
func profileOrigin(scheme, host string, port int) (string, error) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme != "http" && scheme != "https" {
		return "", &ApplyError{Code: CodeInvalidEndpoint, Field: "scheme", Message: "scheme must be http or https"}
	}
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, "/?#@") {
		return "", &ApplyError{Code: CodeInvalidEndpoint, Field: "host", Message: "host must be a DNS name or IP literal"}
	}
	if port == 0 {
		if scheme == "https" {
			port = 443
		} else {
			port = 80
		}
	}
	if port < 1 || port > 65535 {
		return "", &ApplyError{Code: CodeInvalidEndpoint, Field: "port", Message: "port must be 1..65535"}
	}
	origin := scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port))
	if _, err := url.Parse(origin); err != nil {
		return "", &ApplyError{Code: CodeInvalidEndpoint, Field: "host", Message: err.Error(), Err: err}
	}
	return origin, nil
}
