package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Dialect names.
const (
	DialectAnthropic = "anthropic"
	DialectOpenAI    = "openai"
)

// Fetch error kinds. They let the CLI and GUI map a failure to the documented
// message and exit code without inspecting transport errors.
const (
	KindAuth        = "auth"
	KindNotFound    = "not_found"
	KindUpstream    = "upstream"
	KindUnreachable = "unreachable"
	KindParse       = "parse"
	KindEmpty       = "empty"
)

// FetchError describes a model-listing failure. It never contains a token or
// response body beyond a short parse message.
type FetchError struct {
	Kind    string
	Profile string
	URL     string
	Status  int
	Err     error
}

func (e *FetchError) Error() string {
	switch e.Kind {
	case KindAuth:
		return fmt.Sprintf("authentication failed for %s (HTTP %d)", e.Profile, e.Status)
	case KindNotFound:
		return fmt.Sprintf("model listing not found at %s", e.URL)
	case KindUnreachable:
		return fmt.Sprintf("cannot reach %s at %s: %s", e.Profile, e.URL, shortError(e.Err))
	case KindParse:
		return fmt.Sprintf("unrecognized model listing from %s; expected data[] or models[]", e.Profile)
	case KindEmpty:
		return fmt.Sprintf("no models returned by %s", e.Profile)
	case KindUpstream:
		if e.Status != 0 {
			return fmt.Sprintf("upstream returned HTTP %d for %s", e.Status, e.URL)
		}
		return fmt.Sprintf("upstream request to %s failed: %s", e.URL, shortError(e.Err))
	default:
		return fmt.Sprintf("model listing from %s failed", e.Profile)
	}
}

func (e *FetchError) Unwrap() error { return e.Err }

