package config

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestEffectiveListenersLegacyFallback(t *testing.T) {
	cfg := Defaults()
	cfg.Relay.Listen = "127.0.0.1:9123"
	listeners := cfg.EffectiveListeners()
	if len(listeners) != 1 {
		t.Fatalf("legacy listeners = %d, want 1", len(listeners))
	}
	if listeners[0].Name != "default" || listeners[0].Listen != "127.0.0.1:9123" {
		t.Fatalf("legacy listener = %+v", listeners[0])
	}
	if cfg.ExplicitListeners() {
		t.Fatal("defaults must not declare [[listeners]]")
	}
}

func TestEffectiveListenersSkipsDisabled(t *testing.T) {
	no := false
	cfg := Defaults()
	cfg.Listeners = []Listener{
		{Name: "off", Listen: "127.0.0.1:9201", Enabled: &no},
		{Name: "on", Listen: "127.0.0.1:9202", Profile: "remote"},
	}
	listeners := cfg.EffectiveListeners()
	if len(listeners) != 1 || listeners[0].Name != "on" {
		t.Fatalf("effective listeners = %+v", listeners)
	}
	if primary := cfg.PrimaryListener(); primary.Name != "on" || primary.Profile != "remote" {
		t.Fatalf("primary listener = %+v", primary)
	}
}

func TestParseRejectsInvalidListeners(t *testing.T) {
	base := RenderDefaultTOML(DefaultLocalURL, DefaultRemoteURL)
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "duplicate name",
			text: base + "\n[[listeners]]\nname = \"edge\"\nlisten = \"127.0.0.1:9201\"\n\n[[listeners]]\nname = \"edge\"\nlisten = \"127.0.0.1:9202\"\n",
			want: "duplicated",
		},
		{
			name: "duplicate address",
			text: base + "\n[[listeners]]\nname = \"one\"\nlisten = \"127.0.0.1:9201\"\n\n[[listeners]]\nname = \"two\"\nlisten = \"127.0.0.1:9201\"\n",
			want: "duplicates the address",
		},
		{
			name: "bad name",
			text: base + "\n[[listeners]]\nname = \"Edge_Port\"\nlisten = \"127.0.0.1:9201\"\n",
			want: "must match",
		},
		{
			name: "pinned to unknown profile",
			text: base + "\n[[listeners]]\nname = \"edge\"\nlisten = \"127.0.0.1:9201\"\nprofile = \"nope\"\n",
			want: "must name an enabled profile",
		},
		{
			name: "public bind without opt-in",
			text: base + "\n[[listeners]]\nname = \"edge\"\nlisten = \"192.168.1.10:9201\"\n",
			want: "allow_non_loopback",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.text))
			if err == nil {
				t.Fatalf("Parse accepted invalid listeners")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}

	allDisabled := base + "\n[[listeners]]\nname = \"edge\"\nlisten = \"127.0.0.1:9201\"\nenabled = false\n"
	if _, err := Parse([]byte(allDisabled)); err == nil || !strings.Contains(err.Error(), "at least one enabled listener") {
		t.Fatalf("all-disabled error = %v", err)
	}
}

