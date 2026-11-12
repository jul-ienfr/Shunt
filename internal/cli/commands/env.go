package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"shunt/internal/config"
	"shunt/internal/platform"
	"shunt/internal/state"
)

// Schema is the version of every JSON document produced by shunt.
const Schema = 1

// Exit codes from contracts/cli.md.
const (
	ExitOK              = 0
	ExitGeneric         = 1
	ExitUsage           = 2
	ExitNoFreeModel     = 3
	ExitUpstream        = 4
	ExitRelayNotRunning = 5
	ExitWriteRefused    = 6
	ExitAlreadyRunning  = 7
)

// Env is the shared command environment.
type Env struct {
	Version    string
	ConfigPath string
	JSON       bool
	NoColor    bool
	Stdout     io.Writer
	Stderr     io.Writer
	Stdin      io.Reader
	LookupEnv  func(string) (string, bool)
}

func (e *Env) out() io.Writer {
	if e.Stdout == nil {
		return os.Stdout
	}
	return e.Stdout
}

func (e *Env) err() io.Writer {
	if e.Stderr == nil {
		return os.Stderr
	}
	return e.Stderr
}

func (e *Env) getenv(name string) string {
	if e.LookupEnv != nil {
		if v, ok := e.LookupEnv(name); ok {
			return v
		}
		return ""
	}
	return os.Getenv(name)
}

func (e *Env) configPath() string {
	if strings.TrimSpace(e.ConfigPath) != "" {
		return e.ConfigPath
	}
	if v := strings.TrimSpace(e.getenv("SHUNT_CONFIG")); v != "" {
		return v
	}
	paths, err := platform.DefaultPaths()
	if err != nil {
		return ""
	}
	return paths.ConfigFile
}

func (e *Env) load() (config.Config, string, platform.Paths, error) {
	path := e.configPath()
	cfg, actual, err := config.Load(path)
	if err != nil {
		return config.Config{}, actual, platform.Paths{}, err
	}
	paths, err := config.ResolvePaths(cfg, actual)
	if err != nil {
		return config.Config{}, actual, platform.Paths{}, err
	}
	return cfg, actual, paths, nil
}

func (e *Env) defaults() (config.Config, platform.Paths, error) {
	cfg := config.Defaults()
	paths, err := platform.DefaultPaths()
	if err != nil {
		return cfg, paths, err
	}
	if e.ConfigPath != "" {
		paths.ConfigFile = e.ConfigPath
	}
	return cfg, paths, nil
}

func (e *Env) fail(code int, format string, args ...any) int {
	fmt.Fprintf(e.err(), format+"\n", args...)
	return code
}

func (e *Env) printf(format string, args ...any) {
	fmt.Fprintf(e.out(), format, args...)
}

func (e *Env) printJSON(v any) error {
	enc := json.NewEncoder(e.out())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (e *Env) tokenLookup() func(string) string {
	return func(name string) string { return e.getenv(name) }
}

func relayOrigin(cfg config.Config) string {
	return config.ListenOrigin(cfg.PrimaryListener().Listen)
}

func (e *Env) liveStatus(listen string) (runtimeSnapshot, bool) {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return runtimeSnapshot{}, false
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, config.ListenOrigin(listen)+"/_shunt/status", nil)
	if err != nil {
		return runtimeSnapshot{}, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return runtimeSnapshot{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return runtimeSnapshot{}, false
	}
	var snap runtimeSnapshot
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&snap); err != nil {
		return runtimeSnapshot{}, false
	}
	return snap, true
}

// runtimeSnapshot mirrors runtime.Snapshot closely enough for CLI reads. It
// uses the same JSON tags so the live and persisted forms decode identically.
type runtimeSnapshot struct {
	Schema        int                      `json:"schema"`
	Version       string                   `json:"version"`
	PID           int                      `json:"pid"`
	StartedAt     time.Time                `json:"started_at"`
	UptimeS       int64                    `json:"uptime_s"`
	Listen        string                   `json:"listen"`
	Mode          string                   `json:"mode"`
	Selected      *string                  `json:"selected"`
	Preferred     *string                  `json:"preferred"`
	Effective     string                   `json:"effective"`
	LastGood      *string                  `json:"last_good"`
	Inflight      int                      `json:"inflight"`
	RequestsTotal int64                    `json:"requests_total"`
	ErrorsTotal   int64                    `json:"errors_total"`
	SwitchesTotal int64                    `json:"switches_total"`
	BytesIn       int64                    `json:"bytes_in"`
	BytesOut      int64                    `json:"bytes_out"`
	LastSwitch    *runtimeSwitchEvent      `json:"last_switch"`
	Profiles      map[string]runtimeHealth `json:"profiles"`
}

type runtimeSwitchEvent struct {
	At     time.Time `json:"at"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Reason string    `json:"reason"`
	By     string    `json:"by"`
}

type runtimeHealth struct {
	BaseURL              string     `json:"base_url"`
	Enabled              bool       `json:"enabled"`
	Healthy              *bool      `json:"healthy"`
	LastProbeAt          *time.Time `json:"last_probe_at"`
	LastProbeMS          *int64     `json:"last_probe_ms"`
	LastStatus           *int       `json:"last_status"`
	ConsecutiveFailures  int        `json:"consecutive_failures"`
	ConsecutiveSuccesses int        `json:"consecutive_successes"`
	LastError            *string    `json:"last_error"`
}

func readRuntimeFile(path string) (runtimeSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return runtimeSnapshot{}, err
	}
	var snap runtimeSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return runtimeSnapshot{}, err
	}
	return snap, nil
}

// currentSnapshot returns the live snapshot when reachable, then runtime.json.
func (e *Env) currentSnapshot(cfg config.Config, paths platform.Paths) (runtimeSnapshot, bool) {
	if snap, ok := e.liveStatus(cfg.PrimaryListener().Listen); ok {
		return snap, true
	}
	if snap, err := readRuntimeFile(paths.RuntimeFile); err == nil && snap.Listen != "" {
		if live, ok := e.liveStatus(snap.Listen); ok {
			return live, true
		}
		return snap, false
	}
	return runtimeSnapshot{}, false
}

func (e *Env) waitEffective(listen, profile string, timeout time.Duration) (runtimeSnapshot, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if snap, ok := e.liveStatus(listen); ok && snap.Effective == profile {
			return snap, true
		}
		if time.Now().After(deadline) {
			if snap, ok := e.liveStatus(listen); ok {
				return snap, false
			}
			return runtimeSnapshot{}, false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (e *Env) readState(path string) (state.Intent, error) {
	return state.Load(path)
}

func writableDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("empty path")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".shunt-write-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	_, werr := f.WriteString("ok")
	cerr := f.Close()
	_ = os.Remove(name)
	if werr != nil {
		return werr
	}
	return cerr
}

func expandPath(path string) (string, error) {
	return platform.ExpandHome(path)
}

func openBrowser(target string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	case "darwin":
		return exec.Command("open", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}

func backupClientFromName(name string) string {
	clean := filepath.ToSlash(filepath.Clean(name))
	parts := strings.Split(clean, "/")
	for _, client := range []string{"claude", "codex", "dsh"} {
		for _, part := range parts {
			if part == client {
				return client
			}
		}
	}
	return ""
}
