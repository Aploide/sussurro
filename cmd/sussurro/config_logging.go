package main

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/aploide/sussurro/internal/config"
	"github.com/aploide/sussurro/internal/pipeline"
)

const defaultMinDuration = "300ms"

// logEffectiveConfiguration resolves the audio fallbacks before reporting and
// returning them, so the pipeline receives exactly the values named in the log.
func logEffectiveConfiguration(log *slog.Logger, cfg *config.Config) (string, string) {
	maxDuration, maxErr := pipeline.ResolveMaxDuration(cfg.Audio.MaxDuration)
	if maxErr != nil {
		log.Warn("Invalid max_duration, using default",
			"value", cfg.Audio.MaxDuration, "default", pipeline.DefaultMaxDuration, "error", maxErr)
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
