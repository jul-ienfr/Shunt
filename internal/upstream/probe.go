package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"shunt/internal/config"
	"shunt/internal/runtime"
)

// Result is the outcome of one unauthenticated reachability probe.
type Result struct {
	OK        bool
	Status    int
	LatencyMS int64
	Err       string
	Timeout   bool
}

// Probe checks a profile with probe_method and probe_url. Any HTTP status below
// 500 counts as reachable; HTTP authorization and rate-limit responses are
// application results, not transport failures.
func Probe(ctx context.Context, client *http.Client, p config.Profile, timeout time.Duration) Result {
	target := p.ProbeURL
	if target == "" {
		target = strings.TrimRight(p.BaseURL, "/") + "/"
	}
	method := p.ProbeMethod
	if method == "" {
		method = http.MethodGet
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, method, target, nil)
	if err != nil {
		return Result{Err: sanitizeError(err)}
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "shunt-probe/1")
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		timeout := isTimeout(err)
		return Result{LatencyMS: elapsed.Milliseconds(), Err: sanitizeError(err), Timeout: timeout}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	res := Result{
		Status:    resp.StatusCode,
		LatencyMS: elapsed.Milliseconds(),
	}
	if resp.StatusCode >= 500 {
		res.Err = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return res
	}
	res.OK = true
	return res
}

// ProbeURL validates and returns the probe target for a profile.
func ProbeURL(p config.Profile) (string, error) {
	target := p.ProbeURL
	if target == "" {
		target = strings.TrimRight(p.BaseURL, "/") + "/"
	}
	u, err := url.Parse(target)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return "", fmt.Errorf("invalid probe URL")
	}
	return target, nil
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	msg = strings.ReplaceAll(msg, "\n", " ")
	msg = strings.ReplaceAll(msg, "\r", " ")
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

// Tracker maintains per-profile hysteresis counters and produces runtime
// health records. It is safe for concurrent use.
type Tracker struct {
	mu                sync.Mutex
	health            map[string]*healthState
	failureThreshold  int
	recoveryThreshold int
}

type healthState struct {
	record runtime.ProfileHealth
}

// NewTracker initializes health records from the configured profiles.
func NewTracker(profiles map[string]config.Profile, failureThreshold, recoveryThreshold int) *Tracker {
	if failureThreshold < 1 {
		failureThreshold = 3
	}
	if recoveryThreshold < 1 {
		recoveryThreshold = 2
	}
	t := &Tracker{
		health:            make(map[string]*healthState, len(profiles)),
		failureThreshold:  failureThreshold,
		recoveryThreshold: recoveryThreshold,
	}
	for id, p := range profiles {
		t.health[id] = &healthState{record: runtime.ProfileHealth{
			BaseURL: p.BaseURL,
			Enabled: p.IsEnabled(),
		}}
	}
	return t
}

// Observe records a probe or transport result.
func (t *Tracker) Observe(id string, res Result) {
	t.mu.Lock()
	defer t.mu.Unlock()
	state, ok := t.health[id]
	if !ok {
		state = &healthState{}
		t.health[id] = state
	}
	now := time.Now().UTC()
	state.record.LastProbeAt = &now
	latency := res.LatencyMS
	state.record.LastProbeMS = &latency
	if res.Status != 0 {
		status := res.Status
		state.record.LastStatus = &status
	}
	if res.OK {
		state.record.ConsecutiveSuccesses++
		state.record.ConsecutiveFailures = 0
		if state.record.ConsecutiveSuccesses >= t.recoveryThreshold || state.record.Healthy == nil {
			healthy := true
			state.record.Healthy = &healthy
		}
		state.record.LastError = nil
		return
	}
	state.record.ConsecutiveFailures++
	state.record.ConsecutiveSuccesses = 0
	if res.Err != "" {
		msg := res.Err
		state.record.LastError = &msg
	}
	if state.record.ConsecutiveFailures >= t.failureThreshold || state.record.Healthy == nil {
		healthy := false
		state.record.Healthy = &healthy
	}
}

// RecordTransportFailure counts a connect, DNS, or TLS failure observed on a
// real request. HTTP status codes never reach this method.
func (t *Tracker) RecordTransportFailure(id string, err error) {
	t.Observe(id, Result{Err: sanitizeError(err)})
}

// RecordSuccess marks a real request as a transport success.
func (t *Tracker) RecordSuccess(id string, status int, latency time.Duration) {
	t.Observe(id, Result{OK: true, Status: status, LatencyMS: latency.Milliseconds()})
}

// Healthy reports whether a profile is currently considered healthy. A nil
// health record means "unknown", which callers treat as usable.
func (t *Tracker) Healthy(id string) (healthy bool, known bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	state, ok := t.health[id]
	if !ok || state.record.Healthy == nil {
		return true, false
	}
	return *state.record.Healthy, true
}

// IsHealthy returns true when the profile is healthy or not yet probed.
func (t *Tracker) IsHealthy(id string) bool {
	h, _ := t.Healthy(id)
	return h
}

// Snapshot returns a deep copy of all health records.
func (t *Tracker) Snapshot() map[string]runtime.ProfileHealth {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]runtime.ProfileHealth, len(t.health))
	for id, state := range t.health {
		rec := state.record
		if rec.Healthy != nil {
			v := *rec.Healthy
			rec.Healthy = &v
		}
		if rec.LastProbeAt != nil {
			v := *rec.LastProbeAt
			rec.LastProbeAt = &v
		}
		if rec.LastProbeMS != nil {
			v := *rec.LastProbeMS
			rec.LastProbeMS = &v
		}
		if rec.LastStatus != nil {
			v := *rec.LastStatus
			rec.LastStatus = &v
		}
		if rec.LastError != nil {
			v := *rec.LastError
			rec.LastError = &v
		}
		out[id] = rec
	}
	return out
}

// UpdateProfile refreshes the base URL and enabled flag after a config reload.
func (t *Tracker) UpdateProfile(id, baseURL string, enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	state, ok := t.health[id]
	if !ok {
		t.health[id] = &healthState{record: runtime.ProfileHealth{BaseURL: baseURL, Enabled: enabled}}
		return
	}
	state.record.BaseURL = baseURL
	state.record.Enabled = enabled
}

// Retain drops health records for profiles that no longer exist, so a removed
// profile cannot linger in status output.
func (t *Tracker) Retain(profiles map[string]config.Profile) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id := range t.health {
		if _, ok := profiles[id]; !ok {
			delete(t.health, id)
		}
	}
}
