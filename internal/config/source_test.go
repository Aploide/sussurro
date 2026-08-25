package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestLoadConfigReportsExplicitSource(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := cfg.SourcePath(); got != path {
		t.Errorf("SourcePath() = %q, want %q", got, path)
	}
}

func TestLoadConfigReportsUserFileSelectedAheadOfFallback(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	root := t.TempDir()
	home := filepath.Join(root, "home")
	userPath := filepath.Join(home, ".sussurro", "config.yaml")
	fallbackPath := filepath.Join(root, "configs", "default.yaml")
	for _, path := range []string{userPath, fallbackPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.SourcePath(); got != userPath {
		t.Errorf("SourcePath() = %q, want user config %q", got, userPath)
	}
}
