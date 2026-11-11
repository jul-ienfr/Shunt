package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"shunt/internal/config"
	"shunt/internal/platform"
	"shunt/internal/runtime"
	"shunt/internal/state"
	"shunt/internal/upstream"
)

// ApplyError is a structured error returned by control-plane mutations. The
// UI maps Code to its stable error document.
type ApplyError struct {
	Code    string
	Field   string
	Message string
	Current string
	Err     error
}

func (e *ApplyError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

func (e *ApplyError) Unwrap() error { return e.Err }

// Stable UI error codes.
const (
	CodeInvalidEntry      = "invalid_entry"
	CodeLANNotConfirmed   = "lan_not_confirmed"
	CodeBindFailed        = "bind_failed"
	CodeConfigWriteFailed = "config_write_failed"
	CodeInvalidEndpoint   = "invalid_endpoint"
	CodeProbeFailed       = "probe_failed"
	CodeProfileNotFound   = "profile_not_found"
	CodeProfileDisabled   = "profile_disabled"
	CodeInvalidMode       = "invalid_mode"
)

// Server is the long-running relay process: control surface plus data plane.
type Server struct {
	Version    string
	ConfigPath string
	StatePath  string

	logger *slog.Logger

	cfgMu sync.RWMutex
	cfg   config.Config

	intentMu        sync.Mutex
	intent          state.Intent
	appliedRevision int64

	selector   *Selector
	tables     *TableHolder
	tracker    *upstream.Tracker
	runtime    *runtime.Store
	proxy      *Proxy
	supervisor *Supervisor

	uiMu      sync.RWMutex
	ui        http.Handler
	uiEnabled bool

	watchMu        sync.Mutex
	lastStateMod   time.Time
	lastStateSize  int64
	lastConfigMod  time.Time
	lastConfigSize int64

	background sync.WaitGroup

	started time.Time
}

// NewServer builds a relay server from a validated configuration.
func NewServer(cfg config.Config, configPath, statePath, runtimePath, version string, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if err := config.Validate(&cfg); err != nil {
		return nil, err
	}
	intent, err := state.Load(statePath)
	if err != nil {
		logger.Warn("state unavailable; starting with the default profile", "error", err)
		intent = state.Default(cfg.DefaultProfile)
	}
	selected := selectedProfile(intent, cfg)
	if selected == "" {
		selected = cfg.DefaultProfile
	}
	if !profileUsable(cfg, selected) {
		logger.Warn("stored selection is not usable; falling back to default", "selected", selected, "default", cfg.DefaultProfile)
		selected = cfg.DefaultProfile
	}

	tracker := upstream.NewTracker(cfg.Profiles, cfg.Auto.FailureThreshold, cfg.Auto.RecoveryThreshold)
	profiles := tracker.Snapshot()
	rt := runtime.New(runtimePath, version, cfg.Relay.Listen, profiles)
	initial := Selection{
		Profile:    selected,
		BaseURL:    cfg.Profiles[selected].BaseURL,
		Mode:       modeOf(intent),
		Revision:   intent.Revision,
		Generation: 1,
	}
	s := &Server{
		Version:         version,
		ConfigPath:      configPath,
		StatePath:       statePath,
		logger:          logger,
		cfg:             cfg,
		intent:          intent,
		appliedRevision: intent.Revision,
		selector:        NewSelector(initial),
		tables:          NewTableHolder(NewProfileTable(cfg)),
		tracker:         tracker,
		runtime:         rt,
		started:         time.Now().UTC(),
	}
	s.proxy = NewProxy(s.selector, s.tables, tracker, rt, logger, cfg.Relay.MaxInflight)
	s.proxy.SetTraceHeaders(cfg.Relay.TraceHeaders)
	s.supervisor = NewSupervisor(http.HandlerFunc(s.handle), logger)
	rt.Update(func(snap *runtime.Snapshot) {
		snap.Mode = initial.Mode
		snap.Effective = initial.Profile
		snap.Listen = cfg.Relay.Listen
		setIntentFields(snap, intent)
		v := initial.Profile
		snap.LastGood = &v
		snap.Profiles = profiles
	})
	return s, nil
}

// Config returns a copy of the active configuration.
func (s *Server) Config() config.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

// ConfigFilePath returns the configuration file path.
func (s *Server) ConfigFilePath() string { return s.ConfigPath }

// State returns the last applied operator intent.
func (s *Server) State() state.Intent {
	s.intentMu.Lock()
	defer s.intentMu.Unlock()
	return s.intent
}

// Snapshot returns the current runtime observation.
func (s *Server) Snapshot() runtime.Snapshot { return s.runtime.Snapshot() }

// Selector returns the routing selector.
func (s *Server) Selector() *Selector { return s.selector }

// Tracker returns the health tracker.
func (s *Server) Tracker() *upstream.Tracker { return s.tracker }

// Runtime returns the runtime store.
func (s *Server) Runtime() *runtime.Store { return s.runtime }

// Proxy returns the data plane.
func (s *Server) Proxy() *Proxy { return s.proxy }

// SetUI installs the embedded GUI handler.
func (s *Server) SetUI(handler http.Handler) {
	s.uiMu.Lock()
	s.ui = handler
	s.uiEnabled = handler != nil
	s.uiMu.Unlock()
}

// SetUIEnabled enables or disables the GUI for this run.
func (s *Server) SetUIEnabled(enabled bool) {
	s.uiMu.Lock()
	s.uiEnabled = enabled
	s.uiMu.Unlock()
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.handle) }

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/_shunt/status":
		if !allowRead(w, r) {
			return
		}
		s.handleStatus(w, r)
		return
	case r.URL.Path == "/_shunt/healthz":
		if !allowRead(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	case r.URL.Path == "/_shunt/readyz":
		if !allowRead(w, r) {
			return
		}
		s.handleReady(w)
		return
	case r.URL.Path == "/_shunt/version":
		if !allowRead(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"version": s.Version, "schema": runtime.Schema})
		return
	case r.URL.Path == "/_shunt/ui" || strings.HasPrefix(r.URL.Path, "/_shunt/ui/") || strings.HasPrefix(r.URL.Path, "/_shunt/api/"):
		s.handleUI(w, r)
		return
	}
	if r.Method == http.MethodConnect {
		http.Error(w, "CONNECT is not supported", http.StatusMethodNotAllowed)
		return
	}
	s.proxy.ServeHTTP(w, r)
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	s.uiMu.RLock()
	handler := s.ui
	enabled := s.uiEnabled
	s.uiMu.RUnlock()
	if !enabled || handler == nil {
		http.NotFound(w, r)
		return
	}
	handler.ServeHTTP(w, r)
}

