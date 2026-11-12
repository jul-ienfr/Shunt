package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"shunt/internal/cli/commands"
	"shunt/internal/config"
	"shunt/test/support"
)

type doctorCheckDoc struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Action string `json:"action"`
}

type doctorDoc struct {
	Schema   int              `json:"schema"`
	Checks   []doctorCheckDoc `json:"checks"`
	Fails    int              `json:"fails"`
	Warnings int              `json:"warnings"`
	Exit     int              `json:"exit"`
}

func (d doctorDoc) status(check string) string {
	for _, c := range d.Checks {
		if c.Check == check {
			return c.Status
		}
	}
	return ""
}

func doctorRun(t *testing.T, configPath string, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	e := &commands.Env{
		Version:    "0.1.0-test",
		ConfigPath: configPath,
		Stdout:     &out,
		Stderr:     &errOut,
		LookupEnv: func(name string) (string, bool) {
			v, ok := env[name]
			return v, ok
		},
	}
	code := commands.Doctor(e, args)
	return code, out.String(), errOut.String()
}

func doctorJSON(t *testing.T, stdout string) doctorDoc {
	t.Helper()
	var doc doctorDoc
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("decode doctor JSON: %v\n%s", err, stdout)
	}
	return doc
}

// installFakeTailscale makes exec.LookPath("tailscale") resolve to a copy of
// this test binary, which prints output and exits successfully.
func installFakeTailscale(t *testing.T, output string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("test executable: %v", err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("read test executable: %v", err)
	}
	dir := t.TempDir()
	name := "tailscale"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(dir, name)
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatalf("write fake tailscale: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHUNT_FAKE_TAILSCALE_OUTPUT", output)
}

func unusedLoopback(t *testing.T) string {
	t.Helper()
	port := reservePort(t, "127.0.0.1")
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func doctorConfigPath(t *testing.T, baseURL string, mutate func(*config.Config)) string {
	t.Helper()
	dir := t.TempDir()
	yes := true
	cfg := config.Defaults()
	cfg.Profiles = map[string]config.Profile{
		"local": {
			Label:                   "Local",
			BaseURL:                 baseURL,
			ProbeURL:                strings.TrimRight(baseURL, "/") + "/",
			ProbeMethod:             http.MethodGet,
			ConnectTimeoutMS:        500,
			ResponseHeaderTimeoutMS: 120000,
			Priority:                10,
			Enabled:                 &yes,
			TLSVerify:               &yes,
		},
	}
	cfg.DefaultProfile = "local"
	cfg.Switch.OnProfile = "local"
	cfg.Switch.OffProfile = "local"
	cfg.Auto.Preferred = "local"
	cfg.Relay.Listen = unusedLoopback(t)
	cfg.Paths = config.Paths{
		State:     filepath.Join(dir, "state.json"),
		Runtime:   filepath.Join(dir, "runtime.json"),
		CacheDir:  filepath.Join(dir, "cache"),
		LogDir:    filepath.Join(dir, "logs"),
		BackupDir: filepath.Join(dir, "backups"),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	if err := config.Validate(&cfg); err != nil {
		t.Fatalf("doctor test config invalid: %v", err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal doctor config: %v", err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write doctor config: %v", err)
	}
	return path
}

func doctorUpstream(t *testing.T) *support.Upstream {
	t.Helper()
	return support.New(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"m1","display_name":"M1","pricing":{"prompt":"0","completion":"0"}}],"has_more":false}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		if r.URL.Path != "/" {
			_, _ = io.WriteString(w, "ok")
		}
	})
}

