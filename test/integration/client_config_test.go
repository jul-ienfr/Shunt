package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"shunt/internal/cli/commands"
	"shunt/test/support"
)

func modelsHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

const mixedCatalog = `{"data":[
	{"id":"paid-model","display_name":"Paid","pricing":{"prompt":"0.001","completion":"0.002"}},
	{"id":"free-a","display_name":"Free A","context_length":200000,"pricing":{"prompt":"0","completion":"0"}},
	{"id":"free-b","display_name":"Free B","context_length":128000,"pricing":{"prompt":"0","completion":"0"}}
],"has_more":false}`

const paidOnlyCatalog = `{"data":[
	{"id":"paid-model","display_name":"Paid","pricing":{"prompt":"0.001","completion":"0.002"}}
],"has_more":false}`

func TestClientConfigGeneration(t *testing.T) {
	upstream := support.New(modelsHandler(mixedCatalog))
	defer upstream.Close()
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})
	env := map[string]string{"SHUNT_TOKEN": "secret-token"}

	run := func(args ...string) (int, string, string) {
		e, out, errOut := cliEnv(relay, env)
		code := commands.Models(e, args)
		return code, out.String(), errOut.String()
	}

	code, out, errOut := run("--via", "local", "--for", "claude")
	if code != commands.ExitOK {
		t.Fatalf("claude exit = %d: %s", code, errOut)
	}
	for _, want := range []string{"ANTHROPIC_BASE_URL", relay.Origin(), "free-a"} {
		if !strings.Contains(out, want) {
			t.Errorf("claude output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret-token") {
		t.Fatal("claude output leaked the token")
	}
	if strings.Contains(out, "ANTHROPIC_AUTH_TOKEN=") || strings.Contains(out, "ANTHROPIC_AUTH_TOKEN =") {
		t.Fatal("claude output set ANTHROPIC_AUTH_TOKEN")
	}

	code, out, errOut = run("--via", "local", "--for", "codex")
	if code != commands.ExitOK {
		t.Fatalf("codex exit = %d: %s", code, errOut)
	}
	for _, want := range []string{`model = "free-a"`, `model_provider = "shunt"`, `[model_providers.shunt]`, `base_url = "` + relay.Origin() + `/v1"`, `env_key = "SHUNT_API_KEY"`, `wire_api = "responses"`} {
		if !strings.Contains(out, want) {
			t.Errorf("codex output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret-token") {
		t.Fatal("codex output leaked the token")
	}

	code, out, errOut = run("--via", "local", "--for", "dsh")
	if code != commands.ExitOK {
		t.Fatalf("dsh exit = %d: %s", code, errOut)
	}
	for _, want := range []string{"- id: llm-pi-ai", "shunt-anthropic:", "shunt-openai:", "apiKeyEnv: SHUNT_TOKEN", "api: anthropic-messages", "api: openai-completions", "baseURL: " + relay.Origin(), "baseURL: " + relay.Origin() + "/v1", "free-a"} {
		if !strings.Contains(out, want) {
			t.Errorf("dsh output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret-token") {
		t.Fatal("dsh output leaked the token")
	}

	code, out, errOut = run("--via", "local", "--for", "claude", "--model", "paid-model")
	if code != commands.ExitOK {
		t.Fatalf("explicit paid exit = %d: %s", code, errOut)
	}
	if !strings.Contains(out, "paid-model") || !strings.Contains(errOut, "paid") {
		t.Fatalf("explicit paid output = %q / %q", out, errOut)
	}
}

func TestClientConfigNoFreeModel(t *testing.T) {
	upstream := support.New(modelsHandler(paidOnlyCatalog))
	defer upstream.Close()
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})
	e, out, errOut := cliEnv(relay, map[string]string{"SHUNT_TOKEN": "secret-token"})
	code := commands.Models(e, []string{"--via", "local", "--for", "claude"})
	if code != commands.ExitNoFreeModel {
		t.Fatalf("exit = %d: %s / %s", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "--model") {
		t.Fatalf("missing --model guidance: %s", errOut.String())
	}
}

func TestClientConfigWritesAndDryRun(t *testing.T) {
	upstream := support.New(modelsHandler(mixedCatalog))
	defer upstream.Close()
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})
	env := map[string]string{"SHUNT_TOKEN": "secret-token"}
	run := func(args ...string) (int, string, string) {
		e, out, errOut := cliEnv(relay, env)
		code := commands.Models(e, args)
		return code, out.String(), errOut.String()
	}

	dir := t.TempDir()
	claudePath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(claudePath, []byte(`{"env":{"KEEP_ME":"yes"},"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := run("--via", "local", "--for", "claude", "--write", "--path", claudePath)
	if code != commands.ExitOK {
		t.Fatalf("claude write exit = %d: %s", code, errOut)
	}
	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Theme string            `json:"theme"`
		Env   map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("settings: %v\n%s", err, data)
	}
	if doc.Theme != "dark" || doc.Env["KEEP_ME"] != "yes" {
		t.Fatalf("unrelated settings lost: %s", data)
	}
	if doc.Env["ANTHROPIC_BASE_URL"] != relay.Origin() || doc.Env["ANTHROPIC_MODEL"] != "free-a" {
		t.Fatalf("claude env = %v", doc.Env)
	}
	if _, ok := doc.Env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatal("claude settings stored a credential")
	}
	if strings.Contains(string(data), "secret-token") || strings.Contains(out, "secret-token") {
		t.Fatal("token leaked to settings or output")
	}
	backups, err := os.ReadDir(filepath.Join(relay.Dir, "backups"))
	if err != nil || len(backups) == 0 {
		t.Fatalf("expected a backup: %v %v", backups, err)
	}

	codexPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(codexPath, []byte("# keep\nmodel = \"old\"\n\n[model_providers.other]\nbase_url = \"https://other.example\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = run("--via", "local", "--for", "codex", "--write", "--path", codexPath)
	if code != commands.ExitOK {
		t.Fatalf("codex write exit = %d: %s", code, errOut)
	}
	data, err = os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# keep", `model = "old"`, "[model_providers.other]", `base_url = "https://other.example"`, `[model_providers.shunt]`, `base_url = "` + relay.Origin() + `/v1"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("codex file missing %q:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "secret-token") || strings.Contains(out, "secret-token") {
		t.Fatal("token leaked to codex config")
	}

	code, out, _ = run("--via", "local", "--for", "codex", "--force", "--write", "--path", codexPath)
	if code != commands.ExitOK {
		t.Fatalf("codex force exit = %d", code)
	}
	data, _ = os.ReadFile(codexPath)
	if !strings.Contains(string(data), `model = "free-a"`) {
		t.Fatalf("force did not replace the model:\n%s", data)
	}

	dryPath := filepath.Join(dir, "dry-settings.json")
	code, out, errOut = run("--via", "local", "--for", "claude", "--dry-run", "--path", dryPath)
	if code != commands.ExitOK {
		t.Fatalf("dry-run exit = %d: %s", code, errOut)
	}
	if !strings.Contains(out, "--- "+dryPath+" ---") {
		t.Fatalf("dry-run output = %q", out)
	}
	if _, err := os.Stat(dryPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run created %s", dryPath)
	}

	code, out, errOut = run("--via", "local", "--for", "dsh", "--write", "--path", filepath.Join(dir, "dsh.yml"))
	if code != commands.ExitWriteRefused {
		t.Fatalf("dsh write exit = %d: %s", code, errOut)
	}
	if !strings.Contains(out, "- id: llm-pi-ai") {
		t.Fatalf("dsh fragment missing from output: %s", out)
	}
}