func allowRead(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	snap := s.runtime.Snapshot()
	snap.Profiles = s.tracker.Snapshot()
	snap.Inflight = s.proxy.Inflight().Count()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snap)
}

func (s *Server) handleReady(w http.ResponseWriter) {
	snap := s.runtime.Snapshot()
	profiles := s.tracker.Snapshot()
	ready := false
	for _, p := range profiles {
		if p.Enabled && p.Healthy != nil && *p.Healthy {
			ready = true
			break
		}
	}
	_ = snap
	if !ready {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "reason": "no_healthy_upstream"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": true})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Run starts the watchers, probe loop, runtime writer, and listener, then
// blocks until ctx is cancelled. The listener is shut down with the configured
// grace period.
func (s *Server) Run(ctx context.Context) error {
	cfg := s.Config()
	if _, err := config.NormalizeListen(cfg.Relay.Listen); err != nil {
		return err
	}
	if err := s.supervisor.Start(cfg.Relay.Listen); err != nil {
		return fmt.Errorf("bind %s: %w", cfg.Relay.Listen, err)
	}
	s.background.Add(4)
	go func() { defer s.background.Done(); s.stateWatcher(ctx) }()
	go func() { defer s.background.Done(); s.configWatcher(ctx) }()
	go func() { defer s.background.Done(); s.probeLoop(ctx) }()
	go func() { defer s.background.Done(); s.runtime.Run(ctx, 5*time.Second) }()

	<-ctx.Done()
	grace := time.Duration(cfg.Relay.ShutdownGraceMS) * time.Millisecond
	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	s.proxy.CloseIdle()
	err := s.supervisor.Shutdown(shutdownCtx)
	s.waitForBackground()
	return err
}

// waitForBackground lets the watchers and runtime writer observe cancellation
// before the process (or a test) removes the data directory.
func (s *Server) waitForBackground() {
	s.background.Wait()
}

// ServeHTTP lets Server satisfy http.Handler for tests and composition.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handle(w, r) }

func (s *Server) stateWatcher(ctx context.Context) {
	cfg := s.Config()
	interval := time.Duration(cfg.Relay.StatePollMS) * time.Millisecond
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollState()
		}
	}
}

