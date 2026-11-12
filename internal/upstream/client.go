package upstream

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"shunt/internal/config"
)

// NewTransport builds a per-profile transport. Compression is disabled so
// encoded bodies pass through untouched, HTTP/1.1 is pinned, and there is no
// body or total-request timeout.
func NewTransport(p config.Profile) *http.Transport {
	connectTimeout := time.Duration(p.ConnectTimeoutMS) * time.Millisecond
	if connectTimeout <= 0 {
		connectTimeout = 2 * time.Second
	}
	headerTimeout := time.Duration(p.ResponseHeaderTimeoutMS) * time.Millisecond
	if headerTimeout <= 0 {
		headerTimeout = 120 * time.Second
	}
	dialer := &net.Dialer{
		Timeout:   connectTimeout,
		KeepAlive: 30 * time.Second,
	}
	tr := &http.Transport{
		Proxy:                 proxyFunc(p),
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     false,
		DisableCompression:    true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   connectTimeout,
		ResponseHeaderTimeout: headerTimeout,
		ExpectContinueTimeout: time.Second,
	}
	if u, err := url.Parse(p.BaseURL); err == nil && u.Scheme == "https" {
		tr.TLSClientConfig = TLSConfig(u.Hostname())
	}
	return tr
}

// NewClient builds an HTTP client for a profile.
func NewClient(p config.Profile) *http.Client {
	return &http.Client{Transport: NewTransport(p)}
}

// proxyFunc resolves this profile's proxy policy. Relay traffic is direct by
// default: a machine-wide HTTP_PROXY must not silently capture it, which is
// exactly how a configuration that works on one machine fails on another.
// Operators can pin an explicit proxy or opt into the environment per profile.
func proxyFunc(p config.Profile) func(*http.Request) (*url.URL, error) {
	if raw := strings.TrimSpace(p.ProxyURL); raw != "" {
		if u, err := url.Parse(raw); err == nil && u.IsAbs() && u.Host != "" {
			return http.ProxyURL(u)
		}
	}
	if p.ProxyFromEnv {
		return http.ProxyFromEnvironment
	}
	return nil
}

// CloseIdle releases pooled connections for a transport.
func CloseIdle(tr *http.Transport) {
	if tr != nil {
		tr.CloseIdleConnections()
	}
}

// ShutdownContext returns a context bounded by a duration, or Background when
// the duration is disabled.
func ShutdownContext(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, d)
}
