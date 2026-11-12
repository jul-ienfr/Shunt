package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"shunt/internal/config"
	"shunt/test/support"
)

func clientBody(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func readJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v\n%s", resp.Request.URL.Path, err, data)
	}
	return doc
}

func TestGUIClientsMetadata(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": "http://127.0.0.1:1"},
		Selected: "local",
	})
	resp := guiRequest(t, relay, http.MethodGet, "/_shunt/api/clients", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clients status = %d", resp.StatusCode)
	}
	doc := readJSON(t, resp)
	if doc["relay"] != relay.Origin() {
		t.Fatalf("relay = %v, want %s", doc["relay"], relay.Origin())
	}
	clients, ok := doc["clients"].([]any)
	if !ok || len(clients) != 3 {
		t.Fatalf("clients = %#v", doc["clients"])
	}
	byID := map[string]map[string]any{}
	for _, raw := range clients {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("client entry = %#v", raw)
		}
		byID[item["id"].(string)] = item
	}
	for _, id := range []string{"claude", "codex", "dsh"} {
		if byID[id] == nil {
			t.Fatalf("missing client %q in %#v", id, byID)
		}
	}
	if !strings.Contains(byID["claude"]["target"].(string), ".claude") {
		t.Fatalf("claude target = %v", byID["claude"]["target"])
	}
	if byID["dsh"]["write_supported"] != false {
		t.Fatalf("dsh write_supported = %v", byID["dsh"]["write_supported"])
	}
}

func TestGUIClientsPreviewDoesNotLeakExistingSecrets(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": "http://127.0.0.1:1"},
		Selected: "local",
	})
	settingsPath := filepath.Join(relay.Dir, "claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	existing := `{"env":{"ANTHROPIC_API_KEY":"super-secret-key"},"theme":"dark"}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	relay.UpdateConfig(t, func(cfg *config.Config) { cfg.Clients.Claude.SettingsPath = settingsPath })

	body := clientBody(t, map[string]any{"client": "claude", "model": "free-a"})
	resp := guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/preview", body, tokenHeader(relay))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preview status = %d", resp.StatusCode)
	}
	doc := readJSON(t, resp)
	content, _ := doc["content"].(string)
	snippet, _ := doc["snippet"].(string)
	if !strings.Contains(content, relay.Origin()) || !strings.Contains(content, "free-a") {
		t.Fatalf("preview content = %s", content)
	}
	if !strings.Contains(snippet, "ANTHROPIC_BASE_URL") {
		t.Fatalf("preview snippet = %s", snippet)
	}
	if strings.Contains(content, "super-secret-key") || strings.Contains(snippet, "super-secret-key") {
		t.Fatal("preview leaked an existing credential")
	}
	if doc["target"] != settingsPath {
		t.Fatalf("target = %v, want %s", doc["target"], settingsPath)
	}
}

