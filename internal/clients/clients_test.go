package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeOptions() ClaudeOptions {
	return ClaudeOptions{
		BaseURL:    "http://127.0.0.1:8787",
		Model:      "claude-sonnet-4-5",
		Opus:       "claude-opus-4-1",
		Sonnet:     "",
		Haiku:      "claude-haiku-4-5",
		Fable:      "claude-fable",
		Legacy:     true,
		PowerShell: false,
	}
}

func TestClaudeShellAndSettings(t *testing.T) {
	opts := claudeOptions()
	shell := ClaudeShell(opts)
	for _, want := range []string{
		`export ANTHROPIC_BASE_URL="http://127.0.0.1:8787"`,
		`export ANTHROPIC_MODEL="claude-sonnet-4-5"`,
		`export ANTHROPIC_DEFAULT_OPUS_MODEL="claude-opus-4-1"`,
		`export ANTHROPIC_DEFAULT_SONNET_MODEL="claude-sonnet-4-5"`,
		`export ANTHROPIC_SMALL_FAST_MODEL="claude-sonnet-4-5"`,
		"Do not set ANTHROPIC_AUTH_TOKEN",
	} {
		if !strings.Contains(shell, want) {
			t.Errorf("shell missing %q:\n%s", want, shell)
		}
	}
	if strings.Contains(shell, "sk-") || strings.Contains(shell, "ANTHROPIC_AUTH_TOKEN=") {
		t.Fatalf("shell contains a credential: %s", shell)
	}

	data, err := ClaudeSettings(opts)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8787" {
		t.Fatalf("base url = %q", doc.Env["ANTHROPIC_BASE_URL"])
	}
	if _, ok := doc.Env["ANTHROPIC_API_KEY"]; ok {
		t.Fatal("settings must not store a credential")
	}
	if doc.Env["ANTHROPIC_DEFAULT_FABLE_MODEL"] != "claude-fable" {
		t.Fatalf("fable alias = %q", doc.Env["ANTHROPIC_DEFAULT_FABLE_MODEL"])
	}
}

func TestMergeClaudePreservesUnrelatedKeys(t *testing.T) {
	existing := []byte(`{"env":{"KEEP_ME":"yes","ANTHROPIC_MODEL":"old"},"theme":"dark"}`)
	merged, changed, err := MergeClaudeSettings(existing, claudeOptions())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(merged, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["theme"] != "dark" {
		t.Fatalf("unrelated key lost: %v", doc)
	}
	env := doc["env"].(map[string]any)
	if env["KEEP_ME"] != "yes" {
		t.Fatalf("unrelated env lost: %v", env)
	}
	if env["ANTHROPIC_MODEL"] != "claude-sonnet-4-5" {
		t.Fatalf("model not merged: %v", env)
	}
	if len(changed) == 0 {
		t.Fatal("expected changed keys")
	}
	if _, _, err := MergeClaudeSettings([]byte("{bad"), claudeOptions()); err == nil {
		t.Fatal("invalid settings must not be rewritten")
	}
}

func TestCodexSnippetAndMerge(t *testing.T) {
	if got := CodexBaseURL("http://127.0.0.1:8787/"); got != "http://127.0.0.1:8787/v1" {
		t.Fatalf("base url = %q", got)
	}
	if got := CodexBaseURL("http://127.0.0.1:8787/v1"); got != "http://127.0.0.1:8787/v1" {
		t.Fatalf("dedupe /v1 = %q", got)
	}
	snippet := CodexSnippet(CodexOptions{RelayOrigin: "http://127.0.0.1:8787", Model: "gpt-5-codex"})
	for _, want := range []string{`model = "gpt-5-codex"`, `model_provider = "shunt"`, `[model_providers.shunt]`, `base_url = "http://127.0.0.1:8787/v1"`, `env_key = "SHUNT_API_KEY"`, `wire_api = "responses"`} {
		if !strings.Contains(snippet, want) {
			t.Errorf("snippet missing %q:\n%s", want, snippet)
		}
	}

	existing := `# keep this comment
model = "existing"
approval_policy = "never"

[model_providers.other]
name = "other"
base_url = "https://other.example"
`
	merged, changed, err := MergeCodex(existing, CodexOptions{RelayOrigin: "http://127.0.0.1:8787", Model: "new-model"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(merged, "# keep this comment") || !strings.Contains(merged, `model = "existing"`) {
		t.Fatalf("comments or existing model lost without force:\n%s", merged)
	}
	if !strings.Contains(merged, "[model_providers.other]") || !strings.Contains(merged, `base_url = "https://other.example"`) {
		t.Fatalf("unrelated provider lost:\n%s", merged)
	}
	if !strings.Contains(merged, `base_url = "http://127.0.0.1:8787/v1"`) {
		t.Fatalf("shunt provider not written:\n%s", merged)
	}
	if len(changed) == 0 {
		t.Fatal("expected changed keys")
	}

	forced, _, err := MergeCodex(existing, CodexOptions{RelayOrigin: "http://127.0.0.1:8787", Model: "new-model", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(forced, `model = "new-model"`) {
		t.Fatalf("force did not replace model:\n%s", forced)
	}
}

func TestDSHFragment(t *testing.T) {
	fragment := DSHFragment(DSHOptions{
		RelayOrigin:   "http://127.0.0.1:8787",
		API:           "both",
		AnthropicID:   "claude-sonnet-4-5",
		AnthropicName: "Claude Sonnet",
		AnthropicCtx:  200000,
		OpenAIID:      "gpt-5",
		OpenAIName:    "GPT-5",
		OpenAICtx:     128000,
	})
	for _, want := range []string{
		"- id: llm-pi-ai",
		"shunt-anthropic:",
		"shunt-openai:",
		"apiKeyEnv: SHUNT_TOKEN",
		"api: anthropic-messages",
		"api: openai-completions",
		"baseURL: http://127.0.0.1:8787",
		"baseURL: http://127.0.0.1:8787/v1",
		"contextWindow: 200000",
		"preserve retryPolicy",
	} {
		if !strings.Contains(fragment, want) {
			t.Errorf("fragment missing %q:\n%s", want, fragment)
		}
	}
}

func TestBackupAndRestore(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(dir, "backups")
	backup, err := WriteWithBackup(backupDir, "claude", target, []byte("updated"))
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("no backup created")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "updated" {
		t.Fatalf("target = %q", data)
	}

	name := filepath.Base(backup)
	safety, err := Restore(backupDir, "claude", name, target)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(target)
	if string(data) != "original" {
		t.Fatalf("restored = %q", data)
	}
	if safety == "" {
		t.Fatal("expected a safety backup")
	}
	if _, err := ResolveBackup(backupDir, "claude", "../../etc/passwd"); err == nil {
		t.Fatal("path traversal must be rejected")
	}
}

func TestEnsureInsideHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	if err := EnsureInsideHome(filepath.Join(home, "file"), false); err != nil {
		t.Fatalf("home path rejected: %v", err)
	}
	if err := EnsureInsideHome(filepath.Join(home, "..", "outside"), false); err == nil {
		t.Fatal("outside path must be rejected")
	}
	if err := EnsureInsideHome(filepath.Join(home, "..", "outside"), true); err != nil {
		t.Fatalf("explicit path must be allowed: %v", err)
	}
}

func TestDryRun(t *testing.T) {
	got := DryRun("target", []byte("body"))
	if got != "--- target ---\nbody" {
		t.Fatalf("dry run = %q", got)
	}
}
