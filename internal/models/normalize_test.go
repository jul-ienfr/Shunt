package models

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func rawModel(fields map[string]any) map[string]any {
	out := map[string]any{"id": "test-model"}
	for k, v := range fields {
		out[k] = v
	}
	return out
}

func TestNormalizeClassification(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]any
		want string
	}{
		{name: "explicit free", raw: rawModel(map[string]any{"free": true, "pricing": map[string]any{"prompt": "0.5"}}), want: FreeFree},
		{name: "explicit paid", raw: rawModel(map[string]any{"free": false}), want: FreePaid},
		{name: "tier trial", raw: rawModel(map[string]any{"tier": "trial"}), want: FreeFree},
		{name: "tier pro", raw: rawModel(map[string]any{"tier": "pro"}), want: FreePaid},
		{name: "zero pricing", raw: rawModel(map[string]any{"pricing": map[string]any{"prompt": "0", "completion": "0.000"}}), want: FreeFree},
		{name: "zero prompt positive completion", raw: rawModel(map[string]any{"pricing": map[string]any{"prompt": "0", "completion": "0.000002"}}), want: FreePaid},
		{name: "request price only", raw: rawModel(map[string]any{"pricing": map[string]any{"request": "0.01"}}), want: FreePaid},
		{name: "image price only", raw: rawModel(map[string]any{"pricing": map[string]any{"image": "0.02"}}), want: FreePaid},
		{name: "no data", raw: rawModel(nil), want: FreeUnknown},
		{name: "free suffix", raw: map[string]any{"id": "vendor:free"}, want: FreeFree},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, ok := NormalizeRecord(tc.raw, "local", DialectAnthropic)
			if !ok {
				t.Fatal("record dropped")
			}
			if rec.Free != tc.want {
				t.Fatalf("free = %q, want %q (cost %+v)", rec.Free, tc.want, rec.Cost)
			}
		})
	}
}

func TestNormalizeRecordFields(t *testing.T) {
	raw := map[string]any{
		"id":                   "claude-sonnet-4-5",
		"display_name":         "Claude Sonnet 4.5",
		"owned_by":             "anthropic",
		"created_at":           "2026-11-04T00:00:00Z",
		"context_window":       json.Number("200000"),
		"max_output_tokens":    json.Number("8192"),
		"capabilities":         map[string]any{"image_input": true, "pdf_input": true, "thinking": true, "tool_use": true},
		"archived":             false,
		"supported_parameters": []any{"tools", "reasoning_effort"},
	}
	rec, ok := NormalizeRecord(raw, "remote", DialectAnthropic)
	if !ok {
		t.Fatal("record dropped")
	}
	if rec.ID != "claude-sonnet-4-5" || rec.DisplayName != "Claude Sonnet 4.5" || rec.Provider != "anthropic" {
		t.Fatalf("identity = %+v", rec)
	}
	if rec.CreatedAt == nil || rec.CreatedAt.UTC().Format(time.RFC3339) != "2026-11-04T00:00:00Z" {
		t.Fatalf("created_at = %v", rec.CreatedAt)
	}
	if rec.ContextWindow == nil || *rec.ContextWindow != 200000 {
		t.Fatalf("context = %v", rec.ContextWindow)
	}
	if rec.MaxOutputTokens == nil || *rec.MaxOutputTokens != 8192 {
		t.Fatalf("max output = %v", rec.MaxOutputTokens)
	}
	wantCaps := []string{"text", "image", "pdf", "tools", "reasoning"}
	if strings.Join(rec.Capabilities, ",") != strings.Join(wantCaps, ",") {
		t.Fatalf("capabilities = %v", rec.Capabilities)
	}
	if strings.Join(rec.InputModalities, ",") != "text,image" {
		t.Fatalf("modalities = %v", rec.InputModalities)
	}
	if len(rec.ReasoningEfforts) == 0 {
		t.Fatal("reasoning efforts missing")
	}
	if rec.Raw == nil || rec.Raw["id"] != raw["id"] {
		t.Fatal("raw record not preserved")
	}
}