func TestGUIClientsWritePreservesUnrelatedSettings(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": "http://127.0.0.1:1"},
		Selected: "local",
	})
	settingsPath := filepath.Join(relay.Dir, "claude", "settings.json")
	codexPath := filepath.Join(relay.Dir, "codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{"env":{"KEEP_ME":"yes"},"theme":"dark"}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	codexOriginal := "# keep this comment\napproval_policy = \"never\"\n"
	if err := os.WriteFile(codexPath, []byte(codexOriginal), 0o600); err != nil {
		t.Fatal(err)
	}
	relay.UpdateConfig(t, func(cfg *config.Config) {
		cfg.Clients.Claude.SettingsPath = settingsPath
		cfg.Clients.Codex.ConfigPath = codexPath
	})

	resp := guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/write",
		clientBody(t, map[string]any{"client": "claude", "model": "free-a"}), tokenHeader(relay))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claude write status = %d", resp.StatusCode)
	}
	doc := readJSON(t, resp)
	backup, _ := doc["backup"].(string)
	if backup == "" {
		t.Fatal("claude write did not create a backup")
	}
	if data, err := os.ReadFile(backup); err != nil || string(data) != original {
		t.Fatalf("backup = %q, %v", data, err)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Theme string            `json:"theme"`
		Env   map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("settings: %v\n%s", err, data)
	}
	if settings.Theme != "dark" || settings.Env["KEEP_ME"] != "yes" {
		t.Fatalf("unrelated settings lost: %s", data)
	}
	if settings.Env["ANTHROPIC_BASE_URL"] != relay.Origin() || settings.Env["ANTHROPIC_MODEL"] != "free-a" {
		t.Fatalf("claude env = %v", settings.Env)
	}
	if _, ok := settings.Env["ANTHROPIC_API_KEY"]; ok {
		t.Fatal("claude write added a credential")
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/write",
		clientBody(t, map[string]any{"client": "codex", "model": "free-a"}), tokenHeader(relay))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("codex write status = %d", resp.StatusCode)
	}
	_ = readJSON(t, resp)
	codexData, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# keep this comment", `approval_policy = "never"`, `model = "free-a"`, `[model_providers.shunt]`, relay.Origin() + `/v1`} {
		if !strings.Contains(string(codexData), want) {
			t.Fatalf("codex config missing %q:\n%s", want, codexData)
		}
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/preview",
		clientBody(t, map[string]any{"client": "claude", "model": "free-a"}), tokenHeader(relay))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second preview status = %d", resp.StatusCode)
	}
	doc = readJSON(t, resp)
	if changed, _ := doc["changed"].([]any); len(changed) != 0 {
		t.Fatalf("expected no changes after write, got %v", changed)
	}
}

func TestGUIClientsDSHPreviewOnly(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": "http://127.0.0.1:1"},
		Selected: "local",
	})
	body := clientBody(t, map[string]any{
		"client":          "dsh",
		"api":             "both",
		"anthropic_model": "free-a",
		"openai_model":    "free-a",
	})
	resp := guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/preview", body, tokenHeader(relay))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dsh preview status = %d", resp.StatusCode)
	}
	doc := readJSON(t, resp)
	content, _ := doc["content"].(string)
	for _, want := range []string{"- id: llm-pi-ai", "shunt-anthropic", "shunt-openai", "api: anthropic-messages", "api: openai-completions"} {
		if !strings.Contains(content, want) {
			t.Fatalf("dsh fragment missing %q:\n%s", want, content)
		}
	}
	if doc["write_supported"] != false {
		t.Fatalf("dsh write_supported = %v", doc["write_supported"])
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/write", body, tokenHeader(relay))
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("dsh write status = %d, want 501", resp.StatusCode)
	}
}

