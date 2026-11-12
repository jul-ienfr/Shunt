package config

import (
	"fmt"
	"strconv"
	"strings"
)

// This file edits the TOML structures that cannot be addressed with the
// single-key helper in edit.go: array-of-table listeners and whole profile
// blocks. All functions preserve comments, ordering, and unrelated keys.

// sectionBlocks returns the [start,end) line range of every header exactly
// equal to header. end is the index of the next header or len(lines).
func sectionBlocks(lines []string, header string) [][2]int {
	var out [][2]int
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != header {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "[") {
				end = j
				break
			}
		}
		out = append(out, [2]int{i, end})
		i = end - 1
	}
	return out
}

// blockValue returns the unquoted value of key inside a block.
func blockValue(lines []string, start, end int, key string) (string, bool) {
	for i := start + 1; i < end; i++ {
		if !lineHasKey(lines[i], key) {
			continue
		}
		value := strings.TrimSpace(strings.SplitN(lines[i], "=", 2)[1])
		value = strings.SplitN(value, "#", 2)[0]
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted, true
		}
		return strings.Trim(value, `"'`), true
	}
	return "", false
}

// setKeyInRange replaces key inside one block, inserting it at the end of the
// block when absent.
func setKeyInRange(lines []string, start, end int, key, tomlValue string) []string {
	indent := ""
	for i := start + 1; i < end; i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		indent = lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
		break
	}
	for i := start + 1; i < end; i++ {
		if lineHasKey(lines[i], key) {
			lines[i] = replaceLineValue(lines[i], key, tomlValue)
			return lines
		}
	}
	insertAt := end
	for insertAt > start+1 && strings.TrimSpace(lines[insertAt-1]) == "" {
		insertAt--
	}
	lines = insertLines(lines, insertAt, indent+key+" = "+tomlValue)
	return lines
}

// insertLines inserts values at index without aliasing the caller's slice.
func insertLines(lines []string, index int, values ...string) []string {
	out := make([]string, 0, len(lines)+len(values))
	out = append(out, lines[:index]...)
	out = append(out, values...)
	out = append(out, lines[index:]...)
	return out
}

// removeLines deletes lines[start:end] plus one trailing blank line.
func removeLines(lines []string, start, end int) []string {
	if end < len(lines) && strings.TrimSpace(lines[end]) == "" {
		end++
	}
	out := make([]string, 0, len(lines)-(end-start))
	out = append(out, lines[:start]...)
	out = append(out, lines[end:]...)
	return out
}

// documentEdit normalizes newlines, applies edit, and restores the original
// newline style.
func documentEdit(text string, edit func([]string) ([]string, error)) (string, error) {
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	edited, err := edit(lines)
	if err != nil {
		return "", err
	}
	return joinLines(edited, newline), nil
}

// appendBlock adds a table block at the end of the document, separated by one
// blank line.
func appendBlock(lines []string, block []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	lines = append(lines, "")
	lines = append(lines, block...)
	lines = append(lines, "")
	return lines
}

// EditListener rewrites one [[listeners]] block matched by name.
func EditListener(text, name string, edit func([]string) ([]string, error)) (string, error) {
	return documentEdit(text, func(lines []string) ([]string, error) {
		for _, bounds := range sectionBlocks(lines, "[[listeners]]") {
			if value, ok := blockValue(lines, bounds[0], bounds[1], "name"); ok && value == name {
				block := append([]string(nil), lines[bounds[0]:bounds[1]]...)
				edited, err := edit(block)
				if err != nil {
					return nil, err
				}
				out := make([]string, 0, len(lines)-len(block)+len(edited))
				out = append(out, lines[:bounds[0]]...)
				out = append(out, edited...)
				out = append(out, lines[bounds[1]:]...)
				return out, nil
			}
		}
		return nil, fmt.Errorf("listener %q not found", name)
	})
}

// SetListenerListen replaces the listen key of one named listener.
func SetListenerListen(text, name, listen string) (string, error) {
	return EditListener(text, name, func(block []string) ([]string, error) {
		return setKeyInRange(block, 0, len(block), "listen", TOMLString(listen)), nil
	})
}

// AddListener appends a [[listeners]] block. It does not validate uniqueness;
// callers validate the resulting document with Parse.
func AddListener(text, name, listen, profile string) (string, error) {
	return documentEdit(text, func(lines []string) ([]string, error) {
		for _, bounds := range sectionBlocks(lines, "[[listeners]]") {
			if value, ok := blockValue(lines, bounds[0], bounds[1], "name"); ok && value == name {
				return nil, fmt.Errorf("listener %q already exists", name)
			}
		}
		block := []string{
			"[[listeners]]",
			"name = " + TOMLString(name),
			"listen = " + TOMLString(listen),
		}
		if profile != "" {
			block = append(block, "profile = "+TOMLString(profile))
		}
		block = append(block, "enabled = true")
		return appendBlock(lines, block), nil
	})
}

// RemoveListener deletes one named listener block.
func RemoveListener(text, name string) (string, error) {
	return documentEdit(text, func(lines []string) ([]string, error) {
		for _, bounds := range sectionBlocks(lines, "[[listeners]]") {
			if value, ok := blockValue(lines, bounds[0], bounds[1], "name"); ok && value == name {
				return removeLines(lines, bounds[0], bounds[1]), nil
			}
		}
		return nil, fmt.Errorf("listener %q not found", name)
	})
}

// AddProfile appends a [profiles.<id>] block. Callers validate the result.
func AddProfile(text, id, label, baseURL, probeURL string) (string, error) {
	return documentEdit(text, func(lines []string) ([]string, error) {
		header := "[profiles." + id + "]"
		if len(sectionBlocks(lines, header)) > 0 {
			return nil, fmt.Errorf("profile %q already exists", id)
		}
		if label == "" {
			label = id
		}
		block := []string{
			header,
			"label = " + TOMLString(label),
			"base_url = " + TOMLString(baseURL),
		}
		if probeURL != "" {
			block = append(block, "probe_url = "+TOMLString(probeURL))
		}
		block = append(block,
			"probe_method = "+TOMLString("GET"),
			"enabled = true",
		)
		return appendBlock(lines, block), nil
	})
}

// RemoveProfile deletes one [profiles.<id>] block.
func RemoveProfile(text, id string) (string, error) {
	return documentEdit(text, func(lines []string) ([]string, error) {
		header := "[profiles." + id + "]"
		bounds := sectionBlocks(lines, header)
		if len(bounds) == 0 {
			return nil, fmt.Errorf("profile %q not found", id)
		}
		return removeLines(lines, bounds[0][0], bounds[0][1]), nil
	})
}
