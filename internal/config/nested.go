package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// SetNestedValue writes a dotted key such as "workflow.input.backend" into a
// YAML document, creating any missing parent maps.
//
// The existing Save* helpers rewrite a single top-level key by matching its
// line. That cannot address a nested key: several sections contain a "backend"
// line, and a new workflow section may not exist at all. This walks the
// document by indentation instead, so the right key is found and untouched
// content, comments included, is preserved.
func SetNestedValue(content, dottedKey, value string) (string, error) {
	path := strings.Split(dottedKey, ".")
	if len(path) == 0 || dottedKey == "" {
		return "", fmt.Errorf("empty key")
	}

	lines := strings.Split(content, "\n")
	updated, err := setInLines(lines, path, value)
	if err != nil {
		return "", err
	}
	return strings.Join(updated, "\n"), nil
}

// RemoveNestedValue deletes a dotted key, and any block it introduces, from a
// YAML document. A key that is absent leaves the document unchanged, so a
// superseded setting can be dropped without first checking for it.
func RemoveNestedValue(content, dottedKey string) (string, error) {
	path := strings.Split(dottedKey, ".")
	if len(path) == 0 || dottedKey == "" {
		return "", fmt.Errorf("empty key")
	}
	lines := strings.Split(content, "\n")
	return strings.Join(removeInLines(lines, path), "\n"), nil
}

// RemoveWorkflowValue deletes one setting from the file that supplied cfg,
// with the same validation and atomic write as SaveWorkflowValue.
func RemoveWorkflowValue(cfg *Config, dottedKey string) error {
	configSaveMu.Lock()
	defer configSaveMu.Unlock()
	configFile, err := configPath(cfg)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	updated, err := RemoveNestedValue(string(data), dottedKey)
	if err != nil {
		return err
	}
	if updated == string(data) {
		return nil
	}
	var document map[string]any
	if err := yaml.Unmarshal([]byte(updated), &document); err != nil {
		return fmt.Errorf("%s cannot be removed without changing this config's YAML structure: %w", dottedKey, err)
	}
	if err := writeConfigAtomically(configFile, []byte(updated)); err != nil {
		return fmt.Errorf("write config atomically: %w", err)
	}
	return nil
}

// SaveWorkflowValue persists one setting to the file that supplied cfg.
//
// The line-based edit is checked to still parse as a YAML mapping before it
// is written, and the write replaces the file atomically, so a malformed
// edit or an interrupted write never leaves the user with a config that no
// longer loads. This mirrors SaveTheme.
func SaveWorkflowValue(cfg *Config, dottedKey, value string) error {
	configSaveMu.Lock()
	defer configSaveMu.Unlock()
	configFile, err := configPath(cfg)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	updated, err := SetNestedValue(string(data), dottedKey, value)
	if err != nil {
		return err
	}
	var document map[string]any
	if err := yaml.Unmarshal([]byte(updated), &document); err != nil {
		return fmt.Errorf("%s cannot be saved without changing this config's YAML structure: %w", dottedKey, err)
	}
	if err := writeConfigAtomically(configFile, []byte(updated)); err != nil {
		return fmt.Errorf("write config atomically: %w", err)
	}
	return nil
}

func userConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find home directory: %w", err)
	}
	return filepath.Join(homeDir, ".sussurro", "config.yaml"), nil
}

// setInLines writes path into lines, returning the updated document.
func setInLines(lines []string, path []string, value string) ([]string, error) {
	// start and end bound the block the current key must live in; indent is
	// the indentation its siblings use.
	start, end, indent := 0, len(lines), 0

	for depth, key := range path {
		last := depth == len(path)-1
		index := findKeyLine(lines, key, start, end, indent)

		if index == -1 {
			// The key is absent: append it, with any remaining parents.
			return insertKeyPath(lines, path[depth:], value, end, indent), nil
		}

		if last {
			lines[index] = fmt.Sprintf("%s%s: %s", strings.Repeat(" ", indent), key, value)
			return lines, nil
		}

		// Descend into the block this key introduces.
		start = index + 1
		end = blockEnd(lines, start, indent)
		indent = childIndent(lines, start, end, indent)
	}

	return lines, nil
}

// removeInLines drops path from lines, together with any nested block the key
// introduces, returning the document unchanged when the key is absent.
func removeInLines(lines []string, path []string) []string {
	start, end, indent := 0, len(lines), 0

	for depth, key := range path {
		index := findKeyLine(lines, key, start, end, indent)
		if index == -1 {
			return lines
		}
		if depth == len(path)-1 {
			last := blockEnd(lines, index+1, indent)
			// blockEnd skips blank lines; leave them, so the spacing between
			// sections (and the file's trailing newline) survives.
			for last > index+1 && strings.TrimSpace(lines[last-1]) == "" {
				last--
			}
			return append(append([]string{}, lines[:index]...), lines[last:]...)
		}
		start = index + 1
		end = blockEnd(lines, start, indent)
		indent = childIndent(lines, start, end, indent)
	}
	return lines
}

// findKeyLine returns the index of key at exactly the given indentation within
// [start, end), or -1. The caller bounds the range to the enclosing block, and
// the indentation check rejects a deeper key of the same name inside a nested
// block of that same range.
func findKeyLine(lines []string, key string, start, end, indent int) int {
	for i := start; i < end && i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if lineIndent(line) != indent {
			continue
		}
		if trimmed == key+":" || strings.HasPrefix(trimmed, key+": ") {
			return i
		}
	}
	return -1
}

// blockEnd returns the index just past the block that starts at start and is
// indented deeper than parentIndent.
func blockEnd(lines []string, start, parentIndent int) int {
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if lineIndent(lines[i]) <= parentIndent {
			return i
		}
	}
	return len(lines)
}

// childIndent reports the indentation used inside a block, falling back to two
// spaces deeper than the parent when the block is empty.
func childIndent(lines []string, start, end, parentIndent int) int {
	for i := start; i < end && i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return lineIndent(lines[i])
	}
	return parentIndent + 2
}

// insertKeyPath appends the remaining path as nested keys ending in value.
func insertKeyPath(lines, path []string, value string, at, indent int) []string {
	var block []string
	for depth, key := range path {
		pad := strings.Repeat(" ", indent+depth*2)
		if depth == len(path)-1 {
			block = append(block, fmt.Sprintf("%s%s: %s", pad, key, value))
			continue
		}
		block = append(block, fmt.Sprintf("%s%s:", pad, key))
	}

	// Insert before any trailing blank lines so the file keeps its shape.
	for at > 0 && at <= len(lines) && strings.TrimSpace(lines[at-1]) == "" {
		at--
	}

	updated := make([]string, 0, len(lines)+len(block))
	updated = append(updated, lines[:at]...)
	updated = append(updated, block...)
	updated = append(updated, lines[at:]...)
	return updated
}

// lineIndent counts the leading spaces of a line.
func lineIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// YAMLBool renders a boolean as a YAML scalar.
func YAMLBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// YAMLString renders a string as a quoted YAML scalar.
func YAMLString(v string) string {
	return YAMLPathLiteral(v)
}
