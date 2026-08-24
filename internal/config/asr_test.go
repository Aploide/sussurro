package config

import (
	"path/filepath"
	"testing"
)

func TestASRConfigResolvedVADPath(t *testing.T) {
	t.Run("explicit path", func(t *testing.T) {
		cfg := ASRConfig{Path: "/models/whisper.bin", VADPath: "/other/vad.bin"}
		if got := cfg.ResolvedVADPath(); got != cfg.VADPath {
			t.Errorf("ResolvedVADPath() = %q, want %q", got, cfg.VADPath)
		}
	})

	t.Run("setup-managed path for existing config", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		cfg := ASRConfig{Path: filepath.Join("other", "whisper.bin")}
		want := filepath.Join(home, ".sussurro", "models", DefaultVADModelFilename)
		if got := cfg.ResolvedVADPath(); got != want {
			t.Errorf("ResolvedVADPath() = %q, want %q", got, want)
		}
	})
}
