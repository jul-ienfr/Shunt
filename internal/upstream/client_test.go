package upstream

import (
	"net/http"
	"testing"

	"shunt/internal/config"
)

func TestProxyPolicyDefaultsToDirect(t *testing.T) {
	if fn := proxyFunc(config.Profile{}); fn != nil {
		t.Fatal("relay traffic must be direct unless a profile opts in")
	}
	if fn := proxyFunc(config.Profile{ProxyURL: "not a url"}); fn != nil {
		t.Fatal("an invalid proxy_url must fall back to direct traffic")
	}
	if fn := proxyFunc(config.Profile{ProxyURL: "http://proxy.example:3128"}); fn == nil {
		t.Fatal("proxy_url was not applied")
	}
	if fn := proxyFunc(config.Profile{ProxyFromEnv: true}); fn == nil {
		t.Fatal("proxy_from_env was not applied")
	}
}

func TestProxyPolicyExplicitURLWins(t *testing.T) {
	fn := proxyFunc(config.Profile{
		ProxyURL:     "http://proxy.example:3128",
		ProxyFromEnv: true,
	})
	if fn == nil {
		t.Fatal("no proxy function")
	}
	req, err := http.NewRequest(http.MethodGet, "https://upstream.example/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := fn(req)
	if err != nil {
		t.Fatalf("proxy func: %v", err)
	}
	if u == nil || u.Host != "proxy.example:3128" {
		t.Fatalf("proxy = %v, want proxy.example:3128", u)
	}
}

func TestNewTransportUsesProfilePolicy(t *testing.T) {
	direct := NewTransport(config.Profile{BaseURL: "https://upstream.example"})
	if direct.Proxy != nil {
		t.Fatal("transport must not inherit the process proxy environment")
	}
	if direct.TLSClientConfig == nil {
		t.Fatal("https transport must configure TLS verification")
	}
	if direct.DisableCompression != true || direct.ForceAttemptHTTP2 {
		t.Fatalf("transport flags = compression=%v h2=%v", !direct.DisableCompression, direct.ForceAttemptHTTP2)
	}
}
