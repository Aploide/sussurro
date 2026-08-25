package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func loadAppearanceConfigPath(t *testing.T, path string) *Config {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	return cfg
}

func TestThemeDefaultsToSystem(t *testing.T) {
	cfg, err := loadTestConfig(t, legacyConfig)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Appearance.Theme != ThemeSystem {
		t.Errorf("Appearance.Theme = %q, want %q", cfg.Appearance.Theme, ThemeSystem)
	}
}

func TestEveryThemeValueLoads(t *testing.T) {
	for _, theme := range []Theme{ThemeSystem, ThemeLight, ThemeDark} {
		t.Run(string(theme), func(t *testing.T) {
			cfg, err := loadTestConfig(t, legacyConfig+"appearance:\n  theme: \""+string(theme)+"\"\n")
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if cfg.Appearance.Theme != theme {
				t.Errorf("Appearance.Theme = %q, want %q", cfg.Appearance.Theme, theme)
			}
		})
	}
}

func TestInvalidThemeIsRejected(t *testing.T) {
	_, err := loadTestConfig(t, legacyConfig+"appearance:\n  theme: \"sepia\"\n")
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want invalid theme rejected")
	}
	if !strings.Contains(err.Error(), "appearance.theme") || !strings.Contains(err.Error(), "system") {
		t.Errorf("error %q does not name the key and accepted values", err)
	}
}

func TestThemeEnvironmentOverride(t *testing.T) {
	t.Setenv("SUSSURRO_APPEARANCE_THEME", "light")
	cfg, err := loadTestConfig(t, legacyConfig+"appearance:\n  theme: \"dark\"\n")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Appearance.Theme != ThemeLight {
		t.Errorf("Appearance.Theme = %q, want environment override %q", cfg.Appearance.Theme, ThemeLight)
	}
}

func TestSaveThemeUsesLoadedConfigAndPreservesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.yaml")
	body := legacyConfig + `# Keep this comment.
appearance:
  theme: "system"
`
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	cfg := loadAppearanceConfigPath(t, path)

	if err := SaveTheme(cfg, ThemeLight); err != nil {
		t.Fatalf("SaveTheme() error = %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `theme: 'light'`) {
		t.Errorf("saved config has no light theme:\n%s", written)
	}
	if !strings.Contains(string(written), "# Keep this comment.") ||
		!strings.Contains(string(written), `trigger: "ctrl+shift+space"`) {
		t.Errorf("SaveTheme() disturbed unrelated config:\n%s", written)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("saved mode = %o, want 640", got)
	}
}

func TestSaveThemeKeepsLoadedConfigSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.yaml")
	link := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(target, []byte(legacyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	cfg := loadAppearanceConfigPath(t, link)

	if err := SaveTheme(cfg, ThemeDark); err != nil {
		t.Fatalf("SaveTheme() error = %v", err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("SaveTheme() replaced the config symlink")
	}
	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `theme: 'dark'`) {
		t.Errorf("symlink target has no dark theme:\n%s", written)
	}
}

func TestSaveThemeCreatesAppearanceSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(legacyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadAppearanceConfigPath(t, path)

	if err := SaveTheme(cfg, ThemeDark); err != nil {
		t.Fatalf("SaveTheme() error = %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadTestConfig(t, string(written))
	if err != nil {
		t.Fatalf("reloading saved config: %v\n%s", err, written)
	}
	if reloaded.Appearance.Theme != ThemeDark {
		t.Errorf("saved Appearance.Theme = %q, want %q", reloaded.Appearance.Theme, ThemeDark)
	}
}

func TestSaveThemeRejectsInvalidValueBeforeWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(legacyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadAppearanceConfigPath(t, path)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := SaveTheme(cfg, Theme("sepia")); err == nil {
		t.Fatal("SaveTheme() error = nil, want invalid value rejected")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("invalid SaveTheme() changed the file:\n%s", after)
	}
}
