package contract

import (
	"io"
	"net/http"
	"net/url"
	"testing"

	"shunt/internal/config"

	"shunt/test/support"
)

func TestRelayHeaderFiltering(t *testing.T) {
	upstream := support.New(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-End-To-End", "kept")
		w.Header().Set("X-Resp-Hop", "removed")
		w.Header().Set("Keep-Alive", "timeout=5")
		w.Header().Set("Connection", "X-Resp-Hop")
		w.Header().Set("Set-Cookie", "relay-test=1; Path=/")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	defer upstream.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})

	req, err := http.NewRequest(http.MethodGet, relay.URL("/v1/messages?beta=true&stream=1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer upstream-secret")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("X-Api-Key", "api-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "keep-alive, X-Hop-Request")
	req.Header.Set("X-Hop-Request", "remove-me")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("Proxy-Authorization", "Basic cHJveHk=")
	req.Header.Set("Te", "trailers")
	req.Header.Set("Expect", "100-continue")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-End-To-End"); got != "kept" {
		t.Fatalf("end-to-end response header = %q", got)
	}
	if got := resp.Header.Get("X-Resp-Hop"); got != "" {
		t.Fatalf("connection-named response header leaked: %q", got)
	}
	if got := resp.Header.Get("Keep-Alive"); got != "" {
		t.Fatalf("hop-by-hop response header leaked: %q", got)
	}
	if got := resp.Header.Get("X-Shunt-Profile"); got != "local" {
		t.Fatalf("profile header = %q", got)
	}
	if got := resp.Header.Get("X-Shunt-Request-Id"); got == "" {
		t.Fatal("missing request id header")
	}

	got, ok := upstream.LastRequest()
	if !ok {
		t.Fatal("upstream received no request")
	}
	if got.Method != http.MethodGet {
		t.Fatalf("method = %q", got.Method)
	}
	if got.Path != "/v1/messages" {
		t.Fatalf("path = %q", got.Path)
	}
	if got.RawQuery != "beta=true&stream=1" {
		t.Fatalf("query = %q", got.RawQuery)
	}
	if got.Header.Get("X-Hop-Request") != "" {
		t.Fatal("Connection-named request header leaked upstream")
	}
	if got.Header.Get("Keep-Alive") != "" || got.Header.Get("Proxy-Authorization") != "" || got.Header.Get("Te") != "" {
		t.Fatalf("hop-by-hop request headers leaked: %v", got.Header)
	}
	if got.Header.Get("Authorization") != "Bearer upstream-secret" {
		t.Fatalf("authorization = %q", got.Header.Get("Authorization"))
	}
	if got.Header.Get("X-Api-Key") != "api-secret" {
		t.Fatalf("x-api-key = %q", got.Header.Get("X-Api-Key"))
	}
	if got.Header.Get("Anthropic-Version") != "2023-06-01" {
		t.Fatalf("protocol header = %q", got.Header.Get("Anthropic-Version"))
	}
	if got.Header.Get("X-Forwarded-For") != "" {
		t.Fatalf("X-Forwarded-For was added: %q", got.Header.Get("X-Forwarded-For"))
	}
	if got.Header.Get("X-Shunt-Profile") != "" || got.Header.Get("X-Shunt-Request-Id") != "" {
		t.Fatalf("trace headers sent without trace_headers: %v", got.Header)
	}
	upstreamURL, _ := url.Parse(upstream.Origin())
	if got.Host != upstreamURL.Host {
		t.Fatalf("upstream host = %q, want %q", got.Host, upstreamURL.Host)
	}
}

func TestRelayTraceHeadersWhenEnabled(t *testing.T) {
	upstream := support.New(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	defer upstream.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})
	relay.UpdateConfig(t, func(cfg *config.Config) {
		cfg.Relay.TraceHeaders = true
	})

	resp, err := http.Get(relay.URL("/trace"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	got, ok := upstream.LastRequest()
	if !ok {
		t.Fatal("upstream received no request")
	}
	if got.Header.Get("X-Shunt-Profile") != "local" {
		t.Fatalf("trace profile header = %q", got.Header.Get("X-Shunt-Profile"))
	}
	if got.Header.Get("X-Shunt-Request-Id") == "" {
		t.Fatal("missing trace request id")
	}
}
