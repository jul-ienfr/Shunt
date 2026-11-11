package config

import (
	"strings"
	"testing"
)

func TestEditValuePreservesCommentsAndOrder(t *testing.T) {
	text := `schema = 1
default_profile = "local" # keep this

[relay]
# the listener
listen = "127.0.0.1:8787" # old
allow_non_loopback = false

[profiles.local]
base_url = "http://127.0.0.1:4000"
probe_url = "http://127.0.0.1:4000/"
label = "Local"

[profiles.remote]
base_url = "https://example.test"
`
	out, err := SetListen(text, "[::1]:9000")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `listen = "[::1]:9000" # old`) {
		t.Fatalf("comment or value not preserved:\n%s", out)
	}
	if !strings.Contains(out, "# the listener") || !strings.Contains(out, `default_profile = "local" # keep this`) {
		t.Fatalf("unrelated comments lost:\n%s", out)
	}
	if strings.Index(out, "[relay]") > strings.Index(out, "[profiles.local]") {
		t.Fatalf("section order changed:\n%s", out)
	}
}

func TestEditProfileEndpoint(t *testing.T) {
	text := `[profiles.local]
base_url = "http://127.0.0.1:4000"
probe_url = "http://127.0.0.1:4000/"
label = "Local"

[profiles.remote]
base_url = "https://example.test"
`
	out, err := SetProfileBaseURL(text, "local", "http://127.0.0.1:4100", "http://127.0.0.1:4100/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `base_url = "http://127.0.0.1:4100"`) || !strings.Contains(out, `probe_url = "http://127.0.0.1:4100/"`) {
		t.Fatalf("endpoint not updated:\n%s", out)
	}
	if !strings.Contains(out, `label = "Local"`) {
		t.Fatalf("unrelated key lost:\n%s", out)
	}
	if !strings.Contains(out, `[profiles.remote]`) || !strings.Contains(out, `base_url = "https://example.test"`) {
		t.Fatalf("other profile changed:\n%s", out)
	}
}

func TestEditValueRejectsDuplicateKey(t *testing.T) {
	text := `[relay]
listen = "127.0.0.1:8787"
listen = "127.0.0.1:8788"
`
	if _, err := SetListen(text, "127.0.0.1:9000"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate rejection, got %v", err)
	}
}

func TestEditValueAppendsMissingSection(t *testing.T) {
	text := "schema = 1\n"
	out, err := SetAllowNonLoopback(text, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[relay]") || !strings.Contains(out, "allow_non_loopback = true") {
		t.Fatalf("missing section not appended:\n%s", out)
	}
}

func TestEditValueInsertsMissingKeyWithoutExtraBlank(t *testing.T) {
	text := "[relay]\nlisten = \"127.0.0.1:8787\"\n\n[ui]\nenabled = true\n"
	out, err := SetAllowNonLoopback(text, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "listen = \"127.0.0.1:8787\"\nallow_non_loopback = true\n") {
		t.Fatalf("key not inserted at end of section:\n%q", out)
	}
	if strings.Contains(out, "listen = \"127.0.0.1:8787\"\n\nallow_non_loopback") {
		t.Fatalf("unexpected blank line before inserted key:\n%q", out)
	}
}

func TestEditValueTopLevel(t *testing.T) {
	text := "schema = 1\ndefault_profile = \"local\"\n\n[relay]\nlisten = \"127.0.0.1:8787\"\n"
	out, err := SetTopLevel(text, "default_profile", "\"remote\"")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `default_profile = "remote"`) || !strings.Contains(out, `schema = 1`) {
		t.Fatalf("top-level edit failed:\n%s", out)
	}
}

func TestEditValuePreservesCRLF(t *testing.T) {
	text := "schema = 1\r\n\r\n[relay]\r\nlisten = \"127.0.0.1:8787\"\r\n"
	out, err := SetListen(text, "127.0.0.1:9000")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ReplaceAll(out, "\r\n", ""), "\n") {
		t.Fatalf("line endings were not preserved:\n%q", out)
	}
}
