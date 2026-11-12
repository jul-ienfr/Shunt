package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"shunt/test/support"
)

// directClient avoids proxies so loopback and IPv6 requests stay local.
func directClient() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func guiPost(t *testing.T, client *http.Client, origin, path, body, token string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, origin+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Shunt-UI-Token", token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, data
}

func guiGet(t *testing.T, client *http.Client, target string) (*http.Response, []byte) {
	t.Helper()
	resp, err := client.Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, data
}

func reservePort(t *testing.T, host string) int {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Skipf("cannot reserve %s port: %v", host, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if port < 1024 {
		t.Skipf("ephemeral port %d is below the configured minimum", port)
	}
	return port
}

func TestGUIRebindNextRequestFollowsEntry(t *testing.T) {
	upstream := support.New(identityHandler("upstream"))
	defer upstream.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})
	client := directClient()
	current := relay.Origin()

	for i := 0; i < 20; i++ {
		port := reservePort(t, "127.0.0.1")
		body := fmt.Sprintf(`{"ip":"127.0.0.1","port":%d}`, port)
		start := time.Now()
		resp, data := guiPost(t, client, current, "/_shunt/api/entry", body, relay.UI.Token())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("rebind %d status = %d: %s", i, resp.StatusCode, data)
		}
		var doc struct {
			Schema     int    `json:"schema"`
			Listen     string `json:"listen"`
			URL        string `json:"url"`
			Generation int    `json:"generation"`
			Rebound    bool   `json:"rebound"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("decode rebind %d: %v: %s", i, err, data)
		}
		if doc.Schema != 1 || !doc.Rebound || doc.Generation < 1 {
			t.Fatalf("rebind %d response = %+v", i, doc)
		}
		if doc.Listen != net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)) {
			t.Fatalf("rebind %d listen = %q", i, doc.Listen)
		}

		next := "http://" + doc.Listen
		deadline := time.Now().Add(time.Second)
		for {
			health, _ := guiGet(t, client, next+"/_shunt/healthz")
			if health.StatusCode == http.StatusOK {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("rebind %d: new entry was not ready within 1s (status %d)", i, health.StatusCode)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("rebind %d took %s", i, elapsed)
		}

		got := fetchBody(t, next+"/who")
		if got != "upstream" {
			t.Fatalf("rebind %d: proxied request = %q", i, got)
		}
		page, pageBody := guiGet(t, client, doc.URL)
		if page.StatusCode != http.StatusOK {
			t.Fatalf("rebind %d: page status = %d", i, page.StatusCode)
		}
		if !strings.Contains(string(pageBody), relay.UI.Token()) {
			t.Fatalf("rebind %d: page token was not injected", i)
		}
		scriptResp, script := guiGet(t, client, next+"/_shunt/ui/app.js")
		if scriptResp.StatusCode != http.StatusOK {
			t.Fatalf("rebind %d: app.js status = %d", i, scriptResp.StatusCode)
		}
		if !strings.Contains(string(script), "window.location.href") {
			t.Fatalf("rebind %d: page has no reconnect script", i)
		}
		current = next
	}

	// IPv6 loopback is supported wherever the host allows binding it.
	if !supportsIPv6Loopback() {
		t.Log("IPv6 loopback is unavailable; skipping the IPv6 rebind")
		return
	}
	port := reservePort(t, "::1")
	body := fmt.Sprintf(`{"ip":"::1","port":%d}`, port)
	resp, data := guiPost(t, client, current, "/_shunt/api/entry", body, relay.UI.Token())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("IPv6 rebind status = %d: %s", resp.StatusCode, data)
	}
	var doc struct {
		Listen string `json:"listen"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode IPv6 rebind: %v", err)
	}
	if doc.Listen != net.JoinHostPort("::1", fmt.Sprintf("%d", port)) {
		t.Fatalf("IPv6 listen = %q", doc.Listen)
	}
	next := "http://" + doc.Listen
	support.WaitFor(t, time.Second, func() bool {
		health, _ := guiGet(t, client, next+"/_shunt/healthz")
		return health.StatusCode == http.StatusOK
	})
	if got := fetchBody(t, next+"/who"); got != "upstream" {
		t.Fatalf("IPv6 proxied request = %q", got)
	}
}

