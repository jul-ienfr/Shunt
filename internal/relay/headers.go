package relay

import (
	"net/http"
	"net/url"
	"strings"
)

var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// RemoveHopByHop removes hop-by-hop headers and every header named by the
// Connection header.
func RemoveHopByHop(h http.Header) {
	for _, name := range h.Values("Connection") {
		for _, part := range strings.Split(name, ",") {
			if key := strings.TrimSpace(part); key != "" {
				h.Del(key)
			}
		}
	}
	for _, name := range hopByHopHeaders {
		h.Del(name)
	}
}

// PrepareRequest rewrites an outbound request to target the upstream origin.
// It preserves end-to-end headers, removes transport-specific ones, and
// suppresses automatic forwarding headers.
func PrepareRequest(out *http.Request, base *url.URL, profile, requestID string, trace bool) {
	out.URL.Scheme = base.Scheme
	out.URL.Host = base.Host
	out.Host = base.Host
	RemoveHopByHop(out.Header)
	out.Header.Del("Expect")
	out.Header.Del("Host")
	// Setting these map entries to nil suppress ReverseProxy's automatic
	// forwarding headers.
	out.Header["X-Forwarded-For"] = nil
	out.Header["X-Forwarded-Host"] = nil
	out.Header["X-Forwarded-Proto"] = nil
	out.Header.Del("X-Shunt-Request-Id")
	out.Header.Del("X-Shunt-Profile")
	if trace {
		out.Header.Set("X-Shunt-Request-Id", requestID)
		out.Header.Set("X-Shunt-Profile", profile)
	}
}

// FilterResponseHeaders removes hop-by-hop response headers and adds the shunt
// tracing headers.
func FilterResponseHeaders(h http.Header, profile, requestID string) {
	RemoveHopByHop(h)
	h.Set("X-Shunt-Request-Id", requestID)
	h.Set("X-Shunt-Profile", profile)
}
