package upstream

import (
	"context"
	"net"
	"net/http"
	"net/url"
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
		Proxy:                 http.ProxyFromEnvironment,
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