func TestNormalizeDefaults(t *testing.T) {
	rec, ok := NormalizeRecord(map[string]any{"id": "bare"}, "local", DialectOpenAI)
	if !ok {
		t.Fatal("record dropped")
	}
	if rec.DisplayName != "bare" || rec.Provider != "local" || rec.Lifecycle != "active" {
		t.Fatalf("defaults = %+v", rec)
	}
	if strings.Join(rec.Capabilities, ",") != "text" {
		t.Fatalf("capabilities = %v", rec.Capabilities)
	}
	if strings.Join(rec.InputModalities, ",") != "text" {
		t.Fatalf("modalities = %v", rec.InputModalities)
	}
}

func TestNormalizeDropsMissingID(t *testing.T) {
	if _, ok := NormalizeRecord(map[string]any{"display_name": "x"}, "local", DialectOpenAI); ok {
		t.Fatal("record without id must be dropped")
	}
}

func TestFetchAnthropicDeduplicatesAndSendsHeaders(t *testing.T) {
	var gotKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{
				map[string]any{"id": "a", "type": "model"},
				map[string]any{"id": "a", "type": "model"},
				map[string]any{"id": "b", "type": "model"},
			},
		})
	}))
	defer srv.Close()
	cat, err := Fetch(context.Background(), FetchOptions{
		Profile: "p", BaseURL: srv.URL, Dialect: DialectAnthropic, Token: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "secret" || gotVersion != "2023-06-01" {
		t.Fatalf("headers = key %q version %q", gotKey, gotVersion)
	}
	if len(cat.Models) != 2 || cat.Models[0].ID != "a" || cat.Models[1].ID != "b" {
		t.Fatalf("dedupe = %+v", cat.Models)
	}
	if cat.Counts.Total != 2 {
		t.Fatalf("counts = %+v", cat.Counts)
	}
}

func TestFetchOpenAIModelsObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": map[string]any{
				"one": map[string]any{"name": "One", "context_length": 1000},
				"two": "Two",
			},
		})
	}))
	defer srv.Close()
	cat, err := Fetch(context.Background(), FetchOptions{Profile: "p", BaseURL: srv.URL, Dialect: DialectOpenAI, Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Models) != 2 || cat.Models[0].ID != "one" || cat.Models[1].ID != "two" {
		t.Fatalf("models = %+v", cat.Models)
	}
}

func TestFetchPagination(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.RawQuery)
		if r.URL.Query().Get("after_id") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":     []any{map[string]any{"id": "a"}},
				"has_more": true,
				"last_id":  "a",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":     []any{map[string]any{"id": "b"}},
			"has_more": false,
		})
	}))
	defer srv.Close()
	cat, err := Fetch(context.Background(), FetchOptions{Profile: "p", BaseURL: srv.URL, Dialect: DialectAnthropic, MaxPages: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Models) != 2 || cat.Models[1].ID != "b" {
		t.Fatalf("models = %+v", cat.Models)
	}
	if len(pages) != 2 || !strings.Contains(pages[1], "after_id=a") {
		t.Fatalf("pages = %v", pages)
	}
}

func TestFetchErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "auth", status: 401, body: `{}`, want: KindAuth},
		{name: "forbidden", status: 403, body: `{}`, want: KindAuth},
		{name: "not found", status: 404, body: `{}`, want: KindNotFound},
		{name: "upstream", status: 500, body: `{}`, want: KindUpstream},
		{name: "parse", status: 200, body: `not json`, want: KindParse},
		{name: "empty", status: 200, body: `{"data":[]}`, want: KindEmpty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := Fetch(context.Background(), FetchOptions{Profile: "p", BaseURL: srv.URL, Dialect: DialectOpenAI})
			if err == nil {
				t.Fatalf("expected %s error", tc.want)
			}
			fe, ok := err.(*FetchError)
			if !ok || fe.Kind != tc.want {
				t.Fatalf("error = %#v, want kind %s", err, tc.want)
			}
		})
	}
}
