package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aploide/sussurro/internal/config"
)

func TestDictionaryBridgePersistsAndAppliesNormalizedTerms(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".sussurro", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("app:\n  name: Sussurro\n  dictionary: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := &Manager{cfg: &config.Config{}}
	var applied []string
	mgr.SetDictionaryCallback(func(terms []string) {
		applied = append([]string(nil), terms...)
	})

	if got := saveDictionary(mgr, `["  Sussurro  ","whisper.cpp"]`); got != "ok" {
		t.Fatalf("saveDictionary() = %q, want ok", got)
	}
	want := []string{"Sussurro", "whisper.cpp"}
	if !reflect.DeepEqual(mgr.cfg.App.Dictionary, want) {
		t.Errorf("in-memory dictionary = %#v, want %#v", mgr.cfg.App.Dictionary, want)
	}
	if !reflect.DeepEqual(applied, want) {
		t.Errorf("applied dictionary = %#v, want %#v", applied, want)
	}

	written, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `dictionary: ["Sussurro","whisper.cpp"]`) {
		t.Errorf("dictionary was not persisted:\n%s", written)
	}
}

func TestDictionaryBridgeRejectsInvalidTermsWithoutApplying(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".sussurro", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	const original = "app:\n  dictionary: [\"Original\"]\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := &Manager{cfg: &config.Config{App: config.AppConfig{Dictionary: []string{"Original"}}}}
	applied := false
	mgr.SetDictionaryCallback(func([]string) { applied = true })

	got := saveDictionary(mgr, `["Sussurro","sussurro"]`)
	if !strings.HasPrefix(got, "error:") || !strings.Contains(got, "duplicates") {
		t.Fatalf("saveDictionary() = %q, want duplicate error", got)
	}
	if applied {
		t.Error("invalid dictionary was applied live")
	}
	if !reflect.DeepEqual(mgr.cfg.App.Dictionary, []string{"Original"}) {
		t.Errorf("invalid save changed in-memory dictionary to %#v", mgr.cfg.App.Dictionary)
	}
	written, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != original {
		t.Errorf("invalid save changed config:\n%s", written)
	}
}

func TestDictionaryInitialDataOwnsItsSlice(t *testing.T) {
	cfg := defaultConfig()
	cfg.App.Dictionary = []string{"Sussurro"}
	data := buildInitialData(&Manager{cfg: cfg})
	data.Dictionary[0] = "changed in UI data"
	if cfg.App.Dictionary[0] != "Sussurro" {
		t.Errorf("initial data aliases config dictionary: %#v", cfg.App.Dictionary)
	}
}

func TestSettingsTemplateEmbedsDictionaryAssets(t *testing.T) {
	for _, placeholder := range []string{"{{CSS}}", "{{JS}}"} {
		if strings.Contains(settingsHTML, placeholder) {
			t.Errorf("assembled settings page still contains placeholder %q", placeholder)
		}
	}
	for _, want := range []string{".dictionary-entry", "function renderDictionary"} {
		if !strings.Contains(settingsHTML, want) {
			t.Errorf("assembled settings page does not contain %q", want)
		}
	}
}

func TestDictionaryEditorBridgeAndAssetsStayConnected(t *testing.T) {
	html := readAsset(t, "index.html")
	js := readAsset(t, "app.js")
	css := readAsset(t, "style.css")
	bridge, err := os.ReadFile("settings_bridge.go")
	if err != nil {
		t.Fatal(err)
	}

	if count := strings.Count(js, "function renderDictionary("); count != 1 {
		t.Fatalf("app.js defines renderDictionary %d times, want exactly once", count)
	}

	for _, required := range []struct {
		name string
		body string
		want string
	}{
		{name: "section", body: html, want: `data-section-id="dictionary"`},
		{name: "list", body: html, want: `id="dictionary-list"`},
		{name: "add button", body: html, want: `id="dictionary-add-btn"`},
		{name: "save button", body: html, want: `id="dictionary-save-btn"`},
		{name: "status", body: html, want: `id="dictionary-status"`},
		{name: "render", body: js, want: "renderDictionary(data.dictionary"},
		{name: "persistent draft", body: js, want: "let dictionaryDraft = null"},
		{name: "save state", body: js, want: "let dictionarySaving = false"},
		{name: "stale save guard", body: js, want: "generation !== dictionarySaveGeneration"},
		{name: "refresh disables controls", body: js, want: "setControlsDisabled(dictionarySaving)"},
		{name: "entry class", body: js, want: `row.className = "dictionary-entry"`},
		{name: "entry style", body: css, want: ".dictionary-entry"},
		{name: "input style", body: css, want: ".dictionary-input"},
		{name: "save call", body: js, want: "window.saveDictionary(JSON.stringify(normalized))"},
		{name: "Go JSON field", body: string(bridge), want: `json:"dictionary"`},
		{name: "Go save binding", body: string(bridge), want: `Bind("saveDictionary"`},
	} {
		t.Run(required.name, func(t *testing.T) {
			if !strings.Contains(required.body, required.want) {
				t.Errorf("missing %q", required.want)
			}
		})
	}
}
