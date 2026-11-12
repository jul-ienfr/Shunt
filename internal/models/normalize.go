package models

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Classification values.
const (
	FreeFree    = "free"
	FreePaid    = "paid"
	FreeUnknown = "unknown"
)

// Cost is a normalized price record. Values are USD per million tokens.
// Request and image prices are kept for classification but are not part of the
// documented normalized cost object.
type Cost struct {
	InputPerMillion  *float64 `json:"input_per_million,omitempty"`
	OutputPerMillion *float64 `json:"output_per_million,omitempty"`
	Currency         string   `json:"currency,omitempty"`

	requestPerMillion *float64
	imagePerMillion   *float64
}

// Record is one normalized model entry.
type Record struct {
	ID               string         `json:"id"`
	DisplayName      string         `json:"display_name,omitempty"`
	Provider         string         `json:"provider,omitempty"`
	CreatedAt        *time.Time     `json:"created_at,omitempty"`
	ContextWindow    *int64         `json:"context_window,omitempty"`
	MaxOutputTokens  *int64         `json:"max_output_tokens,omitempty"`
	Capabilities     []string       `json:"capabilities,omitempty"`
	InputModalities  []string       `json:"input_modalities,omitempty"`
	ReasoningEfforts []string       `json:"reasoning_efforts,omitempty"`
	Lifecycle        string         `json:"lifecycle,omitempty"`
	Cost             *Cost          `json:"cost,omitempty"`
	Free             string         `json:"free"`
	Raw              map[string]any `json:"raw,omitempty"`
}

// Counts summarizes a catalog by free/paid/unknown status.
type Counts struct {
	Total   int `json:"total"`
	Free    int `json:"free"`
	Paid    int `json:"paid"`
	Unknown int `json:"unknown"`
}

// Catalog is a collection of model records for one profile and dialect.
type Catalog struct {
	Schema      int       `json:"schema"`
	Profile     string    `json:"profile"`
	Dialect     string    `json:"dialect"`
	Source      string    `json:"source,omitempty"`
	SourceURL   string    `json:"source_url"`
	FetchedAt   time.Time `json:"fetched_at"`
	StaleAfter  time.Time `json:"stale_after"`
	Stale       bool      `json:"stale"`
	StaleReason *string   `json:"stale_reason,omitempty"`
	Counts      Counts    `json:"counts"`
	Models      []Record  `json:"models"`
}

// Recount recalculates the free/paid/unknown totals.
func (c *Catalog) Recount() {
	counts := Counts{Total: len(c.Models)}
	for _, m := range c.Models {
		switch m.Free {
		case FreeFree:
			counts.Free++
		case FreePaid:
			counts.Paid++
		default:
			counts.Unknown++
		}
	}
	c.Counts = counts
}

// NormalizeRecord converts one upstream object into a Record. It returns false
// when the object has no usable identifier.
func NormalizeRecord(raw map[string]any, profile, dialect string) (Record, bool) {
	id := firstString(raw, "id", "model", "model_id", "name")
	if id == "" {
		return Record{}, false
	}
	r := Record{
		ID:          id,
		DisplayName: firstString(raw, "display_name", "displayName", "name", "title"),
		Provider:    firstString(raw, "owned_by", "ownedBy", "provider", "provider_name"),
		Lifecycle:   "active",
		Raw:         raw,
	}
	if r.DisplayName == "" {
		r.DisplayName = id
	}
	if r.Provider == "" {
		r.Provider = profile
	}
	r.CreatedAt = parseCreated(raw)
	r.ContextWindow = firstInt(raw, "max_input_tokens", "context_window", "context_length", "max_context_length", "input_tokens")
	r.MaxOutputTokens = firstInt(raw, "max_tokens", "max_output_tokens", "max_completion_tokens", "output_tokens")
	if top, ok := raw["top_provider"].(map[string]any); ok {
		if r.MaxOutputTokens == nil {
			r.MaxOutputTokens = firstInt(top, "max_completion_tokens", "max_output_tokens")
		}
	}
	if arch, ok := raw["architecture"].(map[string]any); ok {
		r.InputModalities = stringSlice(arch["input_modalities"])
	}

	caps := map[string]bool{"text": true}
	collectCapabilities(raw, caps)
	r.Capabilities = orderedCapabilities(caps)

	if len(r.InputModalities) == 0 {
		r.InputModalities = []string{"text"}
		if caps["image"] {
			r.InputModalities = append(r.InputModalities, "image")
		}
	}
	r.ReasoningEfforts = parseReasoning(raw, caps)
	r.Lifecycle = parseLifecycle(raw, r.Lifecycle)
	r.Cost = parseCost(raw)
	r.Free = classifyFree(raw, r.Cost, id)
	return r, true
}

