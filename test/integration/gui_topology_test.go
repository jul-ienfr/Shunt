package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"shunt/test/support"
)

type topologyError struct {
	Error struct {
		Code  string `json:"code"`
		Field string `json:"field"`
	} `json:"error"`
}

func decodeTopologyError(t *testing.T, data []byte) topologyError {
	t.Helper()
	var doc topologyError
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode error document: %v: %s", err, data)
	}
	return doc
}

func listenerNames(relay *support.Relay) []string {
	names := make([]string, 0, len(relay.Server.ListenerStates()))
	for _, state := range relay.Server.ListenerStates() {
		names = append(names, state.Name)
	}
	return names
}

// tryBody fetches a target without failing the test, for use inside WaitFor.
func tryBody(target string) (string, bool) {
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second}
	resp, err := client.Get(target)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	var b strings.Builder
	if _, err := io.Copy(&b, resp.Body); err != nil {
		return "", false
	}
	return b.String(), true
}

// TestGUIRemovesReferencedProfile retargets the default, switch aliases, auto
// preference, and durable selection when the profile they name is deleted.
func TestGUIRemovesReferencedProfile(t *testing.T) {
	local := support.New(identityHandler("local"))
	defer local.Close()
	remote := support.New(identityHandler("remote"))
	defer remote.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles:       map[string]string{"local": local.Origin(), "remote": remote.Origin()},
		DefaultProfile: "local",
		Selected:       "local",
	})
	client := directClient()

	resp, data := guiPost(t, client, relay.Origin(), "/_shunt/api/profiles/local/remove", `{}`, relay.UI.Token())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove default profile status = %d: %s", resp.StatusCode, data)
	}
	cfg := relay.Server.Config()
	if _, ok := cfg.Profiles["local"]; ok {
		t.Fatal("removed profile is still active")
	}
	if cfg.DefaultProfile != "remote" || cfg.Switch.OffProfile != "remote" || cfg.Auto.Preferred != "remote" {
		t.Fatalf("references were not retargeted: default=%q off=%q auto=%q", cfg.DefaultProfile, cfg.Switch.OffProfile, cfg.Auto.Preferred)
	}
	intent := relay.Server.State()
	if intent.Selected == nil || *intent.Selected != "remote" {
		t.Fatalf("selection was not retargeted: %+v", intent.Selected)
	}
	support.WaitFor(t, 2*time.Second, func() bool {
		body, ok := tryBody(relay.URL("/who"))
		return ok && body == "remote"
	})

	resp, data = guiPost(t, client, relay.Origin(), "/_shunt/api/profiles/remote/remove", `{}`, relay.UI.Token())
	if doc := decodeTopologyError(t, data); resp.StatusCode != http.StatusConflict || doc.Error.Code != "profile_in_use" {
		t.Fatalf("last profile removal = %d %+v", resp.StatusCode, doc)
	}
	if got := fetchBody(t, relay.URL("/who")); got != "remote" {
		t.Fatalf("upstream after refused removal = %q", got)
	}
}