func shortError(err error) string {
	if err == nil {
		return "unknown error"
	}
	msg := strings.ReplaceAll(err.Error(), "\n", " ")
	msg = strings.ReplaceAll(msg, "\r", " ")
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

// FetchOptions controls one catalog fetch.
type FetchOptions struct {
	Profile    string
	BaseURL    string
	Dialect    string
	Token      string
	HTTPClient *http.Client
	PageSize   int
	MaxPages   int
	MaxModels  int
	Source     string
}

// ModelsURL returns the canonical listing URL for a profile origin.
func ModelsURL(baseURL, dialect string) string {
	base := strings.TrimRight(baseURL, "/")
	if dialect == DialectAnthropic {
		return base + "/v1/models"
	}
	return base + "/v1/models"
}

// ResolveToken looks up the token in the documented order. It returns the value
// and the environment variable name that supplied it, or empty values.
func ResolveToken(lookup func(string) string, preferred string) (string, string) {
	if lookup == nil {
		lookup = os.Getenv
	}
	names := []string{}
	if preferred != "" {
		names = append(names, preferred)
	}
	names = append(names, "SHUNT_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "OPENAI_API_KEY")
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if v := strings.TrimSpace(lookup(name)); v != "" {
			return v, name
		}
	}
	return "", ""
}

// Fetch retrieves and normalizes a model catalog for one profile and dialect.
func Fetch(ctx context.Context, opts FetchOptions) (Catalog, error) {
	dialect := strings.ToLower(strings.TrimSpace(opts.Dialect))
	if dialect == "" {
		dialect = DialectAnthropic
	}
	if dialect != DialectAnthropic && dialect != DialectOpenAI {
		return Catalog{}, fmt.Errorf("dialect must be anthropic or openai")
	}
	if strings.TrimSpace(opts.BaseURL) == "" {
		return Catalog{}, fmt.Errorf("profile %q has no base URL", opts.Profile)
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 1000
	}
	maxPages := opts.MaxPages
	if maxPages <= 0 {
		maxPages = 10
	}
	maxModels := opts.MaxModels
	if maxModels <= 0 {
		maxModels = 5000
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	next := ModelsURL(opts.BaseURL, dialect)
	if dialect == DialectAnthropic {
		next = withQuery(next, url.Values{"limit": []string{strconv.Itoa(pageSize)}})
	}
	seenIDs := map[string]bool{}
	var records []Record
	lastID := ""
	for page := 0; page < maxPages; page++ {
		body, err := fetchPage(ctx, client, opts, dialect, next)
		if err != nil {
			return Catalog{}, err
		}
		rawRecords, hasMore, pageLastID, err := extractRecords(body)
		if err != nil {
			return Catalog{}, &FetchError{Kind: KindParse, Profile: opts.Profile, URL: next, Err: err}
		}
		for _, raw := range rawRecords {
			rec, ok := NormalizeRecord(raw, opts.Profile, dialect)
			if !ok {
				continue
			}
			if seenIDs[rec.ID] {
				continue
			}
			seenIDs[rec.ID] = true
			records = append(records, rec)
			if len(records) >= maxModels {
				break
			}
		}
		if len(records) >= maxModels {
			break
		}
		if !hasMore || pageLastID == "" || pageLastID == lastID {
			break
		}
		lastID = pageLastID
		values := url.Values{"after_id": []string{lastID}}
		if dialect == DialectAnthropic {
			values.Set("limit", strconv.Itoa(pageSize))
		}
		next = withQuery(ModelsURL(opts.BaseURL, dialect), values)
	}
	if len(records) == 0 {
		return Catalog{}, &FetchError{Kind: KindEmpty, Profile: opts.Profile, URL: ModelsURL(opts.BaseURL, dialect)}
	}
	now := time.Now().UTC()
	source := opts.Source
	if source == "" {
		source = "direct"
	}
	cat := Catalog{
		Schema:    1,
		Profile:   opts.Profile,
		Dialect:   dialect,
		Source:    source,
		SourceURL: ModelsURL(opts.BaseURL, dialect),
		FetchedAt: now,
		Models:    records,
	}
	cat.Recount()
	return cat, nil
}

func fetchPage(ctx context.Context, client *http.Client, opts FetchOptions, dialect, target string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, &FetchError{Kind: KindUnreachable, Profile: opts.Profile, URL: target, Err: err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "shunt-models/1")
	if dialect == DialectAnthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
		if opts.Token != "" {
			req.Header.Set("x-api-key", opts.Token)
		}
	} else if opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &FetchError{Kind: KindUnreachable, Profile: opts.Profile, URL: target, Err: err}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, &FetchError{Kind: KindAuth, Profile: opts.Profile, URL: target, Status: resp.StatusCode}
	case resp.StatusCode == http.StatusNotFound:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, &FetchError{Kind: KindNotFound, Profile: opts.Profile, URL: target, Status: resp.StatusCode}
	case resp.StatusCode >= 500:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, &FetchError{Kind: KindUpstream, Profile: opts.Profile, URL: target, Status: resp.StatusCode}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, &FetchError{Kind: KindUpstream, Profile: opts.Profile, URL: target, Status: resp.StatusCode}
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 64<<20))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return nil, &FetchError{Kind: KindParse, Profile: opts.Profile, URL: target, Err: err}
	}
	return body, nil
}

func extractRecords(body map[string]any) ([]map[string]any, bool, string, error) {
	hasMore := boolValue(body["has_more"])
	lastID := firstString(body, "last_id")
	if data, ok := body["data"].([]any); ok {
		return recordMaps(data), hasMore, lastID, nil
	}
	if models, ok := body["models"].([]any); ok {
		return recordMaps(models), hasMore, lastID, nil
	}
	if models, ok := body["models"].(map[string]any); ok {
		keys := make([]string, 0, len(models))
		for key := range models {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make([]map[string]any, 0, len(keys))
		for _, key := range keys {
			switch v := models[key].(type) {
			case map[string]any:
				if firstString(v, "id", "model") == "" {
					v["id"] = key
				}
				out = append(out, v)
			case string:
				out = append(out, map[string]any{"id": key, "name": v})
			default:
				out = append(out, map[string]any{"id": key})
			}
		}
		return out, hasMore, lastID, nil
	}
	return nil, false, "", errors.New("no data or models array")
}

func recordMaps(items []any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case map[string]any:
			out = append(out, v)
		case string:
			out = append(out, map[string]any{"id": v})
		}
	}
	return out
}

func withQuery(target string, values url.Values) string {
	u, err := url.Parse(target)
	if err != nil {
		return target
	}
	q := u.Query()
	for key, vals := range values {
		for _, v := range vals {
			q.Set(key, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
