package clients

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ClaudeOptions describes a Claude Code configuration.
type ClaudeOptions struct {
	BaseURL    string
	Model      string
	Opus       string
	Sonnet     string
	Haiku      string
	Fable      string
	Legacy     bool
	PowerShell bool
}

func (o ClaudeOptions) aliases() map[string]string {
	pick := func(v string) string {
		if strings.TrimSpace(v) != "" {
			return v
		}
		return o.Model
	}
	return map[string]string{
		"ANTHROPIC_MODEL":                o.Model,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   pick(o.Opus),
		"ANTHROPIC_DEFAULT_SONNET_MODEL": pick(o.Sonnet),
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  pick(o.Haiku),
		"ANTHROPIC_DEFAULT_FABLE_MODEL":  pick(o.Fable),
	}
}

// ClaudeShell returns the recommended environment snippet. It never contains a
// literal credential.
func ClaudeShell(o ClaudeOptions) string {
	base := strings.TrimRight(o.BaseURL, "/")
	if o.PowerShell {
		var b strings.Builder
		fmt.Fprintf(&b, "$env:ANTHROPIC_BASE_URL = '%s'\n", base)
		fmt.Fprintf(&b, "$env:ANTHROPIC_MODEL = '%s'\n", o.Model)
		fmt.Fprintf(&b, "$env:ANTHROPIC_DEFAULT_OPUS_MODEL = '%s'\n", aliases(o, "ANTHROPIC_DEFAULT_OPUS_MODEL"))
		fmt.Fprintf(&b, "$env:ANTHROPIC_DEFAULT_SONNET_MODEL = '%s'\n", aliases(o, "ANTHROPIC_DEFAULT_SONNET_MODEL"))
		fmt.Fprintf(&b, "$env:ANTHROPIC_DEFAULT_HAIKU_MODEL = '%s'\n", aliases(o, "ANTHROPIC_DEFAULT_HAIKU_MODEL"))
		fmt.Fprintf(&b, "$env:ANTHROPIC_DEFAULT_FABLE_MODEL = '%s'\n", aliases(o, "ANTHROPIC_DEFAULT_FABLE_MODEL"))
		if o.Legacy {
			fmt.Fprintf(&b, "$env:ANTHROPIC_SMALL_FAST_MODEL = '%s'\n", o.Model)
		}
		b.WriteString("# Keep your existing ANTHROPIC_API_KEY. Do not set ANTHROPIC_AUTH_TOKEN.\n")
		return b.String()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "export ANTHROPIC_BASE_URL=\"%s\"\n", base)
	fmt.Fprintf(&b, "export ANTHROPIC_MODEL=\"%s\"\n", o.Model)
	fmt.Fprintf(&b, "export ANTHROPIC_DEFAULT_OPUS_MODEL=\"%s\"\n", aliases(o, "ANTHROPIC_DEFAULT_OPUS_MODEL"))
	fmt.Fprintf(&b, "export ANTHROPIC_DEFAULT_SONNET_MODEL=\"%s\"\n", aliases(o, "ANTHROPIC_DEFAULT_SONNET_MODEL"))
	fmt.Fprintf(&b, "export ANTHROPIC_DEFAULT_HAIKU_MODEL=\"%s\"\n", aliases(o, "ANTHROPIC_DEFAULT_HAIKU_MODEL"))
	fmt.Fprintf(&b, "export ANTHROPIC_DEFAULT_FABLE_MODEL=\"%s\"\n", aliases(o, "ANTHROPIC_DEFAULT_FABLE_MODEL"))
	if o.Legacy {
		fmt.Fprintf(&b, "export ANTHROPIC_SMALL_FAST_MODEL=\"%s\"\n", o.Model)
	}
	b.WriteString("# Keep your existing ANTHROPIC_API_KEY. Do not set ANTHROPIC_AUTH_TOKEN.\n")
	return b.String()
}

func aliases(o ClaudeOptions, key string) string {
	return o.aliases()[key]
}

// ClaudeSettings returns a non-secret settings.json fragment. The API key is
// deliberately absent; it stays in the environment.
func ClaudeSettings(o ClaudeOptions) ([]byte, error) {
	env := map[string]any{"ANTHROPIC_BASE_URL": strings.TrimRight(o.BaseURL, "/")}
	for key, value := range o.aliases() {
		env[key] = value
	}
	if o.Legacy {
		env["ANTHROPIC_SMALL_FAST_MODEL"] = o.Model
	}
	doc := map[string]any{"env": env}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// MergeClaudeSettings merges generated non-secret env keys into an existing
// settings.json. Unrelated keys are preserved. An unparseable file is an error
// and is never rewritten.
func MergeClaudeSettings(existing []byte, o ClaudeOptions) ([]byte, []string, error) {
	doc := map[string]any{}
	if len(strings.TrimSpace(string(existing))) > 0 {
		if err := json.Unmarshal(existing, &doc); err != nil {
			return nil, nil, fmt.Errorf("parse settings.json: %w", err)
		}
	}
	var env map[string]any
	switch v := doc["env"].(type) {
	case nil:
		env = map[string]any{}
	case map[string]any:
		env = v
	default:
		return nil, nil, fmt.Errorf("settings.json env must be an object")
	}
	generated := map[string]string{"ANTHROPIC_BASE_URL": strings.TrimRight(o.BaseURL, "/")}
	for key, value := range o.aliases() {
		generated[key] = value
	}
	if o.Legacy {
		generated["ANTHROPIC_SMALL_FAST_MODEL"] = o.Model
	}
	changed := make([]string, 0, len(generated))
	for key, value := range generated {
		if old, ok := env[key]; !ok || old != value {
			changed = append(changed, key)
		}
		env[key] = value
	}
	sort.Strings(changed)
	doc["env"] = env
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return append(data, '\n'), changed, nil
}
