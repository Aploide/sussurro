package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aploide/sussurro/internal/config"
)

// readAsset returns one of the settings UI assets.
func readAsset(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("assets", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(body)
}

var (
	elementIDPattern = regexp.MustCompile(`id="(workflow-[^"]+)"`)
	getElementByID   = regexp.MustCompile(`getElementById\('(workflow-[^']+)'\)`)
	quotedIDPattern  = regexp.MustCompile(`'(workflow-[a-z-]+)'`)
	settingKey       = regexp.MustCompile(`'(workflow\.[a-z_.]+)'`)
	goSettingKey     = regexp.MustCompile(`case "(workflow\.[a-z_.]+)":`)
)

func TestSettingsUIReferencesOnlyExistingElements(t *testing.T) {
	html := readAsset(t, "index.html")
	js := readAsset(t, "app.js")

	declared := make(map[string]bool)
	for _, match := range elementIDPattern.FindAllStringSubmatch(html, -1) {
		declared[match[1]] = true
	}

	// Both direct lookups and ids passed around as strings must resolve, or
	// the control silently does nothing at runtime.
	for _, pattern := range []*regexp.Regexp{getElementByID, quotedIDPattern} {
		for _, match := range pattern.FindAllStringSubmatch(js, -1) {
			if !declared[match[1]] {
				t.Errorf("app.js references element %q that index.html does not define", match[1])
			}
		}
	}
}

func TestEverySavedSettingIsHandledInGo(t *testing.T) {
	js := readAsset(t, "app.js")

	handled := make(map[string]bool)
	source, err := os.ReadFile("settings_workflow.go")
	if err != nil {
		t.Fatalf("reading settings_workflow.go: %v", err)
	}
	for _, match := range goSettingKey.FindAllStringSubmatch(string(source), -1) {
		handled[match[1]] = true
	}

	saved := make(map[string]bool)
	for _, match := range settingKey.FindAllStringSubmatch(js, -1) {
		saved[match[1]] = true
		if !handled[match[1]] {
			t.Errorf("app.js saves %q, which applyWorkflowField rejects", match[1])
		}
	}

	// A setting Go accepts but no control reaches is unreachable to the user.
	for key := range handled {
		if !saved[key] {
			t.Errorf("applyWorkflowField handles %q, but no control in app.js sets it", key)
		}
	}
}

func TestWorkflowSettingsSerializeForTheUI(t *testing.T) {
	cfg := defaultConfig()
	cfg.Workflow.Mode = config.ModeReview

	encoded, err := json.Marshal(buildWorkflowSettings(cfg, probeFor("linux")))
	if err != nil {
		t.Fatalf("marshalling settings: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshalling settings: %v", err)
	}

	// The JS reads these by name; a rename here would silently blank a control.
	for _, field := range []string{
		"mode", "modes", "streamingEnabled", "streamingInterval",
		"inputBackend", "inputBackends", "inputDevice", "inputChord",
		"inputCancelChord", "deliveryBackend", "deliveryBackends",
	} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("settings JSON has no %q field", field)
		}
	}
}

func TestEveryControlHasAnAccessibleLabel(t *testing.T) {
	html := readAsset(t, "index.html")

	// Each select and text input must be addressable by a label, or the
	// control is unusable with a screen reader.
	for _, id := range []string{
		"workflow-mode", "workflow-streaming-interval", "workflow-delivery-backend",
		"workflow-input-backend", "workflow-input-device", "workflow-input-chord",
		"workflow-input-cancel-chord",
	} {
		if !strings.Contains(html, `for="`+id+`"`) {
			t.Errorf("no label is associated with the control %q", id)
		}
	}
}

func TestStyleSheetDefinesTheControlClasses(t *testing.T) {
	css := readAsset(t, "style.css")
	html := readAsset(t, "index.html")

	for _, class := range []string{"setting-select", "setting-input", "setting-note"} {
		if !strings.Contains(html, `class="`+class+`"`) &&
			!strings.Contains(html, class+`"`) {
			t.Errorf("no element uses the class %q", class)
		}
		if !strings.Contains(css, "."+class) {
			t.Errorf("style.css does not define .%s, so the control is unstyled", class)
		}
	}
}
