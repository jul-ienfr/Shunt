package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"shunt/internal/cli/commands"
	"shunt/internal/models"
	"shunt/test/support"
)

func cliEnv(relay *support.Relay, env map[string]string) (*commands.Env, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &commands.Env{
		Version:    "0.1.0-test",
		ConfigPath: relay.ConfigPath,
		Stdout:     out,
		Stderr:     errOut,
		LookupEnv: func(name string) (string, bool) {
			v, ok := env[name]
			return v, ok
		},
	}, out, errOut
}

func catalogPage(afterID string) map[string]any {
	start, end := 0, 300
	if afterID != "" {
		start, end = 300, 500
	}
	data := make([]any, 0, end-start)
	for i := start; i < end; i++ {
		data = append(data, map[string]any{
			"id":           fmt.Sprintf("m%03d", i),
			"display_name": fmt.Sprintf("Model %03d", i),
			"pricing":      map[string]any{"prompt": "0", "completion": "0"},
		})
	}
	page := map[string]any{"data": data, "has_more": afterID == "", "last_id": ""}
	if afterID == "" {
		page["last_id"] = "m299"
	}
	return page
}

func TestModelDiscoveryPaginationCacheAndDialects(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	headers := map[string]string{}
	afterSeen := ""
	upstream := support.New(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		requests++
		if key := r.Header.Get("x-api-key"); key != "" {
			headers["anthropic_key"] = key
			headers["anthropic_version"] = r.Header.Get("anthropic-version")
		}
		if auth := r.Header.Get("Authorization"); auth != "" {
			headers["authorization"] = auth
		}
		if after := r.URL.Query().Get("after_id"); after != "" {
			afterSeen = after
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(catalogPage(r.URL.Query().Get("after_id")))
	})
	defer upstream.Close()

	relay := support.NewRelay(t, support.RelayOptions{
		Profiles: map[string]string{"local": upstream.Origin()},
		Selected: "local",
	})
	env := map[string]string{"SHUNT_TOKEN": "secret-token"}

	run := func(args ...string) (int, *bytes.Buffer, *bytes.Buffer) {
		e, out, errOut := cliEnv(relay, env)
		code := commands.Models(e, args)
		return code, out, errOut
	}

	start := time.Now()
	code, out, errOut := run("--via", "local", "--dialect", "anthropic", "--json")
	elapsed := time.Since(start)
	if code != commands.ExitOK {
		t.Fatalf("cold run exit = %d: %s", code, errOut.String())
	}
	if elapsed > 5*time.Second {
		t.Fatalf("cold fetch took %s", elapsed)
	}
	var cat models.Catalog
	if err := json.Unmarshal(out.Bytes(), &cat); err != nil {
		t.Fatalf("decode catalog: %v\n%s", err, out.String())
	}
	if cat.Counts.Total != 500 || len(cat.Models) != 500 {
		t.Fatalf("catalog = %+v", cat.Counts)
	}
	if cat.Models[0].ID != "m000" || cat.Models[499].ID != "m499" {
		t.Fatalf("model order = %q .. %q", cat.Models[0].ID, cat.Models[499].ID)
	}
	if cat.Counts.Free != 500 {
		t.Fatalf("free count = %d", cat.Counts.Free)
	}
	mu.Lock()
	firstRequests := requests
	mu.Unlock()
	if firstRequests != 2 {
		t.Fatalf("pagination requests = %d, want 2", firstRequests)
	}
	if headers["anthropic_key"] != "secret-token" || headers["anthropic_version"] != "2023-06-01" {
		t.Fatalf("anthropic headers = %v", headers)
	}
	if afterSeen != "m299" {
		t.Fatalf("pagination cursor = %q", afterSeen)
	}

	start = time.Now()
	code, out, errOut = run("--via", "local", "--dialect", "anthropic", "--json")
	cached := time.Since(start)
	if code != commands.ExitOK {
		t.Fatalf("cache run exit = %d: %s", code, errOut.String())
	}
	if cached > 100*time.Millisecond {
		t.Fatalf("cached fetch took %s", cached)
	}
	if err := json.Unmarshal(out.Bytes(), &cat); err != nil || cat.Counts.Total != 500 {
		t.Fatalf("cached catalog = %+v (%v)", cat.Counts, err)
	}
	mu.Lock()
	if requests != firstRequests {
		mu.Unlock()
		t.Fatalf("cache run made %d extra upstream requests", requests-firstRequests)
	}
	mu.Unlock()

	code, _, errOut = run("--via", "local", "--dialect", "anthropic", "--json", "--refresh")
	if code != commands.ExitOK {
		t.Fatalf("refresh exit = %d: %s", code, errOut.String())
	}
	mu.Lock()
	if requests != firstRequests+2 {
		mu.Unlock()
		t.Fatalf("refresh requests = %d", requests)
	}
	mu.Unlock()

	code, out, errOut = run("--via", "local", "--dialect", "openai", "--json")
	if code != commands.ExitOK {
		t.Fatalf("openai exit = %d: %s", code, errOut.String())
	}
	var openaiCat models.Catalog
	if err := json.Unmarshal(out.Bytes(), &openaiCat); err != nil {
		t.Fatalf("openai catalog: %v", err)
	}
	if openaiCat.Counts.Total != 500 || openaiCat.Dialect != "openai" {
		t.Fatalf("openai catalog = %+v", openaiCat)
	}
	mu.Lock()
	auth := headers["authorization"]
	mu.Unlock()
	if auth != "Bearer secret-token" {
		t.Fatalf("openai authorization = %q", auth)
	}

	code, _, _ = run("--via", "missing", "--json")
	if code != commands.ExitUsage {
		t.Fatalf("missing profile exit = %d", code)
	}
}

func TestModelDiscoveryFailures(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		status int
	}{
		{name: "malformed", body: "not json", status: http.StatusOK},
		{name: "empty", body: `{"data":[]}`, status: http.StatusOK},
		{name: "upstream error", body: `{"error":"boom"}`, status: http.StatusInternalServerError},
		{name: "unauthorized", body: `{"error":"no"}`, status: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := support.New(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			defer upstream.Close()
			relay := support.NewRelay(t, support.RelayOptions{
				Profiles: map[string]string{"local": upstream.Origin()},
				Selected: "local",
			})
			e, _, errOut := cliEnv(relay, map[string]string{"SHUNT_TOKEN": "secret-token"})
			code := commands.Models(e, []string{"--via", "local", "--dialect", "anthropic", "--json"})
			if code != commands.ExitUpstream {
				t.Fatalf("exit = %d: %s", code, errOut.String())
			}
			if strings.Contains(errOut.String(), "secret-token") {
				t.Fatalf("error output leaked the token: %s", errOut.String())
			}
		})
	}
}
