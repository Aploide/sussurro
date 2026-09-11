//go:build !linux

package ui

// watchTrayHost reports whether the tray is hosted. Windows and macOS always
// host a status item, and systray's onReady there really does mean the icon
// is up, so no probe is needed and the answer never changes.
func watchTrayHost(onHosted func(bool)) { onHosted(true) }