func TestDoctorHealthyEnvironment(t *testing.T) {
	upstream := doctorUpstream(t)
	defer upstream.Close()
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})
	installFakeTailscale(t, "100.64.0.1 msi online\n")

	code, stdout, stderr := doctorRun(t, relay.ConfigPath, map[string]string{"SHUNT_TOKEN": "doctor-secret"}, "--json")
	if code != commands.ExitOK {
		t.Fatalf("healthy exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	doc := doctorJSON(t, stdout)
	if doc.Fails != 0 || doc.Warnings != 0 || doc.Exit != commands.ExitOK {
		t.Fatalf("healthy doctor = %+v", doc)
	}
	for _, check := range []string{"config", "state", "runtime", "relay", "probe:local", "models:local", "credential", "tailscale", "security"} {
		if got := doc.status(check); got != "PASS" {
			t.Fatalf("check %q = %q, want PASS (%+v)", check, got, doc.Checks)
		}
	}
	if strings.Contains(stdout, "doctor-secret") {
		t.Fatal("doctor JSON leaked the token")
	}

	code, stdout, stderr = doctorRun(t, relay.ConfigPath, map[string]string{"SHUNT_TOKEN": "doctor-secret"})
	if code != commands.ExitOK {
		t.Fatalf("text exit = %d: %s / %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "PASS") || !strings.Contains(stdout, "all checks passed") {
		t.Fatalf("text doctor output:\n%s", stdout)
	}
}

func TestDoctorWrongPortWarns(t *testing.T) {
	upstream := doctorUpstream(t)
	defer upstream.Close()
	configPath := doctorConfigPath(t, upstream.Origin(), nil)
	installFakeTailscale(t, "100.64.0.1 msi online\n")

	code, stdout, stderr := doctorRun(t, configPath, map[string]string{"SHUNT_TOKEN": "doctor-secret"}, "--json")
	if code != commands.ExitGeneric {
		t.Fatalf("wrong-port exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	doc := doctorJSON(t, stdout)
	if doc.Fails != 0 || doc.Warnings == 0 {
		t.Fatalf("wrong-port doctor = %+v", doc)
	}
	if got := doc.status("relay"); got != "WARN" {
		t.Fatalf("relay check = %q", got)
	}
	for _, c := range doc.Checks {
		if c.Status == "WARN" && c.Action == "" {
			t.Fatalf("warning %q has no safe action", c.Check)
		}
	}
}

func TestDoctorStoppedUpstreamFails(t *testing.T) {
	dead := "http://" + unusedLoopback(t)
	configPath := doctorConfigPath(t, dead, nil)
	installFakeTailscale(t, "100.64.0.1 msi online\n")

	code, stdout, stderr := doctorRun(t, configPath, nil, "--json")
	if code != commands.ExitUsage {
		t.Fatalf("stopped-upstream exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	doc := doctorJSON(t, stdout)
	if doc.Fails == 0 {
		t.Fatalf("stopped-upstream doctor = %+v", doc)
	}
	if got := doc.status("probe:local"); got != "FAIL" {
		t.Fatalf("probe:local = %q", got)
	}
	for _, c := range doc.Checks {
		if c.Status == "FAIL" && c.Action == "" {
			t.Fatalf("failure %q has no safe action", c.Check)
		}
	}
}

func TestDoctorInvalidCertificateFails(t *testing.T) {
	tlsUpstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer tlsUpstream.Close()
	configPath := doctorConfigPath(t, tlsUpstream.URL, nil)
	installFakeTailscale(t, "100.64.0.1 msi online\n")

	code, stdout, stderr := doctorRun(t, configPath, nil, "--json")
	if code != commands.ExitUsage {
		t.Fatalf("bad-cert exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	doc := doctorJSON(t, stdout)
	if got := doc.status("probe:local"); got != "FAIL" {
		t.Fatalf("probe:local = %q (%+v)", got, doc.Checks)
	}
	found := false
	for _, c := range doc.Checks {
		if c.Check == "probe:local" && (strings.Contains(strings.ToLower(c.Detail), "certificate") || strings.Contains(strings.ToLower(c.Detail), "x509")) {
			found = true
		}
	}
	if !found {
		t.Fatalf("certificate failure was not reported: %+v", doc.Checks)
	}
}

func TestDoctorMissingCredentialWarns(t *testing.T) {
	upstream := doctorUpstream(t)
	defer upstream.Close()
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})
	installFakeTailscale(t, "100.64.0.1 msi online\n")

	code, stdout, stderr := doctorRun(t, relay.ConfigPath, nil, "--json")
	if code != commands.ExitGeneric {
		t.Fatalf("missing-credential exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	doc := doctorJSON(t, stdout)
	if got := doc.status("credential"); got != "WARN" {
		t.Fatalf("credential check = %q", got)
	}
	if !strings.Contains(stdout, "no model token") {
		t.Fatalf("missing credential detail:\n%s", stdout)
	}
	if got := doc.status("models:local"); got != "" {
		t.Fatalf("models check ran without a credential: %q", got)
	}
}

func TestDoctorUnwritableDirectoriesFail(t *testing.T) {
	upstream := doctorUpstream(t)
	defer upstream.Close()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := doctorConfigPath(t, upstream.Origin(), func(cfg *config.Config) {
		cfg.Paths.State = filepath.Join(blocker, "state.json")
		cfg.Paths.CacheDir = blocker
		cfg.Paths.LogDir = blocker
	})
	installFakeTailscale(t, "100.64.0.1 msi online\n")

	code, stdout, stderr := doctorRun(t, configPath, nil, "--json")
	if code != commands.ExitUsage {
		t.Fatalf("unwritable exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	doc := doctorJSON(t, stdout)
	for _, check := range []string{"directory:state", "directory:cache", "directory:logs"} {
		if got := doc.status(check); got != "FAIL" {
			t.Fatalf("%s = %q (%+v)", check, got, doc.Checks)
		}
	}
}

func TestDoctorWarnsOnActiveFunnel(t *testing.T) {
	upstream := doctorUpstream(t)
	defer upstream.Close()
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})
	installFakeTailscale(t, "100.64.0.1 msi online\nhttps://msi.tail.ts.net Funnel on\n")

	code, stdout, stderr := doctorRun(t, relay.ConfigPath, map[string]string{"SHUNT_TOKEN": "doctor-secret"}, "--json")
	if code != commands.ExitGeneric {
		t.Fatalf("funnel exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	doc := doctorJSON(t, stdout)
	if got := doc.status("tailscale"); got != "WARN" {
		t.Fatalf("tailscale check = %q", got)
	}
	if !strings.Contains(stdout, "Funnel") {
		t.Fatalf("funnel warning missing:\n%s", stdout)
	}
	for _, c := range doc.Checks {
		lower := strings.ToLower(c.Action)
		for _, unsafe := range []string{"disable the firewall", "public bind", "--insecure", "skip verify", "tls bypass"} {
			if strings.Contains(lower, unsafe) {
				t.Fatalf("doctor suggested unsafe action %q: %+v", unsafe, c)
			}
		}
	}
}
