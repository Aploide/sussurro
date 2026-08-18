//go:build linux

package main

import (
	"log/slog"

	"github.com/aploide/sussurro/internal/config"
	inputpkg "github.com/aploide/sussurro/internal/input"
	"github.com/aploide/sussurro/internal/session"
)

// startEvdevInput starts the optional evdev backend when it is explicitly
// configured. It returns a stop function, or nil when evdev is not in use.
//
// auto deliberately never reaches evdev: native hotkeys work without special
// permissions, and requiring input group membership by default would break
// dictation on an ordinary host.
func startEvdevInput(cfg *config.Config, dispatch session.InputDispatcher, onCancel func(), log *slog.Logger) func() {
	if cfg.Workflow.Input.Backend != config.InputEvdev {
		return nil
	}

	chord := cfg.Workflow.Input.Chord
	if chord == "" {
		// Follow the configured hotkey so one setting drives both backends.
		chord = cfg.Hotkey.Trigger
	}

	backend, err := inputpkg.StartEvdev(inputpkg.Options{
		Device:      cfg.Workflow.Input.Device,
		Chord:       chord,
		CancelChord: cfg.Workflow.Input.CancelChord,
	}, dispatch, onCancel, log)
	if err != nil {
		// Falling back keeps dictation working: the native hotkey and the
		// trigger socket need no special permissions.
		log.Error("evdev input unavailable, falling back to the default backend", "error", err)
		log.Info("Available input devices", "devices", inputpkg.Describe())
		return nil
	}

	return backend.Stop
}
