package contract

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"shunt/test/support"
)

func guiRequest(t *testing.T, relay *support.Relay, method, path, body string, mutate func(*http.Request)) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, relay.URL(path), reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if mutate != nil {
		mutate(req)
	}
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	resp.Body = &readCloser{Reader: resp.Body, t: t, path: path}
	return resp
}

type readCloser struct {
	io.Reader
	t    *testing.T
	path string
}

func (r *readCloser) Close() error {
	_, err := io.Copy(io.Discard, r.Reader)
	return err
}

func tokenHeader(relay *support.Relay) func(*http.Request) {
	return func(req *http.Request) { req.Header.Set("X-Shunt-UI-Token", relay.UI.Token()) }
}

func TestGUIStaticAssetsAndSecurityHeaders(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": "http://127.0.0.1:1"},
		Selected: "local",
	})

	resp := guiRequest(t, relay, http.MethodGet, "/_shunt/ui", "", nil)
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ui status = %d", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte(relay.UI.Token())) {
		t.Fatal("ui token was not injected into the page")
	}
	if bytes.Contains(body, []byte("__SHUNT_UI_TOKEN__")) {
		t.Fatal("ui token placeholder was not replaced")
	}
	if !bytes.Contains(body, []byte("/_shunt/ui/theme.js")) || !bytes.Contains(body, []byte(`id="theme"`)) {
		t.Fatal("ui page does not expose the theme bootstrap and toggle")
	}
	if !bytes.Contains(body, []byte(`id="language"`)) || !bytes.Contains(body, []byte(`data-i18n="refresh"`)) {
		t.Fatal("ui page does not expose the language selector and translated labels")
	}
	if !bytes.Contains(body, []byte(`id="upstream-warning"`)) || !bytes.Contains(body, []byte(`id="upstream-configure"`)) {
		t.Fatal("ui page does not expose the upstream warning and configure action")
	}
	if !bytes.Contains(body, []byte(`data-tab="clients"`)) || !bytes.Contains(body, []byte(`id="client-cards"`)) {
		t.Fatal("ui page does not expose the client configuration tab")
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP = %q", csp)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected CORS grant: %q", got)
	}

	for _, asset := range []struct {
		path string
		ct   string
	}{
		{path: "/_shunt/ui/app.js", ct: "application/javascript"},
		{path: "/_shunt/ui/i18n.js", ct: "application/javascript"},
		{path: "/_shunt/ui/theme.js", ct: "application/javascript"},
		{path: "/_shunt/ui/style.css", ct: "text/css"},
		{path: "/_shunt/ui/favicon.svg", ct: "image/svg+xml"},
	} {
		resp := guiRequest(t, relay, http.MethodGet, asset.path, "", nil)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", asset.path, resp.StatusCode)
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), asset.ct) {
			t.Fatalf("%s content-type = %q", asset.path, resp.Header.Get("Content-Type"))
		}
	}

	resp = guiRequest(t, relay, http.MethodGet, "/_shunt/ui/theme.js", "", nil)
	themeBody, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(themeBody, []byte("shunt-theme")) || !bytes.Contains(themeBody, []byte("data-theme")) {
		t.Fatalf("theme bootstrap is incomplete: %s", themeBody)
	}

	resp = guiRequest(t, relay, http.MethodGet, "/_shunt/ui/i18n.js", "", nil)
	i18nBody, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(i18nBody, []byte("shunt-lang")) || !bytes.Contains(i18nBody, []byte("detectLanguage")) {
		t.Fatalf("i18n module does not expose language persistence: %s", i18nBody)
	}
	if !bytes.Contains(i18nBody, []byte("Vue d’ensemble")) || !bytes.Contains(i18nBody, []byte("Overview")) {
		t.Fatalf("i18n module is missing French or English translations: %s", i18nBody)
	}

	enStart := bytes.Index(i18nBody, []byte("  en: {"))
	frStart := bytes.Index(i18nBody, []byte("  fr: {"))
	frEnd := bytes.LastIndex(i18nBody, []byte("};"))
	if enStart < 0 || frStart < 0 || frEnd < 0 || frStart <= enStart || frEnd <= frStart {
		t.Fatal("i18n module does not contain the expected language blocks")
	}
	enKeys := i18nKeys(string(i18nBody[enStart:frStart]))
	frKeys := i18nKeys(string(i18nBody[frStart:frEnd]))
	if len(enKeys) == 0 || len(frKeys) == 0 {
		t.Fatalf("i18n key extraction failed: en=%d fr=%d", len(enKeys), len(frKeys))
	}
	for key := range enKeys {
		if !frKeys[key] {
			t.Errorf("missing French translation for %q", key)
		}
	}
	for key := range frKeys {
		if !enKeys[key] {
			t.Errorf("missing English translation for %q", key)
		}
	}
	staticKeys := regexp.MustCompile(`data-i18n(?:-[a-z-]+)?="([^"]+)"`).FindAllSubmatch(body, -1)
	for _, match := range staticKeys {
		if !enKeys[string(match[1])] {
			t.Errorf("index.html references unknown i18n key %q", match[1])
		}
	}
}