func supportsIPv6Loopback() bool {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func TestGUIProfileEndpointChangeKeepsLiveStream(t *testing.T) {
	const first = "alpha-"
	const rest = "omega"

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	old := support.New(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" {
			w.WriteHeader(http.StatusOK)
			return
		}
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, first)
		flusher.Flush()
		started <- struct{}{}
		<-release
		_, _ = io.WriteString(w, rest)
		flusher.Flush()
	})
	defer old.Close()
	replacement := support.New(identityHandler("replacement"))
	defer replacement.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": old.Origin()},
		Selected: "local",
	})
	client := directClient()

	resp, err := client.Get(relay.URL("/stream"))
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer resp.Body.Close()
	firstBytes := make([]byte, len(first))
	if _, err := io.ReadFull(resp.Body, firstBytes); err != nil {
		t.Fatalf("read first chunk: %v", err)
	}
	if string(firstBytes) != first {
		t.Fatalf("first chunk = %q", firstBytes)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not start")
	}

	parsed, err := url.Parse(replacement.Origin())
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"scheme":"%s","host":"%s","port":%s}`, parsed.Scheme, host, portText)
	applyResp, applyBody := guiPost(t, client, relay.Origin(), "/_shunt/api/profiles/local/endpoint", body, relay.UI.Token())
	if applyResp.StatusCode != http.StatusOK {
		t.Fatalf("endpoint change status = %d: %s", applyResp.StatusCode, applyBody)
	}
	var applied struct {
		Profile string `json:"profile"`
		BaseURL string `json:"base_url"`
		Applied bool   `json:"applied"`
	}
	if err := json.Unmarshal(applyBody, &applied); err != nil {
		t.Fatalf("decode endpoint response: %v", err)
	}
	if !applied.Applied || applied.Profile != "local" || applied.BaseURL != replacement.Origin() {
		t.Fatalf("endpoint response = %+v", applied)
	}

	close(release)
	tail, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream tail: %v", err)
	}
	if string(tail) != rest {
		t.Fatalf("stream truncated: got %q, want %q", tail, rest)
	}
	if got := fetchBody(t, relay.URL("/who")); got != "replacement" {
		t.Fatalf("next request used %q", got)
	}
}

func TestGUIEntryRejectionsAndFailurePreservesListener(t *testing.T) {
	upstream := support.New(identityHandler("upstream"))
	defer upstream.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})
	client := directClient()

	type apiError struct {
		Error struct {
			Code    string `json:"code"`
			Field   string `json:"field"`
			Current string `json:"current"`
		} `json:"error"`
	}

	cases := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{name: "wildcard", body: `{"ip":"0.0.0.0","port":9000}`, status: http.StatusUnprocessableEntity, code: "invalid_entry"},
		{name: "wildcard v6", body: `{"ip":"::","port":9000}`, status: http.StatusUnprocessableEntity, code: "invalid_entry"},
		{name: "unconfirmed LAN", body: `{"ip":"10.255.255.254","port":9000}`, status: http.StatusForbidden, code: "lan_not_confirmed"},
		{name: "public address", body: `{"ip":"8.8.8.8","port":9000,"confirm_lan":true}`, status: http.StatusUnprocessableEntity, code: "invalid_entry"},
		{name: "privileged port", body: `{"ip":"127.0.0.1","port":80}`, status: http.StatusUnprocessableEntity, code: "invalid_entry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, data := guiPost(t, client, relay.Origin(), "/_shunt/api/entry", tc.body, relay.UI.Token())
			var doc apiError
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatalf("decode: %v: %s", err, data)
			}
			if resp.StatusCode != tc.status || doc.Error.Code != tc.code {
				t.Fatalf("got %d %+v, want %d %s", resp.StatusCode, doc, tc.status, tc.code)
			}
			if got := fetchBody(t, relay.URL("/who")); got != "upstream" {
				t.Fatalf("rejected change disturbed the listener: %q", got)
			}
		})
	}

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port
	resp, data := guiPost(t, client, relay.Origin(), "/_shunt/api/entry",
		fmt.Sprintf(`{"ip":"127.0.0.1","port":%d}`, busyPort), relay.UI.Token())
	var doc apiError
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode bind failure: %v: %s", err, data)
	}
	if resp.StatusCode != http.StatusConflict || doc.Error.Code != "bind_failed" {
		t.Fatalf("bind failure = %d %+v", resp.StatusCode, doc)
	}
	if doc.Error.Current != relay.Listen {
		t.Fatalf("bind failure current = %q, want %q", doc.Error.Current, relay.Listen)
	}
	if got := fetchBody(t, relay.URL("/who")); got != "upstream" {
		t.Fatalf("failed bind disturbed the listener: %q", got)
	}

	if err := os.Remove(relay.ConfigPath); err != nil {
		t.Fatalf("remove config: %v", err)
	}
	port := reservePort(t, "127.0.0.1")
	resp, data = guiPost(t, client, relay.Origin(), "/_shunt/api/entry",
		fmt.Sprintf(`{"ip":"127.0.0.1","port":%d}`, port), relay.UI.Token())
	doc = apiError{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode write failure: %v: %s", err, data)
	}
	if resp.StatusCode != http.StatusInternalServerError || doc.Error.Code != "config_write_failed" {
		t.Fatalf("config write failure = %d %+v", resp.StatusCode, doc)
	}
	if got := fetchBody(t, relay.URL("/who")); got != "upstream" {
		t.Fatalf("config write failure disturbed the listener: %q", got)
	}
}
