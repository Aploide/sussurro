package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// compactAssetSyntax keeps contract tests focused on behavior rather than the
// formatter's quote style, line wrapping, or declaration alignment.
func compactAssetSyntax(asset string) string {
	asset = strings.ReplaceAll(asset, "'", `"`)
	return strings.Join(strings.Fields(asset), "")
}

var (
	elementIDPattern = regexp.MustCompile(`id="(workflow-[^"]+)"`)
	getElementByID   = regexp.MustCompile(`getElementById\(["'](workflow-[^"']+)["']\)`)
	quotedIDPattern  = regexp.MustCompile(`["'](workflow-[a-z-]+)["']`)
	settingKey       = regexp.MustCompile(`["'](workflow\.[a-z_.]+)["']`)
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

func TestEditHotkeyBridgeAndAssetsStayConnected(t *testing.T) {
	html := readAsset(t, "index.html")
	js := readAsset(t, "app.js")
	bridge, err := os.ReadFile("settings_bridge.go")
	if err != nil {
		t.Fatalf("reading settings_bridge.go: %v", err)
	}

	for _, required := range []struct {
		name string
		body string
		want string
	}{
		{name: "edit row", body: html, want: `id="hotkey-review-edit-row"`},
		{name: "edit display", body: html, want: `id="hotkey-review-edit-display"`},
		{name: "edit button", body: html, want: `id="hotkey-review-edit-btn"`},
		{name: "edit clear button", body: html, want: `id="hotkey-review-edit-clear-btn"`},
		{name: "JS initial field", body: js, want: "data.editHotkey"},
		{name: "JS save binding", body: js, want: "window.saveEditHotkey"},
		{name: "Go JSON field", body: string(bridge), want: `json:"editHotkey"`},
		{name: "Go save binding", body: string(bridge), want: `Bind("saveEditHotkey"`},
	} {
		t.Run(required.name, func(t *testing.T) {
			if !strings.Contains(required.body, required.want) {
				t.Errorf("missing %q", required.want)
			}
		})
	}
}

func TestEveryHotkeyHasAClearControl(t *testing.T) {
	html := readAsset(t, "index.html")
	js := readAsset(t, "app.js")
	for _, id := range []string{
		"hotkey-clear-btn", "hotkey-toggle-clear-btn", "hotkey-review-edit-clear-btn",
	} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("missing clear control %q", id)
		}
		if !strings.Contains(js, `'`+id+`'`) && !strings.Contains(js, `"`+id+`"`) {
			t.Errorf("clear control %q is not wired in app.js", id)
		}
	}
	if !strings.Contains(js, "save('')") && !strings.Contains(js, `save("")`) {
		t.Error("clear controls do not persist an empty binding")
	}
}