func i18nKeys(block string) map[string]bool {
	keys := map[string]bool{}
	for _, match := range regexp.MustCompile(`(?m)^\s+'([^']+)':`).FindAllStringSubmatch(block, -1) {
		keys[match[1]] = true
	}
	return keys
}

func TestGUIOverviewPayload(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{
			"local":  "http://127.0.0.1:4100",
			"remote": "https://example.test",
		},
		Selected: "local",
	})

	resp := guiRequest(t, relay, http.MethodGet, "/_shunt/api/overview", "", nil)
	var doc struct {
		Schema    int     `json:"schema"`
		Mode      string  `json:"mode"`
		Selected  *string `json:"selected"`
		Effective string  `json:"effective"`
		Inflight  int     `json:"inflight"`
		Relay     struct {
			Version    string `json:"version"`
			PID        int    `json:"pid"`
			UptimeS    int64  `json:"uptime_s"`
			Listen     string `json:"listen"`
			IsLoopback bool   `json:"is_loopback"`
			Interfaces []struct {
				Name string `json:"name"`
			} `json:"interfaces"`
		} `json:"relay"`
		Profiles []struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			Scheme  string `json:"scheme"`
			Host    string `json:"host"`
			Port    int    `json:"port"`
			BaseURL string `json:"base_url"`
			Enabled bool   `json:"enabled"`
			Health  string `json:"health"`
		} `json:"profiles"`
	}
	support.DecodeJSON(t, resp, &doc)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if doc.Schema != 1 || doc.Mode != "manual" {
		t.Fatalf("overview = %+v", doc)
	}
	if doc.Selected == nil || *doc.Selected != "local" || doc.Effective != "local" {
		t.Fatalf("selection = %+v", doc)
	}
	if doc.Relay.Version != "0.1.0-test" || doc.Relay.PID <= 0 || doc.Relay.Listen != relay.Listen {
		t.Fatalf("relay view = %+v", doc.Relay)
	}
	if !doc.Relay.IsLoopback {
		t.Fatal("loopback listener not reported as loopback")
	}
	if len(doc.Profiles) != 2 {
		t.Fatalf("profiles = %+v", doc.Profiles)
	}
	byID := map[string]int{}
	for i, p := range doc.Profiles {
		byID[p.ID] = i
	}
	local := doc.Profiles[byID["local"]]
	if local.BaseURL != "http://127.0.0.1:4100" || local.Scheme != "http" || local.Host != "127.0.0.1" || local.Port != 4100 {
		t.Fatalf("local profile = %+v", local)
	}
	remote := doc.Profiles[byID["remote"]]
	if remote.Scheme != "https" || remote.Port != 443 {
		t.Fatalf("remote profile = %+v", remote)
	}
}

func TestGUIMutationsRequireToken(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": "http://127.0.0.1:4100", "remote": "https://example.test"},
		Selected: "local",
	})

	cases := []struct {
		path string
		body string
	}{
		{path: "/_shunt/api/switch", body: `{"profile":"remote","probe":false}`},
		{path: "/_shunt/api/mode", body: `{"mode":"manual"}`},
		{path: "/_shunt/api/entry", body: `{"ip":"127.0.0.1","port":9999}`},
		{path: "/_shunt/api/profiles/local/endpoint", body: `{"scheme":"http","host":"127.0.0.1","port":4200}`},
		{path: "/_shunt/api/models/refresh", body: ``},
	}
	for _, tc := range cases {
		resp := guiRequest(t, relay, http.MethodPost, tc.path, tc.body, nil)
		var doc struct {
			Schema int `json:"schema"`
			Error  struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		support.DecodeJSON(t, resp, &doc)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s status = %d", tc.path, resp.StatusCode)
		}
		if doc.Schema != 1 || doc.Error.Code != "ui_token_invalid" {
			t.Fatalf("%s error = %+v", tc.path, doc)
		}
	}
}

