//go:build !linux

package ui

// evdevAvailable reports evdev as unavailable off Linux, where /dev/input
// does not exist.
func evdevAvailable() (bool, string) { return false, "Linux only" }