func TestListenerEditsPreserveCommentsAndRoundTrip(t *testing.T) {
	text := RenderDefaultTOML(DefaultLocalURL, DefaultRemoteURL)
	marker := "# Optional: expose several local entry ports"
	if !strings.Contains(text, marker) {
		t.Fatalf("template lost its listener documentation")
	}

	text, err := AddListener(text, "edge", "127.0.0.1:9201", "remote")
	if err != nil {
		t.Fatalf("add listener: %v", err)
	}
	if !strings.Contains(text, marker) {
		t.Fatal("add listener dropped comments")
	}
	cfg, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("parse after add: %v", err)
	}
	if len(cfg.Listeners) != 1 {
		t.Fatalf("listeners = %d, want 1", len(cfg.Listeners))
	}
	if got := cfg.Listeners[0]; got.Name != "edge" || got.Listen != "127.0.0.1:9201" || got.Profile != "remote" || !got.IsEnabled() {
		t.Fatalf("listener = %+v", got)
	}
	if cfg.PrimaryListener().Name != "edge" {
		t.Fatalf("primary listener = %+v", cfg.PrimaryListener())
	}

	text, err = SetListenerListen(text, "edge", "127.0.0.1:9202")
	if err != nil {
		t.Fatalf("set listener listen: %v", err)
	}
	cfg, err = Parse([]byte(text))
	if err != nil {
		t.Fatalf("parse after set: %v", err)
	}
	if cfg.Listeners[0].Listen != "127.0.0.1:9202" {
		t.Fatalf("listen = %q", cfg.Listeners[0].Listen)
	}
	if !strings.Contains(text, marker) {
		t.Fatal("set listener dropped comments")
	}

	text, err = RemoveListener(text, "edge")
	if err != nil {
		t.Fatalf("remove listener: %v", err)
	}
	cfg, err = Parse([]byte(text))
	if err != nil {
		t.Fatalf("parse after remove: %v", err)
	}
	if cfg.ExplicitListeners() {
		t.Fatalf("listener table survived removal: %+v", cfg.Listeners)
	}
	if !strings.Contains(text, marker) {
		t.Fatal("remove listener dropped comments")
	}

	if _, err := RemoveListener(text, "edge"); err == nil {
		t.Fatal("removing an unknown listener succeeded")
	}
	if _, err := SetListenerListen(text, "edge", "127.0.0.1:9203"); err == nil {
		t.Fatal("editing an unknown listener succeeded")
	}
}

func TestAddListenerDropsEncoderEmptyArray(t *testing.T) {
	data, err := toml.Marshal(Defaults())
	if err != nil {
		t.Fatalf("marshal defaults: %v", err)
	}
	if !strings.Contains(string(data), "listeners = []") {
		t.Skipf("encoder no longer writes an empty listeners array")
	}
	text, err := AddListener(string(data), "edge", "127.0.0.1:9201", "remote")
	if err != nil {
		t.Fatalf("add listener: %v", err)
	}
	if strings.Contains(text, "listeners = []") {
		t.Fatal("empty listeners array was not removed")
	}
	cfg, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Listeners) != 1 || cfg.Listeners[0].Name != "edge" {
		t.Fatalf("listeners = %+v", cfg.Listeners)
	}
}

func TestProfileEditsRoundTrip(t *testing.T) {
	text := RenderDefaultTOML(DefaultLocalURL, DefaultRemoteURL)
	text, err := AddProfile(text, "third", "Third upstream", "https://third.example", "https://third.example/health")
	if err != nil {
		t.Fatalf("add profile: %v", err)
	}
	cfg, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("parse after add: %v", err)
	}
	p, ok := cfg.Profiles["third"]
	if !ok {
		t.Fatal("added profile missing")
	}
	if p.BaseURL != "https://third.example" || p.ProbeURL != "https://third.example/health" || !p.IsEnabled() {
		t.Fatalf("profile = %+v", p)
	}
	if !p.VerifyTLS() {
		t.Fatal("new https profile must verify TLS")
	}

	text, err = RemoveProfile(text, "third")
	if err != nil {
		t.Fatalf("remove profile: %v", err)
	}
	cfg, err = Parse([]byte(text))
	if err != nil {
		t.Fatalf("parse after remove: %v", err)
	}
	if _, ok := cfg.Profiles["third"]; ok {
		t.Fatal("removed profile still present")
	}

	if _, err := AddProfile(text, "local", "duplicate", "http://127.0.0.1:4000", ""); err == nil {
		t.Fatal("adding an existing profile succeeded")
	}
	if _, err := RemoveProfile(text, "missing"); err == nil {
		t.Fatal("removing an unknown profile succeeded")
	}
}

func TestParseRejectsPinnedDisabledProfile(t *testing.T) {
	text := RenderDefaultTOML(DefaultLocalURL, DefaultRemoteURL)
	text += `
[profiles.pinned]
label = "Pinned"
base_url = "https://pinned.example"
probe_url = "https://pinned.example/"
enabled = false

[[listeners]]
name = "edge"
listen = "127.0.0.1:9201"
profile = "pinned"
`
	_, err := Parse([]byte(text))
	if err == nil {
		t.Fatal("Parse accepted a listener pinned to a disabled profile")
	}
	if !strings.Contains(err.Error(), `listeners[0] (edge): profile "pinned" must name an enabled profile`) {
		t.Fatalf("error = %v", err)
	}
}