func collectCapabilities(raw map[string]any, caps map[string]bool) {
	if c, ok := raw["capabilities"].(map[string]any); ok {
		if supported(c["image_input"]) || boolValue(c["vision"]) || boolValue(c["image"]) {
			caps["image"] = true
		}
		if supported(c["pdf_input"]) || boolValue(c["pdf"]) {
			caps["pdf"] = true
		}
		if supported(c["thinking"]) || supported(c["reasoning"]) {
			caps["reasoning"] = true
		}
		if supported(c["tool_use"]) || supported(c["tools"]) || supported(c["function_calling"]) {
			caps["tools"] = true
		}
	}
	for _, key := range []string{"tools", "tool_use", "function_calling", "supports_tools", "supports_functions"} {
		if boolValue(raw[key]) {
			caps["tools"] = true
		}
	}
	for _, key := range []string{"vision", "image_input", "supports_vision"} {
		if boolValue(raw[key]) {
			caps["image"] = true
		}
	}
	for _, key := range []string{"reasoning", "thinking", "supports_reasoning"} {
		if boolValue(raw[key]) {
			caps["reasoning"] = true
		}
	}
	if params := stringSlice(raw["supported_parameters"]); len(params) > 0 {
		for _, p := range params {
			switch strings.ToLower(p) {
			case "tools", "tool_choice", "function_calling":
				caps["tools"] = true
			case "reasoning", "reasoning_effort", "thinking":
				caps["reasoning"] = true
			}
		}
	}
}

func orderedCapabilities(caps map[string]bool) []string {
	order := []string{"text", "image", "pdf", "tools", "reasoning"}
	out := make([]string, 0, len(caps))
	for _, key := range order {
		if caps[key] {
			out = append(out, key)
		}
	}
	return out
}

func parseReasoning(raw map[string]any, caps map[string]bool) []string {
	for _, key := range []string{"reasoning_efforts", "reasoning_effort", "reasoning_levels"} {
		if vals := stringSlice(raw[key]); len(vals) > 0 {
			return vals
		}
	}
	if !caps["reasoning"] {
		return nil
	}
	if params := stringSlice(raw["supported_parameters"]); containsFold(params, "reasoning_effort") {
		return []string{"off", "low", "medium", "high"}
	}
	return []string{"off", "low", "medium", "high"}
}

func parseLifecycle(raw map[string]any, fallback string) string {
	value := strings.ToLower(firstString(raw, "lifecycle", "status", "state"))
	switch value {
	case "active", "stable", "ga", "generally_available":
		return "active"
	case "preview", "beta", "experimental":
		return "preview"
	case "deprecated", "retired", "sunset":
		return "deprecated"
	}
	return fallback
}

func parseCreated(raw map[string]any) *time.Time {
	for _, key := range []string{"created_at", "createdAt", "release_date"} {
		if s, ok := raw[key].(string); ok && s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				u := t.UTC()
				return &u
			}
		}
	}
	for _, key := range []string{"created", "created_unix", "created_timestamp"} {
		if n, ok := toInt64(raw[key]); ok && n > 0 {
			t := time.Unix(n, 0).UTC()
			return &t
		}
	}
	return nil
}

