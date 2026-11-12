package integration

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"shunt/test/support"
)

func soakInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func TestSoakRequestsAndSwitches(t *testing.T) {
	if os.Getenv("SHUNT_SOAK") != "1" {
		t.Skip("set SHUNT_SOAK=1 to run the soak test")
	}
	requests := soakInt("SHUNT_SOAK_REQUESTS", 10000)
	switches := soakInt("SHUNT_SOAK_SWITCHES", 200)
	streams := soakInt("SHUNT_SOAK_STREAMS", 50)
	if switches > requests {
		switches = requests
	}
	const chunks = 40

	var expected strings.Builder
	for i := 0; i < chunks; i++ {
		fmt.Fprintf(&expected, "%d", i)
	}
	wantStream := expected.String()

	streamHandler := func(id string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/stream" {
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, id)
				return
			}
			flusher := w.(http.Flusher)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			for i := 0; i < chunks; i++ {
				fmt.Fprintf(w, "%d", i)
				flusher.Flush()
				time.Sleep(20 * time.Millisecond)
			}
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
	client := directClient()
	client.Timeout = 30 * time.Second

	type streamResult struct {
		body string
		err  error
	}
	results := make(chan streamResult, streams)
	var streamWG sync.WaitGroup
	for i := 0; i < streams; i++ {
		streamWG.Add(1)
		go func() {
			defer streamWG.Done()
			resp, err := client.Get(relay.URL("/stream"))
			if err != nil {
				results <- streamResult{err: err}
				return
			}
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			results <- streamResult{body: string(body), err: readErr}
		}()
	}

	start := time.Now()
	perSwitch := requests / switches
	profile := "local"
	for i := 0; i < requests; i++ {
		if i%perSwitch == 0 {
			profile = "remote"
			if (i/perSwitch)%2 == 0 {
				profile = "local"
			}
			if _, err := relay.Server.SwitchProfile(profile, false, false, "soak", "soak"); err != nil {
				t.Fatalf("switch %d: %v", i/perSwitch, err)
			}
		}
		resp, err := client.Get(relay.URL("/who"))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("request %d read: %v", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d status = %d", i, resp.StatusCode)
		}
		if got := string(body); got != "local" && got != "remote" {
			t.Fatalf("request %d body = %q", i, got)
		}
	}
	elapsed := time.Since(start)

	streamWG.Wait()
	close(results)
	for res := range results {
		if res.err != nil {
			t.Fatalf("soak stream: %v", res.err)
		}
		if res.body != wantStream {
			t.Fatalf("soak stream truncated: got %d bytes, want %d", len(res.body), len(wantStream))
		}
	}

	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	t.Logf("soak: %d requests, %d switches, %d streams in %s (heap_alloc=%d bytes, sys=%d bytes)",
		requests, switches, streams, elapsed, stats.HeapAlloc, stats.Sys)
}
