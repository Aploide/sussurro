//go:build windows

package hotkey

// IsWayland reports whether the session runs under Wayland. Never true on
// Windows; global hotkeys always work via RegisterHotKey.
func IsWayland() bool {
	return false
}
