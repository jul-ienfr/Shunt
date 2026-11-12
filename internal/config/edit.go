package config

import (
	"fmt"
	"strconv"
	"strings"
)

// TOMLString quotes a string as a TOML basic string.
func TOMLString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TOMLBool formats a bool as a TOML literal.
func TOMLBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// TOMLInt formats an int as a TOML literal.
func TOMLInt(v int) string { return strconv.Itoa(v) }

// EditValue replaces a single key inside a section of an existing TOML
// document while preserving comments, blank lines, ordering, and unrelated
// keys. An empty section edits a top-level key. When the key is absent it is
// inserted at the end of the section; a missing section is appended.
func EditValue(text, section, key, tomlValue string) (string, error) {
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.Split(text, "\n")
	// Normalize CR so matching is simple; the original newline is restored
	// through the split character below.
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	current := ""
	found := -1
	sectionStart := -1
	sectionEnd := len(lines)
	if section == "" {
		sectionStart = 0
	}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			if current == section && sectionStart >= 0 {
				sectionEnd = i
			}
			current = parseSectionName(trimmed)
			if current == section && sectionStart < 0 {
				sectionStart = i
			}
			continue
		}
		if current != section || !lineHasKey(line, key) {
			continue
		}
		if found >= 0 {
			return "", fmt.Errorf("ambiguous TOML edit: duplicate key %q in [%s]", key, section)
		}
		_ = trimmed
		found = i
	}
	if found >= 0 {
		lines[found] = replaceLineValue(lines[found], key, tomlValue)
		return joinLines(lines, newline), nil
	}
	insert := sectionEnd
	for insert > sectionStart+1 && strings.TrimSpace(lines[insert-1]) == "" {
		insert--
	}
	if sectionStart < 0 {
		// Append a new section.
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, "", "["+section+"]", key+" = "+tomlValue, "")
		return joinLines(lines, newline), nil
	}
	if insert == len(lines) && len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines[len(lines)-1] = key + " = " + tomlValue
		lines = append(lines, "")
		return joinLines(lines, newline), nil
	}
	lines = append(lines, "")
	copy(lines[insert+1:], lines[insert:])
	lines[insert] = key + " = " + tomlValue
	return joinLines(lines, newline), nil
}

// SetListen replaces relay.listen.
func SetListen(text, listen string) (string, error) {
	return EditValue(text, "relay", "listen", TOMLString(listen))
}

// SetAllowNonLoopback replaces relay.allow_non_loopback.
func SetAllowNonLoopback(text string, allow bool) (string, error) {
	return EditValue(text, "relay", "allow_non_loopback", TOMLBool(allow))
}

// SetProfileBaseURL replaces a profile's base_url and, when non-empty,
// probe_url. The caller is responsible for providing a normalized origin.
func SetProfileBaseURL(text, profile, baseURL, probeURL string) (string, error) {
	out, err := EditValue(text, "profiles."+profile, "base_url", TOMLString(baseURL))
	if err != nil {
		return "", err
	}
	if probeURL != "" {
		out, err = EditValue(out, "profiles."+profile, "probe_url", TOMLString(probeURL))
		if err != nil {
			return "", err
		}
	}
	return out, nil
}

// SetTopLevel replaces a top-level key.
func SetTopLevel(text, key, tomlValue string) (string, error) {
	return EditValue(text, "", key, tomlValue)
}

func parseSectionName(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "[[") {
		end := strings.Index(line, "]]")
		if end < 0 {
			return ""
		}
		return strings.TrimSpace(line[2:end])
	}
	end := strings.Index(line, "]")
	if end < 1 {
		return ""
	}
	return strings.TrimSpace(line[1:end])
}

func lineHasKey(line, key string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, key) {
		return false
	}
	rest := trimmed[len(key):]
	rest = strings.TrimLeft(rest, " \t")
	return strings.HasPrefix(rest, "=")
}

func replaceLineValue(line, key, tomlValue string) string {
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	comment := trailingComment(line)
	out := indent + key + " = " + tomlValue
	if comment != "" {
		out += " " + comment
	}
	return out
}

// trailingComment returns a trailing # comment when it is outside quotes.
func trailingComment(line string) string {
	inSingle, inDouble, escaped := false, false, false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inDouble {
			escaped = true
			continue
		}
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return strings.TrimSpace(line[i:])
			}
		}
	}
	return ""
}

func joinLines(lines []string, newline string) string {
	if newline == "\r\n" {
		return strings.Join(lines, "\r\n")
	}
	return strings.Join(lines, "\n")
}