func parseCost(raw map[string]any) *Cost {
	var prompt, completion, request, image *float64
	if pricing, ok := raw["pricing"].(map[string]any); ok {
		prompt = decimal(pricing["prompt"])
		completion = decimal(pricing["completion"])
		request = decimal(pricing["request"])
		image = decimal(pricing["image"])
	}
	if cost, ok := raw["cost"].(map[string]any); ok {
		if prompt == nil {
			prompt = decimal(cost["input"])
		}
		if completion == nil {
			completion = decimal(cost["output"])
		}
	}
	if prompt == nil && completion == nil && request == nil && image == nil {
		return nil
	}
	c := &Cost{Currency: "USD"}
	if prompt != nil {
		v := *prompt * 1e6
		c.InputPerMillion = &v
	}
	if completion != nil {
		v := *completion * 1e6
		c.OutputPerMillion = &v
	}
	if request != nil {
		v := *request * 1e6
		c.requestPerMillion = &v
	}
	if image != nil {
		v := *image * 1e6
		c.imagePerMillion = &v
	}
	return c
}

func classifyFree(raw map[string]any, cost *Cost, id string) string {
	if v, ok := boolPointer(raw["free"]); ok {
		if v {
			return FreeFree
		}
		return FreePaid
	}
	if v, ok := boolPointer(raw["is_free"]); ok {
		if v {
			return FreeFree
		}
		return FreePaid
	}
	if tier := strings.ToLower(firstString(raw, "tier", "plan", "pricing_tier")); tier != "" {
		switch {
		case strings.Contains(tier, "free"), strings.Contains(tier, "trial"):
			return FreeFree
		case strings.Contains(tier, "paid"), strings.Contains(tier, "premium"), strings.Contains(tier, "pro"):
			return FreePaid
		}
	}
	if cost != nil {
		hasComponent := false
		allZero := true
		for _, v := range []*float64{cost.InputPerMillion, cost.OutputPerMillion, cost.requestPerMillion, cost.imagePerMillion} {
			if v == nil {
				continue
			}
			hasComponent = true
			if *v != 0 {
				allZero = false
			}
			if *v > 0 {
				return FreePaid
			}
		}
		if hasComponent && allZero {
			return FreeFree
		}
	}
	if strings.HasSuffix(strings.ToLower(id), ":free") {
		return FreeFree
	}
	return FreeUnknown
}

func supported(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		if b, ok := boolPointer(t["supported"]); ok {
			return b
		}
		if b, ok := boolPointer(t["enabled"]); ok {
			return b
		}
		if b, ok := boolPointer(t["available"]); ok {
			return b
		}
		return true
	default:
		if b, ok := boolPointer(v); ok {
			return b
		}
		return false
	}
}

func boolValue(v any) bool {
	b, ok := boolPointer(v)
	return ok && b
}

func boolPointer(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(t))
		if err == nil {
			return parsed, true
		}
	case map[string]any:
		return supported(t), true
	}
	return false, false
}

func firstString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := raw[key].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func firstInt(raw map[string]any, keys ...string) *int64 {
	for _, key := range keys {
		if n, ok := toInt64(raw[key]); ok && n > 0 {
			v := n
			return &v
		}
	}
	return nil
}

func toInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int64:
		return t, true
	case float64:
		return int64(t), true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	}
	return 0, false
}

func decimal(v any) *float64 {
	var text string
	switch t := v.(type) {
	case string:
		text = strings.TrimSpace(t)
	case float64:
		f := t
		return &f
	case int:
		f := float64(t)
		return &f
	case int64:
		f := float64(t)
		return &f
	case json.Number:
		text = t.String()
	default:
		return nil
	}
	if text == "" {
		return nil
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || f < 0 {
		return nil
	}
	return &f
}

func stringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return append([]string(nil), t...)
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}
		parts := strings.Split(t, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func containsFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}