func (s *Server) pollState() {
	info, err := os.Stat(s.StatePath)
	if err != nil {
		return
	}
	s.watchMu.Lock()
	unchanged := info.ModTime().Equal(s.lastStateMod) && info.Size() == s.lastStateSize
	if !unchanged {
		s.lastStateMod = info.ModTime()
		s.lastStateSize = info.Size()
	}
	s.watchMu.Unlock()
	if unchanged && s.appliedRevision > 0 {
		return
	}
	in, err := state.Load(s.StatePath)
	if err != nil {
		s.logger.Warn("ignoring unreadable state", "error", err)
		return
	}
	if in.Revision <= s.appliedRevision {
		return
	}
	s.applyIntent(in, in.Revision, "state")
}

func (s *Server) applyIntent(in state.Intent, revision int64, by string) {
	cfg := s.Config()
	profile := selectedProfile(in, cfg)
	if profile == "" || !profileUsable(cfg, profile) {
		fallback := cfg.DefaultProfile
		s.logger.Warn("state references an unknown profile; falling back", "profile", profile, "fallback", fallback)
		profile = fallback
		in.Selected = &fallback
		in.Mode = state.ModeManual
	}
	s.intentMu.Lock()
	s.intent = in
	s.appliedRevision = revision
	s.intentMu.Unlock()
	s.recomputeSelection("operator", by)
}

// recomputeSelection resolves the immutable routing snapshot from intent and
// health, then publishes it if anything changed.
func (s *Server) recomputeSelection(reason, by string) {
	cfg := s.Config()
	s.intentMu.Lock()
	in := s.intent
	s.intentMu.Unlock()

	mode := in.Mode
	if mode != state.ModeAuto {
		mode = state.ModeManual
	}
	var profile string
	if mode == state.ModeAuto {
		preferred := ""
		if in.Preferred != nil {
			preferred = *in.Preferred
		}
		if preferred == "" || !profileUsable(cfg, preferred) {
			preferred = cfg.DefaultProfile
		}
		profile = s.autoEffective(cfg, preferred)
	} else {
		profile = selectedProfile(in, cfg)
		if profile == "" || !profileUsable(cfg, profile) {
			profile = cfg.DefaultProfile
		}
	}
	p := cfg.Profiles[profile]
	s.publishSelection(profile, p.BaseURL, mode, in.Revision, reason, by)
}

func (s *Server) autoEffective(cfg config.Config, preferred string) string {
	if s.tracker.IsHealthy(preferred) {
		return preferred
	}
	ids := make([]string, 0, len(cfg.Profiles))
	for id, p := range cfg.Profiles {
		if p.IsEnabled() && id != preferred {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		pi, pj := cfg.Profiles[ids[i]], cfg.Profiles[ids[j]]
		if pi.Priority != pj.Priority {
			return pi.Priority < pj.Priority
		}
		return ids[i] < ids[j]
	})
	for _, id := range ids {
		if s.tracker.IsHealthy(id) {
			return id
		}
	}
	last := s.selector.Load().Profile
	if profileUsable(cfg, last) {
		return last
	}
	return preferred
}

