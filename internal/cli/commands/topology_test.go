package commands

import (
	"flag"
	"testing"
)

func TestParseInterspersed(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	listen := fs.String("listen", "", "")
	profile := fs.String("profile", "", "")
	args := []string{"edge", "--listen", "127.0.0.1:8788", "--profile=remote", "extra"}
	if err := parseInterspersed(fs, args, "listen", "profile"); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *listen != "127.0.0.1:8788" || *profile != "remote" {
		t.Fatalf("flags = %q, %q", *listen, *profile)
	}
	if fs.NArg() != 2 || fs.Arg(0) != "edge" || fs.Arg(1) != "extra" {
		t.Fatalf("positionals = %v", fs.Args())
	}

	fs = flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("listen", "", "")
	if err := parseInterspersed(fs, []string{"edge", "--", "--listen"}, "listen"); err != nil {
		t.Fatalf("parse --: %v", err)
	}
	if fs.NArg() != 2 || fs.Arg(1) != "--listen" {
		t.Fatalf("positionals = %v", fs.Args())
	}
}

func TestOriginURL(t *testing.T) {
	good := map[string]string{
		"http://127.0.0.1:4000":       "http://127.0.0.1:4000",
		"https://host.tailnet.ts.net": "https://host.tailnet.ts.net",
		" https://host.example/ ":     "https://host.example",
		"http://[::1]:8080":           "http://[::1]:8080",
	}
	for raw, want := range good {
		got, err := originURL(raw)
		if err != nil {
			t.Fatalf("originURL(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("originURL(%q) = %q, want %q", raw, got, want)
		}
	}

	bad := []string{
		"",
		"127.0.0.1:4000",
		"ftp://host.example",
		"https://host.example/v1",
		"https://host.example/?a=1",
		"https://host.example/#frag",
		"https://user:pass@host.example",
	}
	for _, raw := range bad {
		if got, err := originURL(raw); err == nil {
			t.Fatalf("originURL(%q) = %q, want an error", raw, got)
		}
	}
}
