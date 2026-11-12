package integration

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"shunt/internal/state"
	"shunt/internal/upstream"
	"shunt/test/support"
)

func identityHandler(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, id)
	}
}

func fetchBody(t *testing.T, target string) string {
	t.Helper()
	resp, err := http.Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	return string(body)
}

func switchState(t *testing.T, relay *support.Relay, profile string) time.Duration {
	t.Helper()
	start := time.Now()
	if _, err := state.Save(relay.StatePath, state.Select(profile, "test", "hot-switch")); err != nil {
		t.Fatalf("save state: %v", err)
	}
	support.WaitFor(t, time.Second, func() bool {
		return relay.Server.Selector().Load().Profile == profile
	})
	return time.Since(start)
}

func TestHotSwitchNextRequestFollowsSelection(t *testing.T) {
	local := support.New(identityHandler("local"))
	defer local.Close()
	remote := support.New(identityHandler("remote"))
	defer remote.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": local.Origin(), "remote": remote.Origin()},
		Selected: "local",
	})

	if got := fetchBody(t, relay.URL("/who")); got != "local" {
		t.Fatalf("initial upstream = %q", got)
	}
	for i := 0; i < 20; i++ {
		target := "remote"
		if i%2 == 1 {
			target = "local"
		}
		latency := switchState(t, relay, target)
		if latency > time.Second {
			t.Fatalf("switch %d took %s", i, latency)
		}
		if got := fetchBody(t, relay.URL("/who")); got != target {
			t.Fatalf("switch %d: upstream = %q, want %q", i, got, target)
		}
	}
	if relay.Listen == "" {
		t.Fatal("entry listener moved during upstream switches")
	}
}

func TestHotSwitchPinsInflightStreams(t *testing.T) {
	const (
		streams = 10
		chunks  = 12
	)
	var expected strings.Builder
	for i := 0; i < chunks; i++ {
		fmt.Fprintf(&expected, "%d", i)
	}
	want := expected.String()

	streamHandler := func(id string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/stream" {
				w.WriteHeader(http.StatusOK)
				return
			}
			flusher := w.(http.Flusher)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			for i := 0; i < chunks; i++ {
				fmt.Fprintf(w, "%d", i)
				flusher.Flush()
				time.Sleep(15 * time.Millisecond)
			}
			_ = id
		}
	}
	local := support.New(streamHandler("local"))
	defer local.Close()
	remote := support.New(streamHandler("remote"))
	defer remote.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": local.Origin(), "remote": remote.Origin()},
		Selected: "local",
	})

	type result struct {
		body string
		err  error
	}
	results := make(chan result, streams)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, err := http.Get(relay.URL("/stream"))
			if err != nil {
				results <- result{err: err}
				return
			}
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			results <- result{body: string(body), err: readErr}
		}()
	}
	close(start)
	support.WaitFor(t, 2*time.Second, func() bool {
		count := 0
		for _, req := range local.Requests() {
			if req.Path == "/stream" {
				count++
			}
		}
		return count == streams
	})

	for i := 0; i < 50; i++ {
		target := "remote"
		if i%2 == 1 {
			target = "local"
		}
		if _, err := relay.Server.SwitchProfile(target, false, false, "test", "hot-switch"); err != nil {
			t.Fatalf("switch %d: %v", i, err)
		}
	}

	wg.Wait()
	close(results)
	for res := range results {
		if res.err != nil {
			t.Fatalf("stream read: %v", res.err)
		}
		if res.body != want {
			t.Fatalf("stream truncated: got %q, want %q", res.body, want)
		}
	}
}

func TestManualUnhealthyDoesNotFallBack(t *testing.T) {
	local := support.New(identityHandler("local"))
	defer local.Close()
	remote := support.New(identityHandler("remote"))
	defer remote.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles:             map[string]string{"local": local.Origin(), "remote": remote.Origin()},
		Selected:             "local",
		AutoFailureThreshold: 3,
	})
	for i := 0; i < 3; i++ {
		relay.Server.Tracker().Observe("local", upstream.Result{OK: false, Status: 0, Err: "dial failed"})
	}
	if healthy, known := relay.Server.Tracker().Healthy("local"); !known || healthy {
		t.Fatalf("local health = %v/%v", healthy, known)
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
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if body.Error.Code != "shunt_upstream_unavailable" {
		t.Fatalf("code = %q", body.Error.Code)
	}
	for _, req := range remote.Requests() {
		if req.Path == "/check" {
			t.Fatal("manual mode silently failed over to the remote profile")
		}
	}
	if relay.Server.Selector().Load().Profile != "local" {
		t.Fatal("selection changed without an operator command")
	}
}