func (s *Server) publishSelection(profile, baseURL, mode string, revision int64, reason, by string) {
	prev := s.selector.Load()
	next := Selection{
		Profile:    profile,
		BaseURL:    baseURL,
		Mode:       mode,
		Revision:   revision,
		Generation: prev.Generation + 1,
	}
	if prev.Profile == next.Profile && prev.BaseURL == next.BaseURL && prev.Mode == next.Mode && prev.Revision == next.Revision {
		return
	}
	s.selector.Store(next)
	if prev.Profile != "" && prev.Profile != next.Profile {
		event := runtime.SwitchEvent{
			At:     time.Now().UTC(),
			From:   prev.Profile,
			To:     next.Profile,
			Reason: reason,
			By:     by,
		}
		s.runtime.Update(func(snap *runtime.Snapshot) {
			snap.SwitchesTotal++
			snap.LastSwitch = &event
			snap.Effective = next.Profile
			snap.Mode = next.Mode
			snap.Listen = s.supervisor.Addr()
			if snap.Listen == "" {
				snap.Listen = s.Config().Relay.Listen
			}
			setIntentFields(snap, s.State())
			v := next.Profile
			snap.LastGood = &v
			snap.Profiles = s.tracker.Snapshot()
		})
		s.logger.Info("effective upstream changed", "from", prev.Profile, "to", next.Profile, "reason", reason, "by", by)
		return
	}
	s.runtime.Update(func(snap *runtime.Snapshot) {
		snap.Effective = next.Profile
		snap.Mode = next.Mode
		setIntentFields(snap, s.State())
		snap.Profiles = s.tracker.Snapshot()
	})
}

func (s *Server) probeLoop(ctx context.Context) {
	cfg := s.Config()
	interval := time.Duration(cfg.Auto.ProbeIntervalMS) * time.Millisecond
	if interval <= 0 {
		interval = 10 * time.Second
	}
	// A small jitter avoids synchronized probes when several profiles exist.
	jitter := time.Duration(rand.Int63n(int64(interval / 10)))
	timer := time.NewTimer(jitter)
	first := make(chan struct{}, 1)
	first <- struct{}{}
	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-first:
		case <-timer.C:
		}
		s.probeOnce(ctx)
		timer.Reset(interval + time.Duration(rand.Int63n(int64(interval/10)+1)))
	}
}

func (s *Server) probeOnce(ctx context.Context) {
	cfg := s.Config()
	timeout := time.Duration(cfg.Auto.ProbeTimeoutMS) * time.Millisecond
	for id, p := range cfg.Profiles {
		if !p.IsEnabled() {
			continue
		}
		tr := upstream.NewTransport(p)
		client := &http.Client{Transport: tr}
		res := upstream.Probe(ctx, client, p, timeout)
		tr.CloseIdleConnections()
		s.tracker.Observe(id, res)
	}
	s.runtime.Update(func(snap *runtime.Snapshot) { snap.Profiles = s.tracker.Snapshot() })
	if s.State().Mode == state.ModeAuto {
		s.recomputeSelection("health", "auto")
	}
}

func (s *Server) configWatcher(ctx context.Context) {
	cfg := s.Config()
	interval := time.Duration(cfg.Relay.ConfigPollMS) * time.Millisecond
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollConfig()
		}
	}
}

func (s *Server) pollConfig() {
	info, err := os.Stat(s.ConfigPath)
	if err != nil {
		return
	}
	s.watchMu.Lock()
	unchanged := info.ModTime().Equal(s.lastConfigMod) && info.Size() == s.lastConfigSize
	if !unchanged {
		s.lastConfigMod = info.ModTime()
		s.lastConfigSize = info.Size()
	}
	s.watchMu.Unlock()
	if unchanged {
		return
	}
	cfg, _, err := config.Load(s.ConfigPath)
	if err != nil {
		s.logger.Warn("ignoring invalid configuration edit", "error", err)
		return
	}
	if err := s.applyConfig(cfg); err != nil {
		s.logger.Warn("ignoring configuration edit", "error", err)
	}
}

