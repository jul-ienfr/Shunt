package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"shunt/internal/config"
	"shunt/internal/runtime"
	"shunt/internal/upstream"
	"shunt/internal/util"
)

// Proxy is the streaming data plane. It loads one immutable selection per
// request and never re-reads routing state while a body is in flight.
type Proxy struct {
	selector *Selector
	tables   *TableHolder
	tracker  *upstream.Tracker
	runtime  *runtime.Store
	inflight *Inflight
	logger   *slog.Logger

	trace atomic.Bool

	mu         sync.Mutex
	transports map[string]*http.Transport
	clients    map[string]*http.Client
}

// NewProxy builds the proxying data plane.
func NewProxy(selector *Selector, tables *TableHolder, tracker *upstream.Tracker, rt *runtime.Store, logger *slog.Logger, maxInflight int) *Proxy {
	if logger == nil {
		logger = slog.Default()
	}
	return &Proxy{
		selector:   selector,
		tables:     tables,
		tracker:    tracker,
		runtime:    rt,
		inflight:   NewInflight(maxInflight),
		logger:     logger,
		transports: map[string]*http.Transport{},
		clients:    map[string]*http.Client{},
	}
}

// SetTraceHeaders enables or disables diagnostic upstream headers.
func (p *Proxy) SetTraceHeaders(enabled bool) { p.trace.Store(enabled) }

// Inflight exposes the in-flight guard for status reporting.
func (p *Proxy) Inflight() *Inflight { return p.inflight }

func (p *Proxy) transport(profileID string, profile config.Profile) (*http.Transport, *http.Client) {
	key := profileID + "|" + profile.BaseURL + fmt.Sprintf("|%d|%d", profile.ConnectTimeoutMS, profile.ResponseHeaderTimeoutMS)
	p.mu.Lock()
	defer p.mu.Unlock()
	if tr, ok := p.transports[key]; ok {
		return tr, p.clients[key]
	}
	tr := upstream.NewTransport(profile)
	client := &http.Client{Transport: tr}
	p.transports[key] = tr
	p.clients[key] = client
	return tr, client
}

// CloseIdle closes all pooled upstream connections.
func (p *Proxy) CloseIdle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, tr := range p.transports {
		tr.CloseIdleConnections()
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.serve(w, r, "")
}

