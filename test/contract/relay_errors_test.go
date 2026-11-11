package contract

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"shunt/test/support"
)

type relayError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Code    string `json:"code"`
		Profile string `json:"profile"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

func decodeRelayError(t *testing.T, resp *http.Response) relayError {
	t.Helper()
	defer resp.Body.Close()
	var body relayError
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode relay error: %v", err)
	}
	if body.Type != "error" {
		t.Fatalf("top-level type = %q", body.Type)
	}
	if body.Error.Type != body.Error.Code {
		t.Fatalf("error types disagree: %+v", body.Error)
	}
	if body.RequestID == "" {
		t.Fatal("missing request_id")
	}
	return body
}

func TestRelayPassesUpstreamErrorsThrough(t *testing.T) {
	cases := []struct {
		status int
		body   string
	}{
		{status: http.StatusBadRequest, body: `{"error":{"type":"invalid_request_error","message":"bad"}}`},
		{status: http.StatusUnauthorized, body: `{"error":{"type":"authentication_error","message":"no"}}`},
		{status: http.StatusForbidden, body: `{"error":{"type":"permission_error","message":"forbidden"}}`},
		{status: http.StatusNotFound, body: `{"error":{"type":"not_found_error"}}`},
		{status: http.StatusUnprocessableEntity, body: `{"error":{"type":"invalid_request_error"}}`},
		{status: http.StatusTooManyRequests, body: `{"error":{"type":"rate_limit_error"}}`},
		{status: http.StatusInternalServerError, body: `{"error":{"type":"api_error","message":"upstream"}}`},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			upstream := support.New(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/" {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Upstream-Error", "yes")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			defer upstream.Close()

			relay := support.NewRelay(t, support.RelayOptions{
				Profiles: map[string]string{"local": upstream.Origin()},
				Selected: "local",
			})
			resp, err := http.Get(relay.URL("/v1/fail"))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			if string(body) != tc.body {
				t.Fatalf("body = %q, want %q", body, tc.body)
			}
			if resp.Header.Get("X-Upstream-Error") != "yes" {
				t.Fatal("upstream response header lost")
			}
			if resp.Header.Get("X-Shunt-Profile") != "local" {
				t.Fatalf("profile header = %q", resp.Header.Get("X-Shunt-Profile"))
			}
		})
	}
}

func TestRelayStructuredUnavailable(t *testing.T) {
	dead := deadOrigin(t)
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": dead},
		Selected: "local",
	})
	resp, err := http.Get(relay.URL("/v1/messages"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	body := decodeRelayError(t, resp)
	if body.Error.Code != "shunt_upstream_unavailable" {
		t.Fatalf("code = %q", body.Error.Code)
	}
	if body.Error.Profile != "local" {
		t.Fatalf("profile = %q", body.Error.Profile)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control = %q", resp.Header.Get("Cache-Control"))
	}
}

func TestRelayStructuredTimeout(t *testing.T) {
	upstream := support.New(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		time.Sleep(1500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	defer upstream.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles:                map[string]string{"local": upstream.Origin()},
		Selected:                "local",
		ResponseHeaderTimeoutMS: 1000,
	})
	resp, err := http.Get(relay.URL("/slow"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", resp.StatusCode)
	}
	body := decodeRelayError(t, resp)
	if body.Error.Code != "shunt_upstream_timeout" {
		t.Fatalf("code = %q", body.Error.Code)
	}
}

func TestRelayDoesNotRetryAfterForwardedBytes(t *testing.T) {
	chunk := "event: first\ndata: pinned\n\n"
	upstream := support.New(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		support.SSE([]string{chunk, "event: never\ndata: no\n\n"}, 0, true)(w, r)
	})
	defer upstream.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})
	resp, err := http.Get(relay.URL("/stream"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil && !strings.Contains(readErr.Error(), "unexpected EOF") {
		// A mid-stream disconnect may surface as EOF or an unexpected EOF.
		t.Logf("stream ended with %v", readErr)
	}
	if string(body) != chunk {
		t.Fatalf("received %q, want %q", body, chunk)
	}
	time.Sleep(100 * time.Millisecond)
	attempts := 0
	for _, req := range upstream.Requests() {
		if req.Path == "/stream" {
			attempts++
		}
	}
	if attempts != 1 {
		t.Fatalf("proxied attempts = %d, want 1", attempts)
	}
}

func TestRelayRejectsConnect(t *testing.T) {
	upstream := support.New(nil)
	defer upstream.Close()
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})
	req, err := http.NewRequest(http.MethodConnect, relay.URL("/"), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestRelayOverloadReturns503(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)

	started := make(chan struct{}, 1)
	upstream := support.New(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hold" {
			w.WriteHeader(http.StatusOK)
			return
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "held")
	})
	defer upstream.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles:    map[string]string{"local": upstream.Origin()},
		Selected:    "local",
		MaxInflight: 1,
	})

	firstDone := make(chan error, 1)
	go func() {
		resp, err := http.Get(relay.URL("/hold"))
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not reach the upstream")
	}

	resp, err := http.Get(relay.URL("/second"))
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	body := decodeRelayError(t, resp)
	if body.Error.Code != "shunt_overloaded" {
		t.Fatalf("code = %q", body.Error.Code)
	}

	unblock()
	if err := <-firstDone; err != nil {
		t.Fatalf("first request failed: %v", err)
	}
}

func deadOrigin(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return "http://" + addr
}
