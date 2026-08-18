package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readDoc returns a documentation file from the repository root.
func readDoc(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(body)
}

func TestEveryWorkflowEnvironmentVariableIsDocumented(t *testing.T) {
	docs := readDoc(t, filepath.Join("docs", "configuration.md"))

	for key, env := range workflowEnvKeys {
		if !strings.Contains(docs, env) {
			t.Errorf("environment variable %s (%s) is not documented", env, key)
		}
		if !strings.Contains(docs, key) {
			t.Errorf("configuration key %s is not documented", key)
		}
	}
}

// documentedEnv matches the SUSSURRO_WORKFLOW_ variables named in the docs.
var documentedEnv = regexp.MustCompile("`(SUSSURRO_WORKFLOW_[A-Z_]+)`")

func TestDocumentedEnvironmentVariablesExist(t *testing.T) {
	docs := readDoc(t, filepath.Join("docs", "configuration.md"))

	bound := make(map[string]bool, len(workflowEnvKeys))
	for _, env := range workflowEnvKeys {
		bound[env] = true
	}

	// A documented variable the loader never binds silently does nothing.
	for _, match := range documentedEnv.FindAllStringSubmatch(docs, -1) {
		if !bound[match[1]] {
			t.Errorf("docs promise %s, but no configuration key binds it", match[1])
		}
	}
}

func TestEveryWorkflowEnumValueIsDocumented(t *testing.T) {
	docs := readDoc(t, filepath.Join("docs", "configuration.md"))

	var values []string
	for _, mode := range interactionModes {
		values = append(values, string(mode))
	}
	for _, backend := range inputBackends {
		values = append(values, string(backend))
	}
	for _, backend := range deliveryBackends {
		values = append(values, string(backend))
	}

	// An accepted value that appears nowhere in the docs is undiscoverable.
	for _, value := range values {
		if !strings.Contains(docs, "`"+value+"`") {
			t.Errorf("accepted value %q is not documented", value)
		}
	}
}

func TestShippedDefaultsMatchTheDocumentedDefaults(t *testing.T) {
	shipped := readShippedDefaults(t)

	// The shipped file is what users start from; it must show the real
	// defaults, not an aspirational set.
	for _, want := range []string{
		`mode: "` + string(DefaultInteractionMode) + `"`,
		`interval: "` + DefaultStreamingInterval + `"`,
		`backend: "` + string(DefaultInputBackend) + `"`,
	} {
		if !strings.Contains(shipped, want) {
			t.Errorf("configs/default.yaml does not contain %q", want)
		}
	}
	if !strings.Contains(shipped, "enabled: false") {
		t.Error("configs/default.yaml does not ship streaming disabled")
	}
}