// ServeHTTPPinned proxies through a listener-level pin. An empty pin follows
// the global selection; a non-empty pin routes this port to one profile and
// applies manual-mode health gating regardless of the global mode.
func (p *Proxy) ServeHTTPPinned(w http.ResponseWriter, r *http.Request, pinned string) {
	p.serve(w, r, pinned)
}

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request, pinned string) {
	if r.Method == http.MethodConnect {
		http.Error(w, "CONNECT is not supported", http.StatusMethodNotAllowed)
		return
	}
	requestID := util.NewRequestID()
	selection := p.selector.Load()
	profileID := selection.Profile
	mode := selection.Mode
	if pinned != "" {
		profileID = pinned
		mode = "manual"
	}
	if profileID == "" {
		WriteRelayError(w, http.StatusServiceUnavailable, CodeNoHealthyUpstream, "no upstream profile is selected", "", requestID)
		p.runtime.Update(func(s *runtime.Snapshot) { s.ErrorsTotal++ })
		return
	}
	table := p.tables.Load()
	profile, ok := table.Profile(profileID)
	if !ok || !profile.IsEnabled() {
		WriteRelayError(w, http.StatusBadGateway, CodeUpstreamUnavailable, "selected profile is unavailable", profileID, requestID)
		p.runtime.Update(func(s *runtime.Snapshot) { s.ErrorsTotal++ })
		return
	}
	if !p.inflight.TryAcquire() {
		WriteRelayError(w, http.StatusServiceUnavailable, CodeOverloaded, "too many in-flight requests", profileID, requestID)
		p.runtime.Update(func(s *runtime.Snapshot) { s.ErrorsTotal++ })
		return
	}
	defer p.inflight.Release()

	if mode == "auto" && !p.anyHealthy(table) {
		WriteRelayError(w, http.StatusServiceUnavailable, CodeNoHealthyUpstream, "no enabled upstream profile is healthy", profileID, requestID)
		p.runtime.Update(func(s *runtime.Snapshot) { s.ErrorsTotal++ })
		return
	}

	if mode == "manual" {
		if healthy, known := p.tracker.Healthy(profileID); known && !healthy {
			WriteRelayError(w, http.StatusBadGateway, CodeUpstreamUnavailable, fmt.Sprintf("upstream %q is unavailable", profileID), profileID, requestID)
			p.runtime.Update(func(s *runtime.Snapshot) { s.ErrorsTotal++ })
			return
		}
	}

	base, err := url.Parse(profile.BaseURL)
	if err != nil || !base.IsAbs() || base.Host == "" {
		WriteRelayError(w, http.StatusBadGateway, CodeUpstreamUnavailable, "profile base URL is invalid", profileID, requestID)
		p.runtime.Update(func(s *runtime.Snapshot) { s.ErrorsTotal++ })
		return
	}

	tr, client := p.transport(profileID, profile)
	trace := p.trace.Load()
	start := time.Now()
	p.runtime.Update(func(s *runtime.Snapshot) {
		s.RequestsTotal++
		s.Inflight = p.inflight.Count()
		if pinned == "" {
			s.Effective = profileID
		}
		v := profileID
		s.LastGood = &v
	})

	rp := &httputil.ReverseProxy{
		Transport:     tr,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = base.Scheme
			pr.Out.URL.Host = base.Host
			pr.Out.URL.Path = pr.In.URL.Path
			pr.Out.URL.RawPath = pr.In.URL.RawPath
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			PrepareRequest(pr.Out, base, profileID, requestID, trace)
			if pr.Out.Body != nil {
				pr.Out.Body = &countingReadCloser{inner: pr.Out.Body, onWrite: func(n int64) {
					p.runtime.Update(func(s *runtime.Snapshot) { s.BytesIn += n })
				}}
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			FilterResponseHeaders(resp.Header, profileID, requestID)
			if resp.Body != nil {
				resp.Body = &countingReadCloser{inner: resp.Body, onWrite: func(n int64) {
					p.runtime.Update(func(s *runtime.Snapshot) { s.BytesOut += n })
				}}
			}
			p.tracker.RecordSuccess(profileID, resp.StatusCode, time.Since(start))
			p.runtime.Update(func(s *runtime.Snapshot) {
				s.Inflight = p.inflight.Count()
				v := profileID
				s.LastGood = &v
			})
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
			if errors.Is(proxyErr, context.Canceled) || errors.Is(req.Context().Err(), context.Canceled) {
				p.logger.Debug("client disconnected", "request_id", requestID, "profile", profileID)
				return
			}
			timeout := isTimeoutError(proxyErr)
			code := CodeUpstreamUnavailable
			status := http.StatusBadGateway
			message := fmt.Sprintf("upstream %q is unavailable: %s", profileID, sanitize(proxyErr))
			if timeout {
				code = CodeUpstreamTimeout
				status = http.StatusGatewayTimeout
				message = fmt.Sprintf("upstream %q timed out", profileID)
			}
			p.tracker.RecordTransportFailure(profileID, proxyErr)
			p.runtime.Update(func(s *runtime.Snapshot) {
				s.ErrorsTotal++
				s.Inflight = p.inflight.Count()
			})
			p.logger.Warn("upstream request failed", "request_id", requestID, "profile", profileID, "error", message)
			WriteRelayError(rw, status, code, message, profileID, requestID)
		},
	}
	rp.ServeHTTP(w, r)
	_ = client
}

// anyHealthy reports whether at least one enabled profile is healthy or still
// unknown. Unknown profiles remain usable so a cold start is not refused.
func (p *Proxy) anyHealthy(table *ProfileTable) bool {
	if table == nil {
		return true
	}
	for id, profile := range table.Profiles {
		if !profile.IsEnabled() {
			continue
		}
		healthy, known := p.tracker.Healthy(id)
		if !known || healthy {
			return true
		}
	}
	return false
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func sanitize(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 160 {
		msg = msg[:160]
	}
	return msg
}

type countingReadCloser struct {
	inner   io.ReadCloser
	onWrite func(int64)
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.inner.Read(p)
	if n > 0 && c.onWrite != nil {
		c.onWrite(int64(n))
	}
	return n, err
}

func (c *countingReadCloser) Close() error { return c.inner.Close() }
