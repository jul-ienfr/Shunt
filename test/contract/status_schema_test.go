package contract

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"shunt/internal/upstream"
	"shunt/test/support"
)

type statusDoc struct {
	Schema        int                       `json:"schema"`
	Version       string                    `json:"version"`
	PID           int                       `json:"pid"`
	StartedAt     time.Time                 `json:"started_at"`
	UptimeS       int64                     `json:"uptime_s"`
	Listen        string                    `json:"listen"`
	Mode          string                    `json:"mode"`
	Selected      *string                   `json:"selected"`
	Preferred     *string                   `json:"preferred"`
	Effective     string                    `json:"effective"`
	LastGood      *string                   `json:"last_good"`
	Inflight      int                       `json:"inflight"`
	RequestsTotal int64                     `json:"requests_total"`
	ErrorsTotal   int64                     `json:"errors_total"`
	SwitchesTotal int64                     `json:"switches_total"`
	BytesIn       int64                     `json:"bytes_in"`
	BytesOut      int64                     `json:"bytes_out"`
	LastSwitch    *map[string]any           `json:"last_switch"`
	Profiles      map[string]map[string]any `json:"profiles"`
}

func TestStatusSchema(t *testing.T) {
	fake := support.New(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	defer fake.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": fake.Origin(), "remote": fake.Origin()},
		Selected: "local",
	})

	req, err := http.NewRequest(http.MethodGet, relay.URL("/proxied"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer super-secret-value")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxied request: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	resp, err = http.Get(relay.URL("/_shunt/status"))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q", got)
	}

	var doc statusDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if doc.Schema != 1 {
		t.Fatalf("schema = %d", doc.Schema)
	}
	if doc.Version != "0.1.0-test" {
		t.Fatalf("version = %q", doc.Version)
	}
	if doc.PID <= 0 {
		t.Fatalf("pid = %d", doc.PID)
	}
	if doc.StartedAt.IsZero() {
		t.Fatal("started_at missing")
	}
	if doc.Listen != relay.Listen {
		t.Fatalf("listen = %q, want %q", doc.Listen, relay.Listen)
	}
	if doc.Mode != "manual" {
		t.Fatalf("mode = %q", doc.Mode)
	}
	if doc.Selected == nil || *doc.Selected != "local" {
		t.Fatalf("selected = %v", doc.Selected)
	}
	if doc.Effective != "local" {
		t.Fatalf("effective = %q", doc.Effective)
	}
	if doc.LastGood == nil || *doc.LastGood != "local" {
		t.Fatalf("last_good = %v", doc.LastGood)
	}
	if doc.RequestsTotal < 1 {
		t.Fatalf("requests_total = %d", doc.RequestsTotal)
	}
	if doc.BytesOut <= 0 {
		t.Fatalf("bytes_out = %d", doc.BytesOut)
	}

	health, ok := doc.Profiles["local"]
	if !ok {
		t.Fatal("local profile missing from status")
	}
	for _, key := range []string{
		"base_url", "enabled", "healthy", "last_probe_at", "last_probe_ms",
		"last_status", "consecutive_failures", "consecutive_successes", "last_error",
	} {
		if _, ok := health[key]; !ok {
			t.Fatalf("profile health missing %q: %v", key, health)
		}
	}

	lower := strings.ToLower(string(raw))
	for _, secret := range []string{"super-secret-value", "authorization", "bearer"} {
		if strings.Contains(lower, secret) {
			t.Fatalf("status leaked %q: %s", secret, raw)
		}
	}
}

func TestHealthReadinessAndVersionEndpoints(t *testing.T) {
	fake := support.New(nil)
	defer fake.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles:              map[string]string{"local": fake.Origin(), "remote": fake.Origin()},
		Selected:              "local",
		AutoProbeIntervalMS:   600000,
		AutoFailureThreshold:  3,
		AutoRecoveryThreshold: 2,
	})

	resp, err := http.Get(relay.URL("/_shunt/healthz"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"ok":true`)) {
		t.Fatalf("healthz = %d %s", resp.StatusCode, body)
	}

	resp, err = http.Get(relay.URL("/_shunt/version"))
	if err != nil {
		t.Fatal(err)
	}
	var version struct {
		Version string `json:"version"`
		Schema  int    `json:"schema"`
	}
	support.DecodeJSON(t, resp, &version)
	if version.Version != "0.1.0-test" || version.Schema != 1 {
		t.Fatalf("version = %+v", version)
	}

	observe := func(ok bool) {
		for i := 0; i < 3; i++ {
			relay.Server.Tracker().Observe("local", upstream.Result{OK: ok, Status: 0})
			relay.Server.Tracker().Observe("remote", upstream.Result{OK: ok, Status: 0})
		}
	}
	observe(false)
	resp, err = http.Get(relay.URL("/_shunt/readyz"))
	if err != nil {
		t.Fatal(err)
	}
	var notReady struct {
		Ready  bool   `json:"ready"`
		Reason string `json:"reason"`
	}
	support.DecodeJSON(t, resp, &notReady)
	if resp.StatusCode != http.StatusServiceUnavailable || notReady.Ready || notReady.Reason != "no_healthy_upstream" {
		t.Fatalf("readyz not-ready = %d %+v", resp.StatusCode, notReady)
	}

	observe(true)
	resp, err = http.Get(relay.URL("/_shunt/readyz"))
	if err != nil {
		t.Fatal(err)
	}
	var ready struct {
		Ready bool `json:"ready"`
	}
	support.DecodeJSON(t, resp, &ready)
	if resp.StatusCode != http.StatusOK || !ready.Ready {
		t.Fatalf("readyz ready = %d %+v", resp.StatusCode, ready)
	}

	resp, err = http.Post(relay.URL("/_shunt/status"), "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("allow = %q", got)
	}
}
