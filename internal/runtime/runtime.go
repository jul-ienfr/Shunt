package runtime

import (
	"context"
	"os"
	"sync"
	"time"

	"shunt/internal/platform"
)

const Schema = 1

// SwitchEvent describes the last effective-profile change.
type SwitchEvent struct {
	At     time.Time `json:"at"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Reason string    `json:"reason"`
	By     string    `json:"by"`
}

// ProfileHealth is the observable health of one upstream profile.
type ProfileHealth struct {
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

// ListenerState is one entry port and the upstream it currently routes to.
// Profile is the configured pin (empty when the port follows the global
// selection); Effective is the resolved profile for the next request.
type ListenerState struct {
	Name      string `json:"name"`
	Listen    string `json:"listen"`
	Profile   string `json:"profile"`
	Effective string `json:"effective"`
	Pinned    bool   `json:"pinned"`
}

// Snapshot is the runtime observation returned by /_shunt/status and persisted
// to runtime.json.
type Snapshot struct {
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
	LastSwitch    *SwitchEvent             `json:"last_switch"`
	Listeners     []ListenerState          `json:"listeners"`
	Profiles      map[string]ProfileHealth `json:"profiles"`
}

// Store owns the mutable runtime observation and serializes writes.
type Store struct {
	mu      sync.Mutex
	path    string
	snap    Snapshot
	started time.Time
	writes  chan struct{}
}

// New creates a runtime store for a process.
func New(path, version, listen string, profiles map[string]ProfileHealth) *Store {
	now := time.Now().UTC()
	s := &Store{
		path:    path,
		started: now,
		writes:  make(chan struct{}, 1),
	}
	s.snap = Snapshot{
		Schema:    Schema,
		Version:   version,
		PID:       os.Getpid(),
		StartedAt: now,
		Listen:    listen,
		Mode:      "manual",
		Profiles:  map[string]ProfileHealth{},
	}
	for id, h := range profiles {
		s.snap.Profiles[id] = h
	}
	return s
}

// Path returns the runtime file path.
func (s *Store) Path() string { return s.path }

// Snapshot returns a deep copy of the current observation.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copyLocked()
}

// Update mutates the snapshot under the lock and signals the writer.
func (s *Store) Update(fn func(*Snapshot)) {
	s.mu.Lock()
	fn(&s.snap)
	if s.started.IsZero() {
		s.started = s.snap.StartedAt
	}
	s.snap.UptimeS = int64(time.Since(s.started).Seconds())
	s.mu.Unlock()
	s.signal()
}

func (s *Store) signal() {
	select {
	case s.writes <- struct{}{}:
	default:
	}
}

// Write persists the current snapshot atomically.
func (s *Store) Write() error {
	s.mu.Lock()
	snap := s.copyLocked()
	s.mu.Unlock()
	return platform.WriteJSON(s.path, snap)
}

// Run writes on change and at least every interval until ctx is cancelled.
func (s *Store) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = s.Write()
			return
		case <-s.writes:
			_ = s.Write()
		case <-ticker.C:
			_ = s.Write()
		}
	}
}

func (s *Store) copyLocked() Snapshot {
	out := s.snap
	out.UptimeS = int64(time.Since(s.started).Seconds())
	if out.StartedAt.IsZero() {
		out.StartedAt = s.started
	}
	if out.Profiles != nil {
		profiles := make(map[string]ProfileHealth, len(out.Profiles))
		for id, h := range out.Profiles {
			nh := h
			if h.Healthy != nil {
				v := *h.Healthy
				nh.Healthy = &v
			}
			if h.LastProbeAt != nil {
				v := *h.LastProbeAt
				nh.LastProbeAt = &v
			}
			if h.LastProbeMS != nil {
				v := *h.LastProbeMS
				nh.LastProbeMS = &v
			}
			if h.LastStatus != nil {
				v := *h.LastStatus
				nh.LastStatus = &v
			}
			if h.LastError != nil {
				v := *h.LastError
				nh.LastError = &v
			}
			profiles[id] = nh
		}
		out.Profiles = profiles
	}
	if out.LastSwitch != nil {
		ls := *out.LastSwitch
		out.LastSwitch = &ls
	}
	if out.Listeners != nil {
		out.Listeners = append([]ListenerState(nil), out.Listeners...)
	}
	if out.Selected != nil {
		v := *out.Selected
		out.Selected = &v
	}
	if out.Preferred != nil {
		v := *out.Preferred
		out.Preferred = &v
	}
	if out.LastGood != nil {
		v := *out.LastGood
		out.LastGood = &v
	}
	return out
}