func TestGUIClientsBackupsListAndRestore(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": "http://127.0.0.1:1"},
		Selected: "local",
	})
	settingsPath := filepath.Join(relay.Dir, "claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{"env":{"KEEP_ME":"yes"},"theme":"dark"}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	relay.UpdateConfig(t, func(cfg *config.Config) { cfg.Clients.Claude.SettingsPath = settingsPath })

	resp := guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/write",
		clientBody(t, map[string]any{"client": "claude", "model": "free-a"}), tokenHeader(relay))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("write status = %d", resp.StatusCode)
	}
	doc := readJSON(t, resp)
	backupPath, _ := doc["backup"].(string)
	if backupPath == "" {
		t.Fatal("write did not return a backup")
	}
	backupName := filepath.Base(backupPath)

	if err := os.WriteFile(settingsPath, []byte(`{"env":{"KEEP_ME":"changed"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	resp = guiRequest(t, relay, http.MethodGet, "/_shunt/api/clients/backups?client=claude", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("backups status = %d", resp.StatusCode)
	}
	doc = readJSON(t, resp)
	if doc["client"] != "claude" || doc["target"] != settingsPath || doc["write_supported"] != true {
		t.Fatalf("backups metadata = %#v", doc)
	}
	backups, ok := doc["backups"].([]any)
	if !ok || len(backups) == 0 {
		t.Fatalf("backups = %#v", doc["backups"])
	}
	first, _ := backups[0].(map[string]any)
	if first["name"] != backupName {
		t.Fatalf("first backup = %#v, want %s", first, backupName)
	}

	restoreBody := clientBody(t, map[string]any{"client": "claude", "backup": backupName})
	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/restore", restoreBody, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthorized restore status = %d", resp.StatusCode)
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/restore",
		clientBody(t, map[string]any{"client": "claude", "backup": "../escape.bak"}), tokenHeader(relay))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("traversal status = %d", resp.StatusCode)
	}
	doc = readJSON(t, resp)
	errDoc, _ := doc["error"].(map[string]any)
	if errDoc["code"] != "backup_invalid" {
		t.Fatalf("traversal error = %#v", doc["error"])
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/restore", restoreBody, tokenHeader(relay))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore status = %d", resp.StatusCode)
	}
	doc = readJSON(t, resp)
	if doc["restored"] != backupName || doc["target"] != settingsPath {
		t.Fatalf("restore response = %#v", doc)
	}
	safety, _ := doc["backup"].(string)
	if safety == "" {
		t.Fatal("restore did not create a safety backup")
	}
	restored, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != original {
		t.Fatalf("restored content = %s, want %s", restored, original)
	}

	resp = guiRequest(t, relay, http.MethodGet, "/_shunt/api/clients/backups?client=claude", "", nil)
	doc = readJSON(t, resp)
	backups, _ = doc["backups"].([]any)
	if len(backups) < 2 {
		t.Fatalf("expected the safety backup in the list, got %#v", backups)
	}
	first, _ = backups[0].(map[string]any)
	if first["name"] != filepath.Base(safety) {
		t.Fatalf("newest backup = %#v, want %s", first, filepath.Base(safety))
	}
}

func TestGUIClientsRestoreRejectsPreviewOnlyAndUnknownBackups(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": "http://127.0.0.1:1"},
		Selected: "local",
	})

	resp := guiRequest(t, relay, http.MethodGet, "/_shunt/api/clients/backups?client=dsh", "", nil)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("dsh backups status = %d, want 501", resp.StatusCode)
	}
	doc := readJSON(t, resp)
	errDoc, _ := doc["error"].(map[string]any)
	if errDoc["code"] != "client_restore_unsupported" {
		t.Fatalf("dsh backups error = %#v", doc["error"])
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/restore",
		clientBody(t, map[string]any{"client": "dsh", "backup": "x.bak"}), tokenHeader(relay))
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("dsh restore status = %d, want 501", resp.StatusCode)
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/restore",
		clientBody(t, map[string]any{"client": "nope", "backup": "x.bak"}), tokenHeader(relay))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown client status = %d, want 400", resp.StatusCode)
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/restore",
		clientBody(t, map[string]any{"client": "claude", "backup": "missing.bak"}), tokenHeader(relay))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing backup status = %d, want 400", resp.StatusCode)
	}
	doc = readJSON(t, resp)
	errDoc, _ = doc["error"].(map[string]any)
	if errDoc["code"] != "backup_invalid" {
		t.Fatalf("missing backup error = %#v", doc["error"])
	}
}

func TestGUIClientsRequireTokenAndCatalog(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": "http://127.0.0.1:1"},
		Selected: "local",
	})
	body := clientBody(t, map[string]any{"client": "claude", "model": "free-a"})
	resp := guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/preview", body, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthorized preview status = %d", resp.StatusCode)
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/clients/preview",
		clientBody(t, map[string]any{"client": "claude"}), tokenHeader(relay))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("missing catalog status = %d, want 409", resp.StatusCode)
	}
	doc := readJSON(t, resp)
	errDoc, _ := doc["error"].(map[string]any)
	if errDoc["code"] != "model_catalog_missing" {
		t.Fatalf("error = %#v", doc["error"])
	}
}
