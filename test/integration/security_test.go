package integration

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"shunt/internal/cli/commands"
	"shunt/internal/config"
	"shunt/test/support"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func assertNoSecrets(t *testing.T, label, text string, secrets []string) {
	t.Helper()
	lower := strings.ToLower(text)
	for _, secret := range secrets {
		if strings.Contains(lower, strings.ToLower(secret)) {
			t.Fatalf("%s leaked %q", label, secret)
		}
	}
}

func TestSecretsNeverReachLogsStateRuntimeCacheOrConfig(t *testing.T) {
	const (
		bearerSecret = "sk-ant-oat-SECRET-bearer-9911"
		apiKeySecret = "sk-live-SECRET-apikey-2244"
		cookieSecret = "cookie-SECRET-7788"
		bodySecret   = "BODY_SECRET_MARKER_5566"
	)
	secrets := []string{bearerSecret, apiKeySecret, cookieSecret, bodySecret}

	upstream := support.New(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, mixedCatalog)
		case "/slow":
			time.Sleep(1500 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		}
	})
	defer upstream.Close()

	logs := &lockedBuffer{}
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles:                map[string]string{"local": upstream.Origin()},
		Selected:                "local",
		ResponseHeaderTimeoutMS: 1000,
		LogWriter:               logs,
	})

	req, err := http.NewRequest(http.MethodPost, relay.URL("/v1/messages"), strings.NewReader(`{"prompt":"`+bodySecret+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearerSecret)
	req.Header.Set("X-Api-Key", apiKeySecret)
	req.Header.Set("Cookie", cookieSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := directClient().Do(req)
	if err != nil {
		t.Fatalf("proxied request: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxied status = %d", resp.StatusCode)
	}
	for _, name := range []string{"Authorization", "X-Api-Key", "Cookie", "Set-Cookie"} {
		if got := resp.Header.Get(name); got != "" {
			t.Fatalf("response leaked request header %s = %q", name, got)
		}
	}

	got, ok := upstream.LastRequest()
	if !ok {
		t.Fatal("upstream saw no request")
	}
	if got.Header.Get("Authorization") != "Bearer "+bearerSecret || got.Header.Get("X-Api-Key") != apiKeySecret {
		t.Fatalf("relay did not forward credentials: %v", got.Header)
	}
	if !bytes.Contains(got.Body, []byte(bodySecret)) {
		t.Fatal("relay did not forward the request body")
	}

	statusResp, err := directClient().Get(relay.URL("/_shunt/status"))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	statusBody, err := io.ReadAll(statusResp.Body)
	_ = statusResp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecrets(t, "status", string(statusBody), secrets)
	assertNoSecrets(t, "status", string(statusBody), []string{"authorization", "bearer", "x-api-key"})

	// Force an error-path log entry and make sure it stays free of headers/body.
	slowResp, err := directClient().Get(relay.URL("/slow"))
	if err != nil {
		t.Fatalf("slow request: %v", err)
	}
	_, _ = io.Copy(io.Discard, slowResp.Body)
	_ = slowResp.Body.Close()
	if slowResp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("slow status = %d, want 504", slowResp.StatusCode)
	}

	// Client configuration generation must never persist the credential.
	env := map[string]string{"SHUNT_TOKEN": bearerSecret}
	modelEnv := &commands.Env{
		Version:    "0.1.0-test",
		ConfigPath: relay.ConfigPath,
		LookupEnv: func(name string) (string, bool) {
			v, ok := env[name]
			return v, ok
		},
	}
	var modelOut, modelErr bytes.Buffer
	modelEnv.Stdout = &modelOut
	modelEnv.Stderr = &modelErr
	writeDir := t.TempDir()
	claudePath := filepath.Join(writeDir, "claude-settings.json")
	codexPath := filepath.Join(writeDir, "codex-config.toml")
	for _, args := range [][]string{
		{"--via", "local", "--for", "claude", "--write", "--path", claudePath},
		{"--via", "local", "--for", "codex", "--write", "--path", codexPath},
	} {
		if code := commands.Models(modelEnv, args); code != commands.ExitOK {
			t.Fatalf("models %v exit = %d: %s", args, code, modelErr.String())
		}
	}
	assertNoSecrets(t, "generated client output", modelOut.String(), secrets)
	for _, path := range []string{claudePath, codexPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read generated config: %v", err)
		}
		assertNoSecrets(t, "generated "+filepath.Base(path), string(data), secrets)
	}

	relay.Close()

	logText := logs.String()
	assertNoSecrets(t, "relay logs", logText, secrets)
	for _, header := range []string{"authorization", "x-api-key", "cookie"} {
		if strings.Contains(strings.ToLower(logText), header) {
			t.Fatalf("relay logs mention request header %q:\n%s", header, logText)
		}
	}
	if !strings.Contains(logText, "upstream request failed") {
		t.Fatalf("expected an error-path log entry:\n%s", logText)
	}

	err = filepath.WalkDir(relay.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		assertNoSecrets(t, "artifact "+path, string(data), secrets)
		return nil
	})
	if err != nil {
		t.Fatalf("walk relay directory: %v", err)
	}
}

func TestNoInsecureTLSMode(t *testing.T) {
	var out, errOut bytes.Buffer
	env := &commands.Env{
		Version: "0.1.0-test",
		Stdout:  &out,
		Stderr:  &errOut,
	}
	if code := commands.Serve(env, []string{"--insecure"}); code != commands.ExitUsage {
		t.Fatalf("serve --insecure exit = %d, want %d (stderr: %s)", code, commands.ExitUsage, errOut.String())
	}

	dir := t.TempDir()
	cfg := config.Defaults()
	no := false
	profile := cfg.Profiles["remote"]
	profile.TLSVerify = &no
	cfg.Profiles["remote"] = profile
	data, err := toml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := config.Load(path); err == nil {
		t.Fatal("config with tls_verify=false was accepted")
	} else if !strings.Contains(err.Error(), "tls_verify") {
		t.Fatalf("tls_verify error = %v", err)
	}
}
