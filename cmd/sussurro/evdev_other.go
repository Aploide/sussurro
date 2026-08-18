//go:build !linux

package main

import (
	"log/slog"

	"github.com/aploide/sussurro/internal/config"
	"github.com/aploide/sussurro/internal/session"
)

// startEvdevInput is a no-op off Linux, where /dev/input does not exist.
// Configuration validation still accepts the value, so a config shared across
// machines does not fail to load.
func startEvdevInput(cfg *config.Config, _ session.InputDispatcher, _ func(), log *slog.Logger) func() {
	if cfg.Workflow.Input.Backend == config.InputEvdev {
		log.Warn("evdev input is Linux-only; using the default backend")
	}
	return nil
}
