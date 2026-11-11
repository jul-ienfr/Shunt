package integration

import (
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"shunt/internal/upstream"
	"shunt/test/support"
)

func toggleHandler(healthy *atomic.Bool, id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			if healthy.Load() {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			return
		}
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, id)
	}
}

func TestAutoFailoverAndRecovery(t *testing.T) {
	var localHealthy atomic.Bool
	localHealthy.Store(true)
	var remoteHealthy atomic.Bool
	remoteHealthy.Store(true)

	local := support.New(toggleHandler(&localHealthy, "local"))
	defer local.Close()
	remote := support.New(toggleHandler(&remoteHealthy, "remote"))
	defer remote.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles:              map[string]string{"local": local.Origin(), "remote": remote.Origin()},
		Mode:                  "auto",
		Preferred:             "local",
		Auto:                  true,
		AutoProbeIntervalMS:   100,
		AutoProbeTimeoutMS:    500,
		AutoFailureThreshold:  3,
		AutoRecoveryThreshold: 2,
	})

	support.WaitFor(t, 3*time.Second, func() bool {
		h, known := relay.Server.Tracker().Healthy("local")
		return known && h
	})
	if got := relay.Server.Selector().Load().Profile; got != "local" {
		t.Fatalf("initial effective profile = %q", got)
	}

	localHealthy.Store(false)
	support.WaitFor(t, 5*time.Second, func() bool {
		return relay.Server.Selector().Load().Profile == "remote"
	})
	if got := fetchBody(t, relay.URL("/who")); got != "remote" {
		t.Fatalf("request after failover = %q", got)
	}

	localHealthy.Store(true)
	support.WaitFor(t, 5*time.Second, func() bool {
		return relay.Server.Selector().Load().Profile == "local"
	})
	if got := fetchBody(t, relay.URL("/who")); got != "local" {
		t.Fatalf("request after recovery = %q", got)
	}
}

func TestAutoAllUnhealthyReturns503(t *testing.T) {
	local := support.New(identityHandler("local"))
	defer local.Close()
	remote := support.New(identityHandler("remote"))
	defer remote.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles:            map[string]string{"local": local.Origin(), "remote": remote.Origin()},
		Mode:                "auto",
		Preferred:           "local",
		Auto:                true,
		AutoProbeIntervalMS: 600000,
	})
	support.WaitFor(t, 3*time.Second, func() bool {
		_, localKnown := relay.Server.Tracker().Healthy("local")
		_, remoteKnown := relay.Server.Tracker().Healthy("remote")
		return localKnown && remoteKnown
	})
	for _, id := range []string{"local", "remote"} {
		for i := 0; i < 3; i++ {
			relay.Server.Tracker().Observe(id, upstream.Result{OK: false, Status: 0, Err: "dial failed"})
		}
	}
	if err := relay.Server.SetMode("auto", "local", "test"); err != nil {
		t.Fatalf("set auto: %v", err)
	}

	resp, err := http.Get(relay.URL("/check"))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	support.DecodeJSON(t, resp, &body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if body.Error.Code != "shunt_no_healthy_upstream" {
		t.Fatalf("code = %q", body.Error.Code)
	}

	for _, id := range []string{"local", "remote"} {
		for i := 0; i < 2; i++ {
			relay.Server.Tracker().Observe(id, upstream.Result{OK: true, Status: 200})
		}
	}
	if got := fetchBody(t, relay.URL("/after")); got == "" {
		t.Fatal("relay did not recover after the upstreams became healthy")
	}
}

func TestManualSelectionStopsAutoMovement(t *testing.T) {
	local := support.New(identityHandler("local"))
	defer local.Close()
	remote := support.New(identityHandler("remote"))
	defer remote.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles:            map[string]string{"local": local.Origin(), "remote": remote.Origin()},
		Mode:                "auto",
		Preferred:           "local",
		Auto:                true,
		AutoProbeIntervalMS: 100,
		AutoProbeTimeoutMS:  500,
	})
	if _, err := relay.Server.SwitchProfile("remote", false, false, "test", "manual override"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if got := relay.Server.State().Mode; got != "manual" {
		t.Fatalf("mode after manual switch = %q", got)
	}

	for i := 0; i < 5; i++ {
		relay.Server.Tracker().Observe("local", upstream.Result{OK: false, Status: 0})
	}
	time.Sleep(500 * time.Millisecond)
	if got := relay.Server.Selector().Load().Profile; got != "remote" {
		t.Fatalf("manual selection moved to %q", got)
	}
	if got := fetchBody(t, relay.URL("/who")); got != "remote" {
		t.Fatalf("request = %q", got)
	}
}
