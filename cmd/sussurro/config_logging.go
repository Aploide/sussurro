package main

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aploide/sussurro/internal/config"
)

const (
	defaultMaxDuration = "30s"
	defaultMinDuration = "300ms"
)

// logEffectiveConfiguration resolves the audio fallbacks before reporting and
// returning them, so the pipeline receives exactly the values named in the log.
func logEffectiveConfiguration(log *slog.Logger, cfg *config.Config) (string, string) {
	maxDuration, maxErr := effectiveMaxDuration(cfg.Audio.MaxDuration)
	if maxErr != nil {
		log.Warn("Invalid max_duration, using default",
			"value", cfg.Audio.MaxDuration, "default", defaultMaxDuration, "error", maxErr)
	}
	minDuration, minErr := effectiveMinDuration(cfg.Audio.MinDuration)
	if minErr != nil {
		log.Warn("Invalid min_duration, using default",
			"value", cfg.Audio.MinDuration, "default", defaultMinDuration, "error", minErr)
	}

	log.Info("Effective configuration",
		"config_path", cfg.SourcePath(),
		"max_duration", maxDuration,
		"min_duration", minDuration)
	return maxDuration, minDuration
}

func effectiveMaxDuration(value string) (string, error) {
	if strings.EqualFold(value, "infinite") || value == "0" {
		return "infinite", nil
	}
	if value == "" {
		return defaultMaxDuration, nil
	}
	if _, err := time.ParseDuration(value); err != nil {
		return defaultMaxDuration, err
	}
	return value, nil
}

func effectiveMinDuration(value string) (string, error) {
	if value == "" {
		return defaultMinDuration, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return defaultMinDuration, err
	}
	if duration <= 0 {
		return defaultMinDuration, fmt.Errorf("must be positive")
	}
	return value, nil
}