func TestGUIHostOriginAndFetchSiteChecks(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": "http://127.0.0.1:4100", "remote": "https://example.test"},
		Selected: "local",
	})

	resp := guiRequest(t, relay, http.MethodGet, "/_shunt/api/overview", "", func(req *http.Request) {
		req.Host = "evil.example:8787"
	})
	var doc struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	support.DecodeJSON(t, resp, &doc)
	if resp.StatusCode != http.StatusForbidden || doc.Error.Code != "ui_origin_invalid" {
		t.Fatalf("host check = %d %+v", resp.StatusCode, doc)
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/switch", `{"profile":"remote","probe":false}`, func(req *http.Request) {
		req.Header.Set("X-Shunt-UI-Token", relay.UI.Token())
		req.Header.Set("Origin", "http://evil.example")
	})
	support.DecodeJSON(t, resp, &doc)
	if resp.StatusCode != http.StatusForbidden || doc.Error.Code != "ui_origin_invalid" {
		t.Fatalf("origin check = %d %+v", resp.StatusCode, doc)
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/switch", `{"profile":"remote","probe":false}`, func(req *http.Request) {
		req.Header.Set("X-Shunt-UI-Token", relay.UI.Token())
		req.Header.Set("Origin", relay.Origin())
		req.Header.Set("Sec-Fetch-Site", "cross-site")
	})
	support.DecodeJSON(t, resp, &doc)
	if resp.StatusCode != http.StatusForbidden || doc.Error.Code != "ui_origin_invalid" {
		t.Fatalf("fetch-site check = %d %+v", resp.StatusCode, doc)
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/switch", `{"profile":"remote","probe":false}`, func(req *http.Request) {
		req.Header.Set("X-Shunt-UI-Token", relay.UI.Token())
		req.Header.Set("Origin", relay.Origin())
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	})
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid same-origin mutation = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("CORS grant = %q", got)
	}
}

func TestGUIPayloadAndBodyLimit(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": "http://127.0.0.1:4100", "remote": "https://example.test"},
		Selected: "local",
	})

	resp := guiRequest(t, relay, http.MethodPost, "/_shunt/api/switch", `{"profile":"remote","unknown":true}`, tokenHeader(relay))
	var doc struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	support.DecodeJSON(t, resp, &doc)
	if resp.StatusCode != http.StatusBadRequest || doc.Error.Code != "invalid_payload" {
		t.Fatalf("unknown field = %d %+v", resp.StatusCode, doc)
	}

	huge := `{"mode":"manual","padding":"` + strings.Repeat("x", 70000) + `"}`
	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/mode", huge, tokenHeader(relay))
	support.DecodeJSON(t, resp, &doc)
	if resp.StatusCode != http.StatusRequestEntityTooLarge || doc.Error.Code != "payload_too_large" {
		t.Fatalf("body limit = %d %+v", resp.StatusCode, doc)
	}
}