func (s *Server) applyConfig(cfg config.Config) error {
	old := s.Config()
	addrChanged := cfg.Relay.Listen != old.Relay.Listen
	if addrChanged {
		if _, err := s.supervisor.Rebind(cfg.Relay.Listen); err != nil {
			return fmt.Errorf("bind %s: %w", cfg.Relay.Listen, err)
		}
	}
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
	s.tables.Store(NewProfileTable(cfg))
	s.proxy.SetTraceHeaders(cfg.Relay.TraceHeaders)
	for id, p := range cfg.Profiles {
		s.tracker.UpdateProfile(id, p.BaseURL, p.IsEnabled())
	}
	s.runtime.Update(func(snap *runtime.Snapshot) {
		snap.Profiles = s.tracker.Snapshot()
		snap.Listen = cfg.Relay.Listen
	})
	s.recomputeSelection("config", "watcher")
	return nil
}

// SwitchProfile selects a profile in manual mode. When checkProbe is true the
// profile is probed first and a failure is refused unless force is set.
func (s *Server) SwitchProfile(profile string, checkProbe, force bool, by, reason string) (upstream.Result, error) {
	cfg := s.Config()
	p, ok := cfg.Profiles[profile]
	if !ok {
		return upstream.Result{}, &ApplyError{Code: CodeProfileNotFound, Field: "profile", Message: fmt.Sprintf("profile %q does not exist", profile)}
	}
	if !p.IsEnabled() {
		return upstream.Result{}, &ApplyError{Code: CodeProfileDisabled, Field: "profile", Message: fmt.Sprintf("profile %q is disabled", profile)}
	}
	var res upstream.Result
	if checkProbe {
		tr := upstream.NewTransport(p)
		client := &http.Client{Transport: tr}
		res = upstream.Probe(context.Background(), client, p, 5*time.Second)
		tr.CloseIdleConnections()
		s.tracker.Observe(profile, res)
		if !res.OK && !force {
			return res, &ApplyError{Code: CodeProbeFailed, Field: "profile", Message: fmt.Sprintf("probe of %q failed: %s", profile, res.Err), Err: errors.New(res.Err)}
		}
	}
	in := state.Select(profile, by, reason)
	saved, err := state.Save(s.StatePath, in)
	if err != nil {
		return res, err
	}
	s.applyIntent(saved, saved.Revision, by)
	return res, nil
}

// SetMode switches between manual and auto mode.
func (s *Server) SetMode(mode, preferred, by string) error {
	if mode != state.ModeManual && mode != state.ModeAuto {
		return &ApplyError{Code: CodeInvalidMode, Field: "mode", Message: "mode must be manual or auto"}
	}
	cfg := s.Config()
	current := s.State()
	var in state.Intent
	if mode == state.ModeAuto {
		if preferred == "" {
			if current.Preferred != nil {
				preferred = *current.Preferred
			} else {
				preferred = cfg.DefaultProfile
			}
		}
		if !profileUsable(cfg, preferred) {
			return &ApplyError{Code: CodeProfileNotFound, Field: "preferred", Message: fmt.Sprintf("profile %q is not enabled", preferred)}
		}
		in = state.SetAuto(preferred, current.Selected, by, "operator")
	} else {
		selected := ""
		if current.Selected != nil {
			selected = *current.Selected
		}
		if !profileUsable(cfg, selected) {
			selected = cfg.DefaultProfile
		}
		in = state.Select(selected, by, "operator")
		if current.Preferred != nil {
			in.Preferred = current.Preferred
		}
	}
	saved, err := state.Save(s.StatePath, in)
	if err != nil {
		return err
	}
	s.applyIntent(saved, saved.Revision, by)
	return nil
}