// TestGUITopologyAddsProfileAndPort covers the dashboard workflow for routing a
// new local port to a new upstream profile.
func TestGUITopologyAddsProfileAndPort(t *testing.T) {
	local := support.New(identityHandler("local"))
	defer local.Close()
	remote := support.New(identityHandler("remote"))
	defer remote.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": local.Origin()},
		Selected: "local",
	})
	client := directClient()
	token := relay.UI.Token()

	// Add a profile that points at the second upstream.
	remoteURL := strings.TrimPrefix(remote.Origin(), "http://")
	host, port, err := net.SplitHostPort(remoteURL)
	if err != nil {
		t.Fatalf("split upstream: %v", err)
	}
	body := fmt.Sprintf(`{"id":"remote","label":"Remote","scheme":"http","host":%q,"port":%s}`, host, port)
	resp, data := guiPost(t, client, relay.Origin(), "/_shunt/api/profiles", body, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add profile status = %d: %s", resp.StatusCode, data)
	}
	support.WaitFor(t, 2*time.Second, func() bool {
		_, ok := relay.Server.Config().Profiles["remote"]
		return ok
	})

	// Duplicate ids are refused without touching the file.
	resp, data = guiPost(t, client, relay.Origin(), "/_shunt/api/profiles", body, token)
	if doc := decodeTopologyError(t, data); resp.StatusCode == http.StatusOK || doc.Error.Code != "invalid_endpoint" {
		t.Fatalf("duplicate profile = %d %+v", resp.StatusCode, doc)
	}

	// Add a second entry port pinned to that profile. The legacy entry must be
	// materialized as "default" so the current port keeps serving.
	newPort := reservePort(t, "127.0.0.1")
	body = fmt.Sprintf(`{"name":"remote","ip":"127.0.0.1","port":%d,"profile":"remote"}`, newPort)
	resp, data = guiPost(t, client, relay.Origin(), "/_shunt/api/listeners", body, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add listener status = %d: %s", resp.StatusCode, data)
	}
	var added struct {
		Name    string `json:"name"`
		Listen  string `json:"listen"`
		URL     string `json:"url"`
		Applied bool   `json:"applied"`
	}
	if err := json.Unmarshal(data, &added); err != nil {
		t.Fatalf("decode listener: %v: %s", err, data)
	}
	if !added.Applied || added.Listen != net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", newPort)) {
		t.Fatalf("listener response = %+v", added)
	}
	if names := listenerNames(relay); len(names) != 2 || names[0] != "default" {
		t.Fatalf("listeners after add = %v", names)
	}
	if got := fetchBody(t, added.URL+"who"); got != "remote" {
		t.Fatalf("new port upstream = %q", got)
	}
	if got := fetchBody(t, relay.URL("/who")); got != "local" {
		t.Fatalf("existing port upstream = %q", got)
	}

	// The overview exposes every port to the dashboard.
	resp, data = guiGet(t, client, relay.Origin()+"/_shunt/api/overview")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("overview status = %d: %s", resp.StatusCode, data)
	}
	var overview struct {
		Listeners []struct {
			Name     string `json:"name"`
			Profile  string `json:"profile"`
			Explicit bool   `json:"explicit"`
		} `json:"listeners"`
	}
	if err := json.Unmarshal(data, &overview); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if len(overview.Listeners) != 2 || !overview.Listeners[0].Explicit || overview.Listeners[1].Profile != "remote" {
		t.Fatalf("overview listeners = %+v", overview.Listeners)
	}

	// A pinned profile cannot be removed while the port exists.
	resp, data = guiPost(t, client, relay.Origin(), "/_shunt/api/profiles/remote/remove", `{}`, token)
	if doc := decodeTopologyError(t, data); resp.StatusCode != http.StatusConflict || doc.Error.Code != "profile_in_use" {
		t.Fatalf("pinned profile removal = %d %+v", resp.StatusCode, doc)
	}

	// Move the default port; the old address stops serving.
	movedPort := reservePort(t, "127.0.0.1")
	body = fmt.Sprintf(`{"ip":"127.0.0.1","port":%d}`, movedPort)
	resp, data = guiPost(t, client, relay.Origin(), "/_shunt/api/listeners/default/listen", body, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("move listener status = %d: %s", resp.StatusCode, data)
	}
	movedURL := fmt.Sprintf("http://127.0.0.1:%d", movedPort)
	support.WaitFor(t, 2*time.Second, func() bool {
		body, ok := tryBody(movedURL + "/who")
		return ok && body == "local"
	})
	oldURL := relay.Origin()
	support.WaitFor(t, 2*time.Second, func() bool {
		_, ok := tryBody(oldURL + "/who")
		return !ok
	})

	// Remove the extra port and the profile it used.
	resp, data = guiPost(t, client, movedURL, "/_shunt/api/listeners/remote/remove", `{}`, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove listener status = %d: %s", resp.StatusCode, data)
	}
	if names := listenerNames(relay); len(names) != 1 || names[0] != "default" {
		t.Fatalf("listeners after remove = %v", names)
	}
	resp, data = guiPost(t, client, movedURL, "/_shunt/api/profiles/remote/remove", `{}`, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove profile status = %d: %s", resp.StatusCode, data)
	}
	if _, ok := relay.Server.Config().Profiles["remote"]; ok {
		t.Fatal("removed profile is still active")
	}
	if _, ok := relay.Server.Tracker().Snapshot()["remote"]; ok {
		t.Fatal("removed profile is still in the health snapshot")
	}

	// The last entry port cannot be removed.
	resp, data = guiPost(t, client, movedURL, "/_shunt/api/listeners/default/remove", `{}`, token)
	if doc := decodeTopologyError(t, data); resp.StatusCode != http.StatusUnprocessableEntity || doc.Error.Code != "invalid_entry" {
		t.Fatalf("last listener removal = %d %+v", resp.StatusCode, doc)
	}
	if got := fetchBody(t, movedURL+"/who"); got != "local" {
		t.Fatalf("listener after refused removal = %q", got)
	}
}
