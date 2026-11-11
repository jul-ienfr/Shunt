// Package support provides deterministic test doubles for the shunt tests.
package support

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// Request is a captured inbound request. The body is read by the test double
// before the wrapped handler runs.
type Request struct {
	Method   string
	Host     string
	Path     string
	RawQuery string
	Header   http.Header
	Body     []byte
}

// Upstream is an httptest server that records every request and delegates to a
// configurable handler.
type Upstream struct {
	*httptest.Server

	mu       sync.Mutex
	requests []Request
}

// New starts an upstream with the given handler. A nil handler answers 200
// with a small plain-text body.
func New(handler http.HandlerFunc) *Upstream {
	u := &Upstream{}
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "ok")
		}
	}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		_ = r.Body.Close()
		u.mu.Lock()
		u.requests = append(u.requests, Request{
			Method:   r.Method,
			Host:     r.Host,
			Path:     r.URL.Path,
			RawQuery: r.URL.RawQuery,
			Header:   r.Header.Clone(),
			Body:     body,
		})
		u.mu.Unlock()
		handler(w, r)
	}))
	return u
}

// Origin returns the fake upstream's base URL without a trailing slash.
func (u *Upstream) Origin() string { return u.Server.URL }

// Requests returns a copy of the captured requests.
func (u *Upstream) Requests() []Request {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]Request, len(u.requests))
	copy(out, u.requests)
	return out
}

// LastRequest returns the most recent captured request.
func (u *Upstream) LastRequest() (Request, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.requests) == 0 {
		return Request{}, false
	}
	return u.requests[len(u.requests)-1], true
}

// Count returns the number of captured requests.
func (u *Upstream) Count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.requests)
}

// Reset clears the captured requests.
func (u *Upstream) Reset() {
	u.mu.Lock()
	u.requests = nil
	u.mu.Unlock()
}

// JSON returns a handler that writes the value as JSON with the given status.
func JSON(status int, value any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}
}

// SSE returns a handler that streams each chunk as a server-sent event. When
// delay is positive it sleeps between chunks. When disconnect is true it closes
// the connection after the first chunk.
func SSE(chunks []string, delay time.Duration, disconnect bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		for i, chunk := range chunks {
			_, _ = io.WriteString(w, chunk)
			if flusher != nil {
				flusher.Flush()
			}
			if disconnect && i == 0 {
				if hijacker, ok := w.(http.Hijacker); ok {
					if conn, _, err := hijacker.Hijack(); err == nil {
						_ = conn.Close()
					}
				}
				return
			}
			if delay > 0 && i != len(chunks)-1 {
				select {
				case <-time.After(delay):
				case <-r.Context().Done():
					return
				}
			}
		}
	}
}
