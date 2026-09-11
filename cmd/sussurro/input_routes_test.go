package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/aploide/sussurro/internal/config"
)

// The two helpers decide, between them, which input routes stay up. Every
// combination must leave at least one route active wherever one can work:
// a config that silently disables all input is worse than any warning.
func TestInputRoutes(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	cases := []struct {
		name        string
		backend     config.InputBackend
		wayland     bool
		evdevActive bool
		socketUp    bool
		wantSocket  bool
		wantNative  bool
	}{
		{"auto x11", config.InputAuto, false, false, true, true, true},
		{"auto wayland", config.InputAuto, true, false, true, true, false},
		{"native x11", config.InputNative, false, false, false, false, true},
		// native on Wayland cannot grab keys, so the socket must stay up.
		{"native wayland", config.InputNative, true, false, true, true, false},
		{"trigger socket up", config.InputTrigger, false, false, true, true, false},
		// A socket that failed to listen falls back to the grab.
		{"trigger socket down x11", config.InputTrigger, false, false, false, true, true},
		{"trigger socket down wayland", config.InputTrigger, true, false, false, true, false},
		{"evdev running", config.InputEvdev, false, true, true, true, false},
		// evdev that did not start keeps the grab as its fallback.
		{"evdev failed x11", config.InputEvdev, false, false, true, true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := isWayland
			isWayland = func() bool { return tc.wayland }
			defer func() { isWayland = prev }()

			cfg := &config.Config{}
			cfg.Workflow.Input.Backend = tc.backend

			if got := useTriggerSocket(cfg); got != tc.wantSocket {
				t.Errorf("useTriggerSocket = %v, want %v", got, tc.wantSocket)
			}
			if got := useNativeHotkeys(cfg, tc.evdevActive, tc.socketUp, log); got != tc.wantNative {
				t.Errorf("useNativeHotkeys = %v, want %v", got, tc.wantNative)
			}
			if !tc.wantSocket && !tc.wantNative && !tc.evdevActive {
				t.Errorf("no input route left active")
			}
		})
	}
}
