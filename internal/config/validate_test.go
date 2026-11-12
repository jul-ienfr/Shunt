package config

import (
	"net"
	"strings"
	"testing"
)

func cloneDefaults() Config {
	cfg := Defaults()
	profiles := make(map[string]Profile, len(cfg.Profiles))
	for id, p := range cfg.Profiles {
		profiles[id] = p
	}
	cfg.Profiles = profiles
	return cfg
}

func TestValidateListenMatrix(t *testing.T) {
	tests := []struct {
		name      string
		listen    string
		allow     bool
		wantError string
	}{
		{name: "ipv4 loopback", listen: "127.0.0.1:8787", allow: false},
		{name: "ipv6 loopback", listen: "[::1]:8787", allow: false},
		{name: "loopback lowest port", listen: "127.0.0.1:1024", allow: false},
		{name: "loopback highest port", listen: "127.0.0.1:65535", allow: false},
		{name: "wildcard ipv4", listen: "0.0.0.0:8787", allow: false, wantError: "wildcard"},
		{name: "wildcard ipv6", listen: "[::]:8787", allow: true, wantError: "wildcard"},
		{name: "public address", listen: "8.8.8.8:8787", allow: true, wantError: "private"},
		{name: "private needs flag", listen: "10.255.255.254:8787", allow: false, wantError: "allow_non_loopback"},
		{name: "private unassigned", listen: "10.255.255.254:8787", allow: true, wantError: "not assigned"},
		{name: "port below range", listen: "127.0.0.1:1023", allow: false, wantError: "1024"},
		{name: "port above range", listen: "127.0.0.1:65536", allow: false, wantError: "1024"},
		{name: "hostname rejected", listen: "localhost:8787", allow: false, wantError: "IP address"},
		{name: "missing port", listen: "127.0.0.1", allow: false, wantError: "host:port"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateListen(tc.listen, tc.allow)
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantError)
			}
			if !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error %q does not contain %q", err, tc.wantError)
			}
		})
	}
}

func TestValidateListenAssignedPrivateAddress(t *testing.T) {
	addrs, err := LocalAddresses()
	if err != nil {
		t.Skipf("list interfaces: %v", err)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr.Address)
		if ip == nil || ip.IsLoopback() || !IsPrivateIP(ip) {
			continue
		}
		listen := net.JoinHostPort(addr.Address, "8787")
		if err := ValidateListen(listen, false); err == nil {
			t.Fatalf("expected non-loopback flag rejection for %s", listen)
		}
		if err := ValidateListen(listen, true); err != nil {
			t.Fatalf("expected assigned private address %s to be accepted: %v", listen, err)
		}
		return
	}
	t.Skip("no assigned private non-loopback address on this host")
}

func TestIsPrivateIPRanges(t *testing.T) {
	private := []string{"10.0.0.1", "172.16.0.1", "172.31.255.1", "192.168.1.1", "100.64.0.1", "169.254.1.1", "127.0.0.1", "fc00::1", "fd12:3456::1", "fe80::1"}
	for _, text := range private {
		if !IsPrivateIP(net.ParseIP(text)) {
			t.Errorf("%s should be private", text)
		}
	}
	public := []string{"8.8.8.8", "172.32.0.1", "192.169.0.1", "2001:4860:4860::8888", "fec0::1"}
	for _, text := range public {
		if IsPrivateIP(net.ParseIP(text)) {
			t.Errorf("%s should not be private", text)
		}
	}
}

func TestNormalizeAndOriginIPv6(t *testing.T) {
	got, err := NormalizeListen("[::1]:8787")
	if err != nil {
		t.Fatal(err)
	}
	if got != "[::1]:8787" {
		t.Fatalf("normalized = %q", got)
	}
	if origin := ListenOrigin("[::1]:8787"); origin != "http://[::1]:8787" {
		t.Fatalf("origin = %q", origin)
	}
}

func TestValidateRejectsBadConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "wildcard listen", mutate: func(c *Config) { c.Relay.Listen = "0.0.0.0:8787" }, want: "wildcard"},
		{name: "schema", mutate: func(c *Config) { c.Schema = 2 }, want: "schema"},
		{name: "profile id", mutate: func(c *Config) {
			p := c.Profiles["local"]
			delete(c.Profiles, "local")
			c.Profiles["Local"] = p
		}, want: "profile id"},
		{name: "missing default", mutate: func(c *Config) { c.DefaultProfile = "nope" }, want: "default_profile"},
		{name: "switch profile", mutate: func(c *Config) { c.Switch.OnProfile = "nope" }, want: "switch.on_profile"},
		{name: "auto preferred", mutate: func(c *Config) { c.Auto.Preferred = "nope" }, want: "auto.preferred"},
		{name: "base url path", mutate: func(c *Config) {
			p := c.Profiles["local"]
			p.BaseURL = "http://127.0.0.1:4000/v1"
			p.ProbeURL = "http://127.0.0.1:4000/v1/"
			c.Profiles["local"] = p
		}, want: "path"},
		{name: "base url userinfo", mutate: func(c *Config) {
			p := c.Profiles["local"]
			p.BaseURL = "http://user:pass@127.0.0.1:4000"
			c.Profiles["local"] = p
		}, want: "absolute http(s) origin"},
		{name: "probe origin", mutate: func(c *Config) {
			p := c.Profiles["local"]
			p.ProbeURL = "http://127.0.0.1:4001/"
			c.Profiles["local"] = p
		}, want: "same origin"},
		{name: "probe method", mutate: func(c *Config) {
			p := c.Profiles["local"]
			p.ProbeMethod = "POST"
			c.Profiles["local"] = p
		}, want: "probe_method"},
		{name: "tls disabled", mutate: func(c *Config) {
			p := c.Profiles["local"]
			no := false
			p.TLSVerify = &no
			c.Profiles["local"] = p
		}, want: "tls_verify"},
		{name: "body idle", mutate: func(c *Config) { c.Relay.BodyIdleTimeoutMS = 5 }, want: "body_idle_timeout_ms"},
		{name: "ui body", mutate: func(c *Config) { c.UI.MaxRequestBytes = 10 }, want: "max_request_bytes"},
		{name: "dialect", mutate: func(c *Config) { c.Models.DefaultDialect = "gemini" }, want: "default_dialect"},
		{name: "relative paths", mutate: func(c *Config) { c.Paths.State = "state.json" }, want: "paths.state"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := cloneDefaults()
			tc.mutate(&cfg)
			err := Validate(&cfg)
			if err == nil {
				t.Fatalf("expected validation error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestValidateNeedsOneEnabledProfile(t *testing.T) {
	cfg := cloneDefaults()
	no := false
	for id, p := range cfg.Profiles {
		p.Enabled = &no
		cfg.Profiles[id] = p
	}
	if err := Validate(&cfg); err == nil {
		t.Fatal("expected all-disabled profile rejection")
	}
}
