package config

import (
	"strings"
	"testing"
)

func TestLegacyConfigDefaultsToSkippingLLMCleanup(t *testing.T) {
	cfg, err := loadTestConfig(t, legacyConfig)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.App.SkipLLMCleanup {
		t.Error("legacy config enables synchronous LLM cleanup on the release-to-clipboard path")
	}
}

func TestConfigCanOptIntoLLMCleanup(t *testing.T) {
	body := strings.Replace(legacyConfig, "log_level: \"info\"", "log_level: \"info\"\n  skip_llm_cleanup: false", 1)
	cfg, err := loadTestConfig(t, body)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.App.SkipLLMCleanup {
		t.Error("explicit skip_llm_cleanup: false was ignored")
	}
}