func TestInitialDataIncludesEditHotkey(t *testing.T) {
	cfg := defaultConfig()
	cfg.Hotkey.Edit = "super+9"
	data := buildInitialData(&Manager{cfg: cfg})
	if data.EditHotkey != "super+9" {
		t.Errorf("EditHotkey = %q, want super+9", data.EditHotkey)
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
		"workflow-input-cancel-chord", "appearance-theme",
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

// panelOfSection walks the document tracking the current tab panel, which is
// more reliable than matching nested divs with a regex.
func panelOfSection(html string) map[string]string {
	panels := map[string]string{}
	current := ""
	panelPattern := regexp.MustCompile(`data-tab-panel="([^"]+)"`)
	sectionPattern := regexp.MustCompile(`data-section-id="([^"]+)"`)

	for _, line := range strings.Split(html, "\n") {
		if m := panelPattern.FindStringSubmatch(line); m != nil {
			current = m[1]
		}
		if m := sectionPattern.FindStringSubmatch(line); m != nil {
			panels[m[1]] = current
		}
	}
	return panels
}

func TestEverySectionLivesInATab(t *testing.T) {
	html := readAsset(t, "index.html")

	// A section outside a tab panel is unreachable once tabs hide the others,
	// which is exactly the failure a restructure invites.
	sections := panelOfSection(html)
	if len(sections) == 0 {
		t.Fatal("no sections found in index.html")
	}
	for section, panel := range sections {
		if panel == "" {
			t.Errorf("section %q is not inside any tab panel", section)
		}
	}
}

func TestEveryTabHasAPanel(t *testing.T) {
	html := readAsset(t, "index.html")

	tabs := regexp.MustCompile(`data-tab="([^"]+)"`).FindAllStringSubmatch(html, -1)
	if len(tabs) == 0 {
		t.Fatal("no tabs found in index.html")
	}

	for _, tab := range tabs {
		if !strings.Contains(html, `data-tab-panel="`+tab[1]+`"`) {
			t.Errorf("tab %q has no matching panel", tab[1])
		}
	}
}

func TestEverySectionsPanelIsATab(t *testing.T) {
	html := readAsset(t, "index.html")

	// The converse: a panel holding sections but with no tab to select it
	// hides those settings permanently.
	for section, panel := range panelOfSection(html) {
		if panel != "" && !strings.Contains(html, `data-tab="`+panel+`"`) {
			t.Errorf("section %q is in panel %q, which no tab selects", section, panel)
		}
	}
}

func TestEmbeddedSettingsPageIncludesThemeAssets(t *testing.T) {
	for _, marker := range []string{"<!--SETTINGS_STYLE-->", "{{JS}}"} {
		if strings.Contains(settingsHTML, marker) {
			t.Errorf("assembled settings page still contains marker %q", marker)
		}
	}
	for _, content := range []string{`:root[data-theme="light"]`, "function renderTheme(theme)"} {
		if !strings.Contains(settingsHTML, content) {
			t.Errorf("assembled settings page is missing %q", content)
		}
	}
}

func TestThemeControlOffersEveryConfiguredValue(t *testing.T) {
	html := compactAssetSyntax(readAsset(t, "index.html"))
	js := compactAssetSyntax(readAsset(t, "app.js"))

	for _, theme := range []string{"system", "light", "dark"} {
		if !strings.Contains(html, compactAssetSyntax(`option value="`+theme+`"`)) {
			t.Errorf("Appearance control has no %q option", theme)
		}
		if !strings.Contains(js, `"`+theme+`"`) {
			t.Errorf("app.js does not recognize theme %q", theme)
		}
	}
	for _, contract := range []string{
		"window.saveTheme(chosen)",
		"document.documentElement.dataset.theme = chosen",
		"renderTheme(data.theme)",
	} {
		if !strings.Contains(js, compactAssetSyntax(contract)) {
			t.Errorf("app.js is missing theme contract %q", contract)
		}
	}
	statusContract := `id="appearance-status" role="status" aria-live="polite"`
	if !strings.Contains(html, compactAssetSyntax(statusContract)) {
		t.Error("Appearance save result has no accessible live status")
	}
}

func TestThemePalettesCoverOverridesAndSystemPreference(t *testing.T) {
	css := compactAssetSyntax(readAsset(t, "style.css"))

	for _, selector := range []string{
		`:root[data-theme="dark"]`,
		`:root[data-theme="light"]`,
		`@media (prefers-color-scheme: light)`,
		`:root[data-theme="system"]`,
	} {
		if !strings.Contains(css, compactAssetSyntax(selector)) {
			t.Errorf("style.css has no %s palette selector", selector)
		}
	}

	// Explicit light and system-light must stay the same palette. Every light
	// declaration appears once in each block.
	for _, declaration := range []string{
		`--bg: #f5f5f7`,
		`--surface: #ffffff`,
		`--surface2: #ededf0`,
		`--border: #d2d2d7`,
		`--text: #1d1d1f`,
		`--muted: #5f6368`,
		`--accent: #16833b`,
		`--red: #c62828`,
		`--blue: #0066cc`,
		`--modal-scrim: rgba(0, 0, 0, 0.45)`,
		`fill="%235f6368"`,
	} {
		if count := strings.Count(css, compactAssetSyntax(declaration)); count != 2 {
			t.Errorf("light palette declaration %q appears %d times, want explicit and system", declaration, count)
		}
	}

	// Pin the dark palette so appearance changes remain deliberate and reviewed.
	for _, declaration := range []string{
		`--bg: #111113`,
		`--surface: #1a1a1c`,
		`--surface2: #222224`,
		`--border: #2e2e30`,
		`--text: #e8e8ea`,
		`--muted: #a0a0a8`,
		`--accent: #30d158`,
		`--red: #ff453a`,
		`--blue: #0a84ff`,
		`--subtle-hover: rgba(255, 255, 255, 0.02)`,
		`--subtle-selected: rgba(255, 255, 255, 0.04)`,
		`--control-hover: #2e2e30`,
		`--control-hover-border: #444444`,
		`--toggle-knob: #ffffff`,
		`--modal-scrim: rgba(0, 0, 0, 0.6)`,
		`--preview-bg: rgba(255, 255, 255, 0.05)`,
		`--info-bg: rgba(10, 132, 255, 0.1)`,
		`--info-border: rgba(10, 132, 255, 0.25)`,
		`fill="%23a0a0a8"`,
	} {
		if !strings.Contains(css, compactAssetSyntax(declaration)) {
			t.Errorf("dark palette no longer contains %q", declaration)
		}
	}
}

func TestColourLiteralsExistOnlyInPaletteTokens(t *testing.T) {
	css := readAsset(t, "style.css")
	html := readAsset(t, "index.html")
	literal := regexp.MustCompile(`(?i)(#[0-9a-f]{3,8}\b|rgba?\s*\(|%23[0-9a-f]{3,8}\b)`)

	for number, line := range strings.Split(css, "\n") {
		if literal.MatchString(line) && !strings.Contains(line, "--") {
			t.Errorf("style.css:%d has an untokenised colour literal: %s", number+1, line)
		}
	}
	if match := literal.FindString(html); match != "" {
		t.Errorf("index.html has untokenised colour literal %q", match)
	}
}

func TestExactlyOneTabStartsSelected(t *testing.T) {
	html := readAsset(t, "index.html")

	if selected := strings.Count(html, `aria-selected="true"`); selected != 1 {
		t.Errorf("%d tabs start selected, want exactly 1", selected)
	}

	// Count panels that are not marked hidden on their own opening tag.
	panelLine := regexp.MustCompile(`<div class="tab-panel" data-tab-panel="[^"]+"( hidden)?>`)
	visible := 0
	for _, m := range panelLine.FindAllStringSubmatch(html, -1) {
		if m[1] == "" {
			visible++
		}
	}
	if visible != 1 {
		t.Errorf("%d panels start visible, want exactly 1", visible)
	}
}

// hexColor parses a six-digit sRGB hex value into the normalized form the
// overlay palette tests already work in, so both surfaces share one contrast
// implementation.
func hexColor(t *testing.T, hex string) overlayColor {
	t.Helper()
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		t.Fatalf("colour %q is not a six-digit hex value", hex)
	}
	channel := func(offset int) float64 {
		value, err := strconv.ParseUint(hex[offset:offset+2], 16, 8)
		if err != nil {
			t.Fatalf("parsing colour %q: %v", hex, err)
		}
		return float64(value) / 255
	}
	return overlayColor{R: channel(0), G: channel(2), B: channel(4), A: 1}
}

// paletteValue reads one token from a palette block of the stylesheet.
func paletteValue(t *testing.T, css, selector, token string) string {
	t.Helper()
	start := strings.Index(css, selector)
	if start < 0 {
		t.Fatalf("stylesheet has no %q block", selector)
	}
	block := css[start:]
	if end := strings.Index(block, "}"); end >= 0 {
		block = block[:end]
	}
	match := regexp.MustCompile(token + `:\s*(#[0-9a-fA-F]{6})`).FindStringSubmatch(block)
	if match == nil {
		t.Fatalf("%q block does not define %s as a hex colour", selector, token)
	}
	return match[1]
}

// Secondary text must stay legible against every surface it can sit on. A
// pinned hex only catches drift; this catches a palette edit that looks fine
// on --bg but fails on the raised surfaces, which is how --muted originally
// shipped below the threshold on all three.
func TestMutedTextMeetsContrastMinimums(t *testing.T) {
	css := readAsset(t, "style.css")

	// WCAG 2.1 AA for normal text is 4.5:1. Every var(--muted) usage
	// renders at 11-15px, so the 3:1 large-text allowance never applies.
	// Human testing found 5.34:1 too dim in the dark palette, so its practical
	// floor is 6:1 while light retains the standard minimum.
	for _, palette := range []struct {
		name            string
		selector        string
		minimumContrast float64
	}{
		{"dark", `:root[data-theme="dark"]`, 6.0},
		{"light", `:root[data-theme="light"]`, 4.5},
	} {
		muted := hexColor(t, paletteValue(t, css, palette.selector, "--muted"))
		for _, surface := range []string{"--bg", "--surface", "--surface2"} {
			token := paletteValue(t, css, palette.selector, surface)
			if ratio := contrastRatio(muted, hexColor(t, token)); ratio < palette.minimumContrast {
				t.Errorf("%s palette: --muted on %s %s is %.2f:1, want at least %.1f:1",
					palette.name, surface, token, ratio, palette.minimumContrast)
			}
		}
	}
}

// The page refuses to measure a viewport narrower than the layout supports,
// because a transient sliver yields a height that is wrong twice over: bogus
// in itself, and inflated by the wrapping the narrow width causes. Sizing from
// one made the window jump tall and then visibly shrink. The JS cannot import
// the Go constant, so pin them together here rather than letting them drift.
func TestPageMinimumWidthMatchesTheLayoutRequirement(t *testing.T) {
	js := readAsset(t, "app.js")
	want := fmt.Sprintf("const MIN_SETTINGS_WIDTH = %d;", settingsContentWidth)
	if !strings.Contains(js, want) {
		t.Errorf("app.js does not declare %q; it must match settingsContentWidth", want)
	}
	if !strings.Contains(js, "if (width < MIN_SETTINGS_WIDTH) {") {
		t.Error("app.js does not reject a viewport narrower than MIN_SETTINGS_WIDTH")
	}
	// Skipping alone is not enough: this is the only measurement the page takes
	// on open, so a narrow reading must reschedule or the window stays stuck at
	// its startup minimum. That regression shipped once already.
	if !strings.Contains(js, "narrowLayoutRetries") {
		t.Error("app.js does not retry after a too-narrow measurement")
	}
}
