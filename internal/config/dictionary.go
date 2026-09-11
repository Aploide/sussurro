package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

// NormalizeDictionary trims personal vocabulary and rejects entries whose
// meaning would be ambiguous to the decoder and cleanup stages.
func NormalizeDictionary(terms []string) ([]string, error) {
	normalized := make([]string, len(terms))
	for i, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			return nil, fmt.Errorf("dictionary term %d is blank; enter a term or remove it", i+1)
		}

		// EqualFold implements Unicode case folding. Lowercasing map keys is not
		// equivalent (for example, the Greek sigma forms compare equal but lower
		// to different runes).
		for _, previous := range normalized[:i] {
			if strings.EqualFold(term, previous) {
				return nil, fmt.Errorf("dictionary term %q duplicates %q; remove one or change its spelling", term, previous)
			}
		}
		normalized[i] = term
	}
	return normalized, nil
}

// SaveDictionary replaces app.dictionary in the file that supplied cfg. The
// JSON representation is also a valid YAML flow sequence, which keeps this
// setting on one line and avoids disturbing unrelated comments and formatting.
func SaveDictionary(cfg *Config, terms []string) ([]string, error) {
	normalized, err := NormalizeDictionary(terms)
	if err != nil {
		return nil, err
	}
	value, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("encode dictionary: %w", err)
	}

	configSaveMu.Lock()
	defer configSaveMu.Unlock()

	configFile, err := configPath(cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve user config path: %w", err)
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}

	updated, err := setDictionaryValue(string(data), string(value))
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(updated), &document); err != nil {
		return nil, fmt.Errorf("validate updated config: %w", err)
	}
	if err := writeConfigAtomically(configFile, []byte(updated)); err != nil {
		return nil, fmt.Errorf("write dictionary: %w", err)
	}
	return normalized, nil
}

// setDictionaryValue handles both the block sequence written by hand and the
// flow sequence SaveDictionary writes. SetNestedValue can replace scalar keys,
// but replacing only "dictionary:" would leave its old "- term" children in
// place and produce invalid YAML.
func setDictionaryValue(content, value string) (string, error) {
	lines := strings.Split(content, "\n")
	app := findKeyLine(lines, "app", 0, len(lines), 0)
	if app == -1 {
		return SetNestedValue(content, "app.dictionary", value)
	}

	appEnd := blockEnd(lines, app+1, 0)
	indent := childIndent(lines, app+1, appEnd, 0)
	dictionary := findKeyLine(lines, "dictionary", app+1, appEnd, indent)
	if dictionary == -1 {
		return SetNestedValue(content, "app.dictionary", value)
	}

	end := dictionarySequenceEnd(lines, dictionary+1, appEnd, indent)
	modifiers := dictionaryValueModifiers(lines[dictionary])
	if modifiers != "" {
		modifiers += " "
	}
	comment := dictionaryInlineComment(lines[dictionary])
	lines[dictionary] = fmt.Sprintf(
		"%sdictionary: %s%s%s",
		strings.Repeat(" ", indent), modifiers, value, comment,
	)
	lines = append(lines[:dictionary+1], lines[end:]...)
	return strings.Join(lines, "\n"), nil
}

// dictionarySequenceEnd finds the end of a block sequence, including YAML's
// valid indentationless form where dashes align with the dictionary key. It
// leaves trailing blank lines and comments in place because they may describe
// the following app setting rather than the entries being replaced.
func dictionarySequenceEnd(lines []string, start, appEnd, indent int) int {
	end := start
	foundEntry := false
	for end < appEnd {
		trimmed := strings.TrimSpace(lines[end])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			end++
			continue
		}

		lineIndentation := lineIndent(lines[end])
		if lineIndentation < indent ||
			(lineIndentation == indent && !strings.HasPrefix(trimmed, "-")) {
			break
		}
		foundEntry = true
		end++
	}
	if !foundEntry {
		return start
	}
	for end > start {
		trimmed := strings.TrimSpace(lines[end-1])
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			break
		}
		end--
	}
	return end
}

// dictionaryValueModifiers preserves an anchor or explicit YAML tag attached
// to the dictionary value. An alias elsewhere in the document remains valid
// after the sequence itself is replaced.
func dictionaryValueModifiers(line string) string {
	_, remainder, found := strings.Cut(strings.TrimSpace(line), ":")
	if !found {
		return ""
	}
	var modifiers []string
	for _, field := range strings.Fields(remainder) {
		if !strings.HasPrefix(field, "&") && !strings.HasPrefix(field, "!") {
			break
		}
		modifiers = append(modifiers, field)
	}
	return strings.Join(modifiers, " ")
}

// dictionaryInlineComment returns a YAML comment on the key line while
// ignoring hash characters inside quoted flow-sequence entries.
func dictionaryInlineComment(line string) string {
	_, remainder, found := strings.Cut(line, ":")
	if !found {
		return ""
	}
	inSingle, inDouble, escaped := false, false, false
	for i := 0; i < len(remainder); i++ {
		character := remainder[i]
		if inDouble {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
			} else if character == '"' {
				inDouble = false
			}
			continue
		}
		if inSingle {
			if character == '\'' {
				if i+1 < len(remainder) && remainder[i+1] == '\'' {
					i++
				} else {
					inSingle = false
				}
			}
			continue
		}
		switch character {
		case '"':
			inDouble = true
		case '\'':
			inSingle = true
		case '#':
			if i == 0 || remainder[i-1] == ' ' || remainder[i-1] == '\t' {
				return " " + strings.TrimSpace(remainder[i:])
			}
		}
	}
	return ""
}