func TestGUISwitchAndMode(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": "http://127.0.0.1:4100", "remote": "https://example.test"},
		Selected: "local",
	})

	resp := guiRequest(t, relay, http.MethodPost, "/_shunt/api/switch", `{"profile":"remote","reason":"test","probe":false}`, tokenHeader(relay))
	var overview struct {
		Mode      string  `json:"mode"`
		Selected  *string `json:"selected"`
		Effective string  `json:"effective"`
	}
	support.DecodeJSON(t, resp, &overview)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("switch status = %d", resp.StatusCode)
	}
	if overview.Mode != "manual" || overview.Selected == nil || *overview.Selected != "remote" || overview.Effective != "remote" {
		t.Fatalf("switch overview = %+v", overview)
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/mode", `{"mode":"auto","preferred":"local"}`, tokenHeader(relay))
	support.DecodeJSON(t, resp, &overview)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mode status = %d", resp.StatusCode)
	}
	if overview.Mode != "auto" {
		t.Fatalf("mode overview = %+v", overview)
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/mode", `{"mode":"sideways"}`, tokenHeader(relay))
	var apiErr struct {
		Error struct {
			Code  string `json:"code"`
			Field string `json:"field"`
		} `json:"error"`
	}
	support.DecodeJSON(t, resp, &apiErr)
	if resp.StatusCode != http.StatusUnprocessableEntity || apiErr.Error.Code != "invalid_mode" || apiErr.Error.Field != "mode" {
		t.Fatalf("invalid mode = %d %+v", resp.StatusCode, apiErr)
	}

	resp = guiRequest(t, relay, http.MethodPost, "/_shunt/api/switch", `{"profile":"nope","probe":false}`, tokenHeader(relay))
	support.DecodeJSON(t, resp, &apiErr)
	if resp.StatusCode != http.StatusNotFound || apiErr.Error.Code != "profile_not_found" {
		t.Fatalf("unknown profile = %d %+v", resp.StatusCode, apiErr)
	}
}

func TestGUIEntryAndEndpointValidation(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": "http://127.0.0.1:4100", "remote": "https://example.test"},
		Selected: "local",
	})
	type apiErrorDoc struct {
		Error struct {
			Code  string `json:"code"`
			Field string `json:"field"`
		} `json:"error"`
	}

	cases := []struct {
		name   string
		path   string
		body   string
		status int
		code   string
	}{
		{name: "wildcard", path: "/_shunt/api/entry", body: `{"ip":"0.0.0.0","port":9000}`, status: http.StatusUnprocessableEntity, code: "invalid_entry"},
		{name: "public", path: "/_shunt/api/entry", body: `{"ip":"8.8.8.8","port":9000,"confirm_lan":true}`, status: http.StatusUnprocessableEntity, code: "invalid_entry"},
		{name: "lan not confirmed", path: "/_shunt/api/entry", body: `{"ip":"10.255.255.254","port":9000}`, status: http.StatusForbidden, code: "lan_not_confirmed"},
		{name: "bad port", path: "/_shunt/api/entry", body: `{"ip":"127.0.0.1","port":80}`, status: http.StatusUnprocessableEntity, code: "invalid_entry"},
		{name: "bad scheme", path: "/_shunt/api/profiles/local/endpoint", body: `{"scheme":"ftp","host":"127.0.0.1","port":4100}`, status: http.StatusUnprocessableEntity, code: "invalid_endpoint"},
		{name: "bad host", path: "/_shunt/api/profiles/local/endpoint", body: `{"scheme":"http","host":"http://x/y","port":4100}`, status: http.StatusUnprocessableEntity, code: "invalid_endpoint"},
		{name: "bad port", path: "/_shunt/api/profiles/local/endpoint", body: `{"scheme":"http","host":"127.0.0.1","port":70000}`, status: http.StatusUnprocessableEntity, code: "invalid_endpoint"},
		{name: "unknown profile", path: "/_shunt/api/profiles/nope/endpoint", body: `{"scheme":"http","host":"127.0.0.1","port":4100}`, status: http.StatusNotFound, code: "profile_not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := guiRequest(t, relay, http.MethodPost, tc.path, tc.body, tokenHeader(relay))
			var doc apiErrorDoc
			support.DecodeJSON(t, resp, &doc)
			if resp.StatusCode != tc.status || doc.Error.Code != tc.code {
				t.Fatalf("got %d %+v, want %d %s", resp.StatusCode, doc, tc.status, tc.code)
			}
		})
	}
}

func TestGUIModelsRequiresCredential(t *testing.T) {
	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": "http://127.0.0.1:4100"},
		Selected: "local",
	})
	for _, name := range []string{"SHUNT_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "OPENAI_API_KEY"} {
		t.Setenv(name, "")
	}
	resp := guiRequest(t, relay, http.MethodGet, "/_shunt/api/models", "", nil)
	var doc struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	support.DecodeJSON(t, resp, &doc)
	if resp.StatusCode != http.StatusConflict || doc.Error.Code != "credential_unavailable" {
		t.Fatalf("models without token = %d %+v", resp.StatusCode, doc)
	}
}
