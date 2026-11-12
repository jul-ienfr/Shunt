package support

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"shunt/internal/config"
	"shunt/internal/relay"
	"shunt/internal/runtime"
	"shunt/internal/state"
	"shunt/internal/ui"
)

// RelayOptions controls the isolated relay instance built by NewRelay.
type RelayOptions struct {
	// Profiles maps profile ID to base URL. When nil the built-in defaults are
	// used. Probe URLs are derived from each base URL.
	Profiles map[string]string

	DefaultProfile string
	Mode           string
	Selected       string
	Preferred      string

	MaxInflight int
	Auto        bool

	AutoProbeIntervalMS   int
	AutoProbeTimeoutMS    int
	AutoFailureThreshold  int
	AutoRecoveryThreshold int

	StatePollMS             int
	ConfigPollMS            int
	ResponseHeaderTimeoutMS int

	// Listeners describes additional entry ports. Each entry receives a free
	// loopback port; the first enabled listener is the primary. When empty the
	// relay keeps the legacy single [relay] listen entry.
	Listeners []ListenerOption

	// LogWriter receives relay logs. When nil, logs are discarded.
	LogWriter io.Writer
}

// ListenerOption configures one entry port for a test relay.
type ListenerOption struct {
	Name     string
	Profile  string
	Disabled bool
}

// Relay is a fully running relay with isolated configuration and state files.
type Relay struct {
	Server *relay.Server
	UI     *ui.Handler

	Dir         string
	ConfigPath  string
	StatePath   string
	RuntimePath string
	CacheDir    string

	Listen string
	Port   int
	// ListenerAddrs maps listener name to its loopback address.
	ListenerAddrs map[string]string

	ctx    context.Context
	cancel context.CancelFunc
	done   chan error
}

// Origin returns the relay entry origin, for example http://127.0.0.1:8787.
func (r *Relay) Origin() string { return "http://" + r.Listen }

// URL joins the relay origin and a path.
func (r *Relay) URL(path string) string { return r.Origin() + path }

// Config returns the active configuration.
func (r *Relay) Config() config.Config { return r.Server.Config() }

