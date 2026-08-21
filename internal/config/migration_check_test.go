package config

import (
	"os"
	"testing"
)

// The user's real config uses the superseded trigger/mode form, so migration
// is the difference between their hotkey working and silently vanishing.
func TestUserConfigMigrates(t *testing.T) {
	path := os.Getenv("HOME") + "/.sussurro/config.yaml"
	body, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no user config at %s", path)
	}

	cfg, err := loadTestConfig(t, string(body))
	if err != nil {
		t.Fatalf("LoadConfig() on the user's config: %v", err)
	}
	if !cfg.Hotkey.Configured() {
		t.Fatal("the user's config produced no hotkey binding at all")
	}
	t.Logf("push_to_talk=%q toggle=%q", cfg.Hotkey.PushToTalk, cfg.Hotkey.Toggle)
}