// ApplyEntry changes the listener IP and port. It binds the new socket before
// writing the configuration and keeps the old listener on any failure.
func (s *Server) ApplyEntry(ip string, port int, confirmLAN bool) (string, int, error) {
	if port < 1024 || port > 65535 {
		return "", 0, &ApplyError{Code: CodeInvalidEntry, Field: "port", Message: "port must be 1024..65535"}
	}
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return "", 0, &ApplyError{Code: CodeInvalidEntry, Field: "ip", Message: "ip must be an IP address"}
	}
	if parsed.IsUnspecified() {
		return "", 0, &ApplyError{Code: CodeInvalidEntry, Field: "ip", Message: "wildcard binds are not allowed"}
	}
	loopback := parsed.IsLoopback()
	if !loopback {
		if !config.IsPrivateIP(parsed) {
			return "", 0, &ApplyError{Code: CodeInvalidEntry, Field: "ip", Message: "only loopback or private local addresses are allowed"}
		}
		if !confirmLAN {
			return "", 0, &ApplyError{Code: CodeLANNotConfirmed, Field: "ip", Message: "a non-loopback bind requires confirm_lan: true"}
		}
	}
	addr := net.JoinHostPort(parsed.String(), strconv.Itoa(port))
	if err := config.ValidateListen(addr, true); err != nil {
		return "", 0, &ApplyError{Code: CodeInvalidEntry, Field: "ip", Message: err.Error(), Err: err}
	}
	// Bind first so a busy address never disturbs the running listener.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", 0, &ApplyError{Code: CodeBindFailed, Field: "port", Message: err.Error(), Current: s.supervisor.Addr(), Err: err}
	}
	_ = ln.Close()

	cfg := s.Config()
	newCfg := cfg
	newCfg.Relay.Listen = addr
	if !loopback {
		newCfg.Relay.AllowNonLoopback = true
	}
	if err := config.Validate(&newCfg); err != nil {
		return "", 0, &ApplyError{Code: CodeInvalidEntry, Field: "ip", Message: err.Error(), Err: err}
	}
	if err := s.writeConfigEdits(func(text string) (string, error) {
		out, err := config.SetListen(text, addr)
		if err != nil {
			return "", err
		}
		if !loopback {
			out, err = config.SetAllowNonLoopback(out, true)
		}
		return out, err
	}); err != nil {
		return "", 0, &ApplyError{Code: CodeConfigWriteFailed, Message: err.Error(), Err: err}
	}
	gen, err := s.supervisor.Rebind(addr)
	if err != nil {
		return "", 0, &ApplyError{Code: CodeBindFailed, Field: "port", Message: err.Error(), Current: s.supervisor.Addr(), Err: err}
	}
	s.cfgMu.Lock()
	s.cfg = newCfg
	s.cfgMu.Unlock()
	s.runtime.Update(func(snap *runtime.Snapshot) { snap.Listen = addr })
	return addr, gen, nil
}

