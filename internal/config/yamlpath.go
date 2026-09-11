package config

import (
	"os"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Model paths are the only config values that can contain a Windows path, and
// they used to be written as double-quoted YAML scalars. Inside a double-quoted
// scalar a backslash opens an escape sequence, so the "\U" of C:\Users is read
// as YAML's 32-bit unicode escape and demands eight hex digits — every config
// generated on Windows was rejected with
//
//	While parsing config: yaml: line 18: did not find expected hexdecimal number
//
// before the app could start, which also meant it could never repair itself.
// Single-quoted scalars have no escape sequences at all; only a literal
// apostrophe is special, and it is escaped by doubling it.

// YAMLPathLiteral renders p as a single-quoted YAML scalar, safe for paths that
// contain backslashes.
func YAMLPathLiteral(p string) string {
	return "'" + escapeApostrophes(p) + "'"
}

// ReplacePathInYAML swaps oldPath for newPath in raw config text. It covers
// both the verbatim form written by older versions (and by every non-Windows
// install, where the path needs no escaping) and the single-quoted form with
// doubled apostrophes.
func ReplacePathInYAML(content, oldPath, newPath string) string {
	if esc := escapeApostrophes(oldPath); esc != oldPath {
		content = strings.ReplaceAll(content, esc, escapeApostrophes(newPath))
	}
	return strings.ReplaceAll(content, oldPath, newPath)
}

func escapeApostrophes(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// A generated config puts each path on its own line, so the value can be
// re-quoted textually without reformatting the rest of the file. The trailing
// group keeps any comment and the CR of a CRLF file intact.
var doubleQuotedPathLine = regexp.MustCompile(`^(\s*path:[ \t]*)"([^"]*)"([ \t]*(?:#[^\r\n]*)?\r?)$`)

// repairConfigPaths re-quotes double-quoted path values that are not valid
// YAML, and reports whether the file was rewritten. Lines that already parse
// are left untouched, so a config someone fixed by hand (with "C:\\Users\\…")
// keeps working and repeat runs are no-ops.
func repairConfigPaths(configFile string) (bool, error) {
	if configFile == "" {
		return false, nil
	}

	content, err := os.ReadFile(configFile)
	if err != nil {
		return false, err
	}

	lines := strings.Split(string(content), "\n")
	changed := false
	for i, line := range lines {
		m := doubleQuotedPathLine.FindStringSubmatch(line)
		if m == nil || parsesAsDoubleQuoted(m[2]) {
			continue
		}
		lines[i] = m[1] + YAMLPathLiteral(m[2]) + m[3]
		changed = true
	}
	if !changed {
		return false, nil
	}

	return true, os.WriteFile(configFile, []byte(strings.Join(lines, "\n")), 0644)
}

// parsesAsDoubleQuoted reports whether raw — the text between the quotes —
// survives being read back as a double-quoted scalar.
func parsesAsDoubleQuoted(raw string) bool {
	var s string
	return yaml.Unmarshal([]byte(`"`+raw+`"`), &s) == nil
}