// WriteConfig serializes cfg over the relay configuration file. The running
// relay is not reloaded; call ReloadConfig for an explicit apply.
func (r *Relay) WriteConfig(t *testing.T, cfg config.Config) {
	t.Helper()
	data, err := toml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(r.ConfigPath, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// RewriteConfig writes raw TOML over the relay configuration file.
func (r *Relay) RewriteConfig(t *testing.T, text string) {
	t.Helper()
	if err := os.WriteFile(r.ConfigPath, []byte(text), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// ReloadConfig applies the current on-disk configuration synchronously.
func (r *Relay) ReloadConfig(t *testing.T) {
	t.Helper()
	if err := r.Server.ReloadConfig(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
}

// UpdateConfig mutates the active configuration, writes it, and reloads it.
func (r *Relay) UpdateConfig(t *testing.T, mutate func(*config.Config)) {
	t.Helper()
	cfg := r.Server.Config()
	mutate(&cfg)
	r.WriteConfig(t, cfg)
	r.ReloadConfig(t)
}

// Close stops the relay and waits for it to exit.
func (r *Relay) Close() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(10 * time.Second):
	}
	r.cancel = nil
}

// NewRelay builds an isolated configuration, starts the relay on a free
// loopback port, attaches the GUI, and waits until it answers requests.
func NewRelay(t testing.TB, opts RelayOptions) *Relay {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	statePath := filepath.Join(dir, "state.json")
	runtimePath := filepath.Join(dir, "runtime.json")
	cacheDir := filepath.Join(dir, "cache")

	listen, port := freeLoopback(t)
	cfg := buildConfig(t, opts)
	cfg.Relay.Listen = listen
	listenerAddrs := map[string]string{}
	if len(opts.Listeners) > 0 {
		listeners := make([]config.Listener, 0, len(opts.Listeners))
		for i, option := range opts.Listeners {
			addr := listen
			if i > 0 {
				addr, _ = freeLoopback(t)
			}
			enabled := !option.Disabled
			listeners = append(listeners, config.Listener{
				Name:    option.Name,
				Listen:  addr,
				Profile: option.Profile,
				Enabled: &enabled,
			})
			listenerAddrs[option.Name] = addr
		}
		cfg.Listeners = listeners
		primary := cfg.PrimaryListener()
		listen = primary.Listen
		port = portOf(t, listen)
	}
	cfg.Paths = config.Paths{
		State:     statePath,
		Runtime:   runtimePath,
		CacheDir:  cacheDir,
		LogDir:    filepath.Join(dir, "logs"),
		BackupDir: filepath.Join(dir, "backups"),
	}

	if err := config.Validate(&cfg); err != nil {
		t.Fatalf("test config invalid: %v", err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatalf("create cache dir: %v", err)
	}

	intent := state.Select(cfg.DefaultProfile, "test", "startup")
	if opts.Selected != "" {
		intent = state.Select(opts.Selected, "test", "startup")
	}
	if opts.Mode == state.ModeAuto {
		var selected *string
		if opts.Selected != "" {
			value := opts.Selected
			selected = &value
		}
		preferred := opts.Preferred
		if preferred == "" {
			preferred = cfg.DefaultProfile
		}
		intent = state.SetAuto(preferred, selected, "test", "startup")
	}
	if _, err := state.Save(statePath, intent); err != nil {
		t.Fatalf("write state: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if opts.LogWriter != nil {
		logger = slog.New(slog.NewTextHandler(opts.LogWriter, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	server, err := relay.NewServer(cfg, configPath, statePath, runtimePath, "0.1.0-test", logger)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	handler, err := ui.New(server, int64(cfg.UI.MaxRequestBytes), logger)
	if err != nil {
		t.Fatalf("new ui: %v", err)
	}
	server.SetUI(handler)
	server.Runtime().Update(func(snap *runtime.Snapshot) { snap.Listen = listen })

	r := &Relay{
		Server:        server,
		UI:            handler,
		Dir:           dir,
		ConfigPath:    configPath,
		StatePath:     statePath,
		RuntimePath:   runtimePath,
		CacheDir:      cacheDir,
		Listen:        listen,
		Port:          port,
		ListenerAddrs: listenerAddrs,
		done:          make(chan error, 1),
	}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	go func() { r.done <- server.Run(r.ctx) }()
	waitReady(t, r)
	t.Cleanup(r.Close)
	return r
}

func buildConfig(t testing.TB, opts RelayOptions) config.Config {
	t.Helper()
	cfg := config.Defaults()
	if opts.MaxInflight > 0 {
		cfg.Relay.MaxInflight = opts.MaxInflight
	}
	if opts.StatePollMS > 0 {
		cfg.Relay.StatePollMS = opts.StatePollMS
	}
	if opts.ConfigPollMS > 0 {
		cfg.Relay.ConfigPollMS = opts.ConfigPollMS
	}
	if opts.ResponseHeaderTimeoutMS > 0 {
		cfg.Relay.ResponseHeaderTimeoutMS = opts.ResponseHeaderTimeoutMS
	}

	if opts.Profiles != nil {
		yes := true
		profiles := make(map[string]config.Profile, len(opts.Profiles))
		ids := make([]string, 0, len(opts.Profiles))
		for id := range opts.Profiles {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for i, id := range ids {
			base := opts.Profiles[id]
			profiles[id] = config.Profile{
				Label:                   id,
				BaseURL:                 base,
				ProbeURL:                base + "/",
				ProbeMethod:             http.MethodGet,
				ConnectTimeoutMS:        500,
				ResponseHeaderTimeoutMS: cfg.Relay.ResponseHeaderTimeoutMS,
				Priority:                10 * (i + 1),
				Enabled:                 &yes,
			}
		}
		cfg.Profiles = profiles
	}

	ids := make([]string, 0, len(cfg.Profiles))
	for id, p := range cfg.Profiles {
		if p.IsEnabled() {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		t.Fatal("test configuration has no enabled profile")
	}
	pick := func(preferred string) string {
		if preferred != "" {
			if p, ok := cfg.Profiles[preferred]; ok && p.IsEnabled() {
				return preferred
			}
		}
		for _, id := range ids {
			if id == "local" {
				return id
			}
		}
		return ids[0]
	}

	cfg.DefaultProfile = pick(opts.DefaultProfile)
	if p, ok := cfg.Profiles["local"]; ok && p.IsEnabled() {
		cfg.Switch.OffProfile = "local"
	} else {
		cfg.Switch.OffProfile = cfg.DefaultProfile
	}
	if p, ok := cfg.Profiles["remote"]; ok && p.IsEnabled() {
		cfg.Switch.OnProfile = "remote"
	} else {
		cfg.Switch.OnProfile = ids[len(ids)-1]
	}
	cfg.Auto.Enabled = opts.Auto
	cfg.Auto.Preferred = pick(opts.Preferred)
	if opts.AutoProbeIntervalMS > 0 {
		cfg.Auto.ProbeIntervalMS = opts.AutoProbeIntervalMS
	}
	if opts.AutoProbeTimeoutMS > 0 {
		cfg.Auto.ProbeTimeoutMS = opts.AutoProbeTimeoutMS
	}
	if opts.AutoFailureThreshold > 0 {
		cfg.Auto.FailureThreshold = opts.AutoFailureThreshold
	}
	if opts.AutoRecoveryThreshold > 0 {
		cfg.Auto.RecoveryThreshold = opts.AutoRecoveryThreshold
	}
	if cfg.Auto.Preferred == "" {
		cfg.Auto.Preferred = cfg.DefaultProfile
	}
	cfg.Relay.LogLevel = "error"
	return cfg
}

// ListenerURL returns the origin of a named listener, or an empty string.
func (r *Relay) ListenerURL(name string) string {
	addr, ok := r.ListenerAddrs[name]
	if !ok {
		return ""
	}
	return "http://" + addr
}

func portOf(t testing.TB, addr string) int {
	t.Helper()
	_, raw, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("parse port %q: %v", raw, err)
	}
	return port
}

// ReserveLoopback returns a free loopback address for tests that add a
// listener after the relay started.
func ReserveLoopback(t testing.TB) string {
	t.Helper()
	addr, _ := freeLoopback(t)
	return addr
}

func freeLoopback(t testing.TB) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close()
	if addr.Port < 1024 {
		t.Skipf("ephemeral port %d is below the configured minimum", addr.Port)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(addr.Port)), addr.Port
}

func waitReady(t testing.TB, r *Relay) {
	t.Helper()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(r.URL("/_shunt/healthz"))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case err := <-r.done:
			t.Fatalf("relay exited during startup: %v", err)
		case <-time.After(25 * time.Millisecond):
		}
	}
	t.Fatal("relay did not become ready")
}

// DecodeJSON decodes a response body into dst and closes it.
func DecodeJSON(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

// MustJSON marshals v, failing the test on error.
func MustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return data
}

// WaitFor polls cond until it returns true or the timeout elapses.
func WaitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// FormatAddr is a small helper for tests that need a host:port.
func FormatAddr(host string, port int) string { return fmt.Sprintf("%s:%d", host, port) }
