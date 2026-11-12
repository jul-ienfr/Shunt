package clients

import (
	"fmt"
	"strconv"
	"strings"
)

// DSHOptions describes the DSH provider fragment.
type DSHOptions struct {
	RelayOrigin   string
	API           string // anthropic, openai, or both
	AnthropicID   string
	AnthropicName string
	AnthropicCtx  int64
	OpenAIID      string
	OpenAIName    string
	OpenAICtx     int64
	Profile       string
}

// DSHFragment returns a merge-ready cordis.patch.yml fragment. It never writes
// to disk and never contains a credential value.
func DSHFragment(o DSHOptions) string {
	api := strings.ToLower(strings.TrimSpace(o.API))
	if api == "" {
		api = "both"
	}
	origin := strings.TrimRight(o.RelayOrigin, "/")
	var b strings.Builder
	b.WriteString("# Merge this fragment into the existing `- id: llm-pi-ai` row.\n")
	b.WriteString("# A patch replaces that row's whole config block: preserve retryPolicy,\n")
	b.WriteString("# image budgets, defaultContextWindow, and any other existing keys.\n")
	b.WriteString("# Do not add a second row with the same plugin name.\n")
	b.WriteString("# The token stays in the environment; apiKeyEnv is only a reference.\n")
	b.WriteString("- id: llm-pi-ai\n")
	b.WriteString("  config:\n")
	b.WriteString("    providers:\n")
	if api == "anthropic" || api == "both" {
		fmt.Fprintf(&b, "      %s:\n", yamlKey(providerID("shunt-anthropic", o.Profile)))
		b.WriteString("        displayName: Shunt (Anthropic)\n")
		b.WriteString("        apiKeyEnv: SHUNT_TOKEN\n")
		b.WriteString("        api: anthropic-messages\n")
		fmt.Fprintf(&b, "        baseURL: %s\n", yamlScalar(origin))
		b.WriteString("        models:\n")
		fmt.Fprintf(&b, "          - id: %s\n", yamlScalar(o.AnthropicID))
		fmt.Fprintf(&b, "            name: %s\n", yamlScalar(o.AnthropicName))
		if o.AnthropicCtx > 0 {
			fmt.Fprintf(&b, "            contextWindow: %d\n", o.AnthropicCtx)
		}
	}
	if api == "openai" || api == "both" {
		fmt.Fprintf(&b, "      %s:\n", yamlKey(providerID("shunt-openai", o.Profile)))
		b.WriteString("        displayName: Shunt (OpenAI)\n")
		b.WriteString("        apiKeyEnv: SHUNT_TOKEN\n")
		b.WriteString("        api: openai-completions\n")
		fmt.Fprintf(&b, "        baseURL: %s\n", yamlScalar(origin+"/v1"))
		b.WriteString("        models:\n")
		fmt.Fprintf(&b, "          - id: %s\n", yamlScalar(o.OpenAIID))
		fmt.Fprintf(&b, "            name: %s\n", yamlScalar(o.OpenAIName))
		if o.OpenAICtx > 0 {
			fmt.Fprintf(&b, "            contextWindow: %d\n", o.OpenAICtx)
		}
	}
	b.WriteString("# If the provider list does not refresh, reopen DSH Settings.\n")
	return b.String()
}

func providerID(base, profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return base
	}
	return base + "-" + profile
}

func yamlKey(s string) string { return s }

func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && !strings.ContainsRune("-_./:@[]", r) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return strconv.Quote(s)
}
