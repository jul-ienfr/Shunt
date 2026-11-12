package integration

import (
	"io"
	"net/http"
	"testing"
	"time"

	"shunt/internal/config"
	"shunt/test/support"
)

// TestMultiListenerPinnedAndGlobal checks that one port follows the global
// switch while a pinned port keeps its own upstream.
func TestMultiListenerPinnedAndGlobal(t *testing.T) {
	local := support.New(identityHandler("local"))
	defer local.Close()
	remote := support.New(identityHandler("remote"))
	defer remote.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": local.Origin(), "remote": remote.Origin()},
		Selected: "local",
		Listeners: []support.ListenerOption{
			{Name: "main"},
			{Name: "pinned", Profile: "remote"},
		},
	})
	mainURL := relay.ListenerURL("main")
	pinnedURL := relay.ListenerURL("pinned")
	if mainURL == "" || pinnedURL == "" {
		t.Fatalf("listener addresses = %q, %q", mainURL, pinnedURL)
	}

	if got := fetchBody(t, mainURL+"/who"); got != "local" {
		t.Fatalf("main initial upstream = %q, want local", got)
	}
	if got := fetchBody(t, pinnedURL+"/who"); got != "remote" {
		t.Fatalf("pinned initial upstream = %q, want remote", got)
	}

	switchState(t, relay, "remote")
	if got := fetchBody(t, mainURL+"/who"); got != "remote" {
		t.Fatalf("main after switch = %q, want remote", got)
	}
	if got := fetchBody(t, pinnedURL+"/who"); got != "remote" {
		t.Fatalf("pinned after switch = %q, want remote", got)
	}

	switchState(t, relay, "local")
	if got := fetchBody(t, mainURL+"/who"); got != "local" {
		t.Fatalf("main after switch back = %q, want local", got)
	}
	if got := fetchBody(t, pinnedURL+"/who"); got != "remote" {
		t.Fatalf("pinned followed the global switch: got %q, want remote", got)
	}

	states := relay.Server.ListenerStates()
	if len(states) != 2 {
		t.Fatalf("listener states = %d, want 2", len(states))
	}
	byName := map[string]bool{}
	for _, state := range states {
		byName[state.Name] = state.Pinned
	}
	if byName["main"] {
		t.Fatal("main listener reported as pinned")
	}
	if !byName["pinned"] {
		t.Fatal("pinned listener not reported as pinned")
	}
}

// TestListenerSetApplyAddsAndRetiresPorts covers hot addition and removal of an
// entry port through configuration reload.
func TestListenerSetApplyAddsAndRetiresPorts(t *testing.T) {
	local := support.New(identityHandler("local"))
	defer local.Close()
	remote := support.New(identityHandler("remote"))
	defer remote.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles:  map[string]string{"local": local.Origin(), "remote": remote.Origin()},
		Selected:  "local",
		Listeners: []support.ListenerOption{{Name: "main"}},
	})
	mainURL := relay.ListenerURL("main")
	if got := fetchBody(t, mainURL+"/who"); got != "local" {
		t.Fatalf("main upstream = %q, want local", got)
	}

	extraAddr := support.ReserveLoopback(t)
	relay.UpdateConfig(t, func(cfg *config.Config) {
		cfg.Listeners = append(cfg.Listeners, config.Listener{
			Name:    "extra",
			Listen:  extraAddr,
			Profile: "remote",
		})
	})
	extraURL := "http://" + extraAddr
	support.WaitFor(t, 2*time.Second, func() bool {
		body, ok := tryFetch(extraURL + "/who")
		return ok && body == "remote"
	})

	relay.UpdateConfig(t, func(cfg *config.Config) {
		cfg.Listeners = cfg.Listeners[:1]
	})
	support.WaitFor(t, 2*time.Second, func() bool {
		resp, err := http.Get(extraURL + "/who")
		if err == nil {
			_ = resp.Body.Close()
			return false
		}
		return true
	})

	if got := fetchBody(t, mainURL+"/who"); got != "local" {
		t.Fatalf("main upstream after retire = %q, want local", got)
	}
}

// tryFetch returns the body and whether the request succeeded without failing
// the test, so it can run inside a polling loop.
func tryFetch(target string) (string, bool) {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(target)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false
	}
	return string(body), true
}