// ApplyProfileEndpoint validates, probes, and hot-applies a profile origin.
func (s *Server) ApplyProfileEndpoint(id, scheme, host string, port int) (upstream.Result, error) {
	cfg := s.Config()
	p, ok := cfg.Profiles[id]
	if !ok {
		return upstream.Result{}, &ApplyError{Code: CodeProfileNotFound, Field: "profile", Message: fmt.Sprintf("profile %q does not exist", id)}
	}
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme != "http" && scheme != "https" {
		return upstream.Result{}, &ApplyError{Code: CodeInvalidEndpoint, Field: "scheme", Message: "scheme must be http or https"}
	}
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, "/?#@") {
		return upstream.Result{}, &ApplyError{Code: CodeInvalidEndpoint, Field: "host", Message: "host must be a DNS name or IP literal"}
	}
	if port == 0 {
		if scheme == "https" {
			port = 443
		} else {
			port = 80
		}
	}
	if port < 1 || port > 65535 {
		return upstream.Result{}, &ApplyError{Code: CodeInvalidEndpoint, Field: "port", Message: "port must be 1..65535"}
	}
	origin := scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port))
	if _, err := url.Parse(origin); err != nil {
		return upstream.Result{}, &ApplyError{Code: CodeInvalidEndpoint, Field: "host", Message: err.Error(), Err: err}
	}
	candidate := p
	candidate.BaseURL = origin
	candidate.ProbeURL = origin + "/"
	if scheme == "https" && !candidate.VerifyTLS() {
		return upstream.Result{}, &ApplyError{Code: CodeInvalidEndpoint, Field: "tls_verify", Message: "TLS verification cannot be disabled"}
	}
	tr := upstream.NewTransport(candidate)
	client := &http.Client{Transport: tr}
	res := upstream.Probe(context.Background(), client, candidate, 5*time.Second)
	tr.CloseIdleConnections()
	if !res.OK {
		return res, &ApplyError{Code: CodeProbeFailed, Field: "host", Message: fmt.Sprintf("probe failed: %s", res.Err), Err: errors.New(res.Err)}
	}
	if err := s.writeConfigEdits(func(text string) (string, error) {
		return config.SetProfileBaseURL(text, id, origin, origin+"/")
	}); err != nil {
		return res, &ApplyError{Code: CodeConfigWriteFailed, Message: err.Error(), Err: err}
	}
	candidate.ProbeURL = origin + "/"
	s.cfgMu.Lock()
	s.cfg.Profiles[id] = candidate
	s.cfgMu.Unlock()
	s.tables.Store(NewProfileTable(s.Config()))
	s.tracker.UpdateProfile(id, origin, candidate.IsEnabled())
	s.runtime.Update(func(snap *runtime.Snapshot) {
		snap.Profiles = s.tracker.Snapshot()
		if snap.Effective == id {
			snap.LastGood = &id
		}
	})
	s.recomputeSelection("endpoint", "gui")
	return res, nil
}

func (s *Server) writeConfigEdits(edit func(string) (string, error)) error {
	if s.ConfigPath == "" {
		return fmt.Errorf("no configuration path")
	}
	data, err := os.ReadFile(s.ConfigPath)
	if err != nil {
		return err
	}
	out, err := edit(string(data))
	if err != nil {
		return err
	}
	return platform.WriteFileAtomic(s.ConfigPath, []byte(out), 0o600)
}

// ReloadConfig re-reads and applies the configuration file. It is used by the
// CLI after an edit and by the watcher.
func (s *Server) ReloadConfig() error {
	cfg, _, err := config.Load(s.ConfigPath)
	if err != nil {
		return err
	}
	return s.applyConfig(cfg)
}

// Uptime returns how long the server has been running.
func (s *Server) Uptime() time.Duration { return time.Since(s.started) }

func selectedProfile(in state.Intent, cfg config.Config) string {
	if in.Mode == state.ModeAuto {
		if in.Preferred != nil {
			return *in.Preferred
		}
		return cfg.DefaultProfile
	}
	if in.Selected != nil {
		return *in.Selected
	}
	if in.Preferred != nil {
		return *in.Preferred
	}
	return cfg.DefaultProfile
}

func modeOf(in state.Intent) string {
	if in.Mode == state.ModeAuto {
		return state.ModeAuto
	}
	return state.ModeManual
}

func profileUsable(cfg config.Config, id string) bool {
	p, ok := cfg.Profiles[id]
	return ok && p.IsEnabled()
}

func setIntentFields(snap *runtime.Snapshot, in state.Intent) {
	if in.Mode == state.ModeManual {
		snap.Selected = in.Selected
		snap.Preferred = nil
		return
	}
	snap.Preferred = in.Preferred
	snap.Selected = in.Selected
}

// Ensure the file path helpers compile on all platforms.
var _ = filepath.Join
