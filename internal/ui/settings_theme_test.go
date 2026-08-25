package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aploide/sussurro/internal/config"
	"github.com/spf13/viper"
)

const themeTestConfig = `app:
  name: "Sussurro"
models:
  asr:
    path: "models/asr.bin"
  llm:
    path: "models/llm.gguf"
appearance:
  theme: "system"
`

func loadThemeManager(t *testing.T) (*Manager, string) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)

	path := filepath.Join(t.TempDir(), "custom.yaml")
	if err := os.WriteFile(path, []byte(themeTestConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	t.Cleanup(func() { mgr.SetThemeCallback(nil) })
	return mgr, path
}

func TestInitialDataIncludesTheme(t *testing.T) {
	mgr, _ := loadThemeManager(t)
	mgr.cfg.Appearance.Theme = config.ThemeDark

	if got := buildInitialData(mgr).Theme; got != config.ThemeDark {
		t.Errorf("initial theme = %q, want %q", got, config.ThemeDark)
	}
}

func TestSaveThemePersistsUpdatesAndCallsLiveHook(t *testing.T) {
	mgr, path := loadThemeManager(t)
	var applied []config.Theme
	mgr.SetThemeCallback(func(theme config.Theme) {
		applied = append(applied, theme)
	})

	if got := saveTheme(mgr, "light"); got != "ok" {
		t.Fatalf("saveTheme() = %q, want ok", got)
	}
	if mgr.cfg.Appearance.Theme != config.ThemeLight {
		t.Errorf("live config theme = %q, want light", mgr.cfg.Appearance.Theme)
	}
	if len(applied) != 1 || applied[0] != config.ThemeLight {
		t.Errorf("live callback themes = %v, want [light]", applied)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `theme: 'light'`) {
		t.Errorf("saved config has no light theme:\n%s", written)
	}
}

func TestSaveThemeRejectsInvalidValueWithoutChangingLiveState(t *testing.T) {
	mgr, path := loadThemeManager(t)
	called := false
	mgr.SetThemeCallback(func(config.Theme) { called = true })
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	result := saveTheme(mgr, "sepia")
	if !strings.HasPrefix(result, "error:") {
		t.Fatalf("saveTheme() = %q, want error", result)
	}
	if mgr.cfg.Appearance.Theme != config.ThemeSystem {
		t.Errorf("live config theme = %q after rejection, want system", mgr.cfg.Appearance.Theme)
	}
	if called {
		t.Error("live callback ran for a rejected theme")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("invalid theme changed config:\n%s", after)
	}
}

func TestThemeCallbacksAreScopedToTheirManager(t *testing.T) {
	first, _ := loadThemeManager(t)
	second, _ := loadThemeManager(t)
	firstCalls := 0
	secondCalls := 0
	first.SetThemeCallback(func(config.Theme) { firstCalls++ })
	second.SetThemeCallback(func(config.Theme) { secondCalls++ })

	first.applyTheme(config.ThemeDark)
	if firstCalls != 1 || secondCalls != 0 {
		t.Errorf("callback counts = %d/%d, want 1/0", firstCalls, secondCalls)
	}
}
