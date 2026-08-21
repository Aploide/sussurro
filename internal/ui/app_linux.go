//go:build linux

package ui

import (
	"log/slog"

	ihk "github.com/aploide/sussurro/internal/hotkey"
)

// installOverlayHotkey registers an X11 global hotkey via GDK XGrabKey.
// On Wayland, the overlay is a *linuxOverlay but IsWayland() returns true,
// so the caller should skip this and use the trigger server instead.
func installOverlayHotkey(overlay Overlay, trigger string, onDown, onUp func()) {
	lo, ok := overlay.(*linuxOverlay)
	if !ok {
		// A nil or non-Linux overlay silently swallowed the hotkey install,
		// which is exactly how a dead hotkey goes unnoticed.
		slog.Error("Hotkey not installed: overlay is not available yet",
			"trigger", trigger, "overlay_nil", overlay == nil)
		return
	}
	lo.installHotkey(trigger, onDown, onUp)
}

// reinstallOverlayHotkey re-registers the X11 hotkey with a new trigger.
// On Wayland the hotkey is handled by the external trigger server (socket),
// so this is intentionally a no-op in that environment.
func reinstallOverlayHotkey(overlay Overlay, trigger string, onDown, onUp func()) {
	if ihk.IsWayland() {
		return
	}
	installOverlayHotkey(overlay, trigger, onDown, onUp)
}

// installOverlayContextMenu wires the right-click menu on the GTK3 overlay.
func installOverlayContextMenu(overlay Overlay, openSettings, quit func()) {
	if lo, ok := overlay.(*linuxOverlay); ok {
		lo.installContextMenu(openSettings, quit)
	}
}
