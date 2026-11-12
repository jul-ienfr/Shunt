package clients

import (
	"fmt"
	"strings"

	"shunt/internal/config"
)

// CodexOptions describes the Codex provider block.
type CodexOptions struct {
	RelayOrigin string
	Model       string
	Force       bool
}

// CodexBaseURL returns the relay origin with the protocol-required /v1 suffix.
func CodexBaseURL(origin string) string {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if strings.HasSuffix(origin, "/v1") {
		return origin
	}
	return origin + "/v1"
}

// CodexSnippet returns the standalone TOML snippet printed by default.
func CodexSnippet(o CodexOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "model = %s\n", config.TOMLString(o.Model))
	b.WriteString("model_provider = \"shunt\"\n\n")
	b.WriteString("[model_providers.shunt]\n")
	b.WriteString("name = \"shunt\"\n")
	fmt.Fprintf(&b, "base_url = %s\n", config.TOMLString(CodexBaseURL(o.RelayOrigin)))
	b.WriteString("env_key = \"SHUNT_API_KEY\"\n")
	b.WriteString("wire_api = \"responses\"\n")
	return b.String()
}

// MergeCodex edits an existing config.toml with the comment-preserving line
// editor. model and model_provider are changed only when absent unless Force is
// set. The provider block is refreshed in place.
func MergeCodex(existing string, o CodexOptions) (string, []string, error) {
	text := existing
	if strings.TrimSpace(text) == "" {
		text = ""
	}
	var changed []string
	if o.Force || !hasTopLevelKey(text, "model") {
		var err error
		text, err = config.EditValue(text, "", "model", config.TOMLString(o.Model))
		if err != nil {
			return "", nil, err
		}
		changed = append(changed, "model")
	}
	if o.Force || !hasTopLevelKey(text, "model_provider") {
		var err error
		text, err = config.EditValue(text, "", "model_provider", config.TOMLString("shunt"))
		if err != nil {
			return "", nil, err
		}
		changed = append(changed, "model_provider")
	}
	section := "model_providers.shunt"
	for _, pair := range [][2]string{
		{"name", config.TOMLString("shunt")},
		{"base_url", config.TOMLString(CodexBaseURL(o.RelayOrigin))},
		{"env_key", config.TOMLString("SHUNT_API_KEY")},
		{"wire_api", config.TOMLString("responses")},
	} {
		var err error
		text, err = config.EditValue(text, section, pair[0], pair[1])
		if err != nil {
			return "", nil, err
		}
		changed = append(changed, section+"."+pair[0])
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text, changed, nil
}

func hasTopLevelKey(text, key string) bool {
	section := ""
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(trimmed, "[") {
			section = trimmed
			continue
		}
		if section != "" || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, key) {
			rest := strings.TrimLeft(trimmed[len(key):], " \t")
			if strings.HasPrefix(rest, "=") {
				return true
			}
		}
	}
	return false
}
