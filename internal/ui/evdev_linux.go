//go:build linux

package ui

import (
	"errors"
	"io/fs"
	"os"

	"github.com/aploide/sussurro/internal/input"
)

// evdevAvailable reports whether evdev input can actually be used, and why not
// when it cannot. Opening a device is the only reliable test: discovery
// succeeds without permission to read the devices themselves.
func evdevAvailable() (bool, string) {
	candidates, err := input.DiscoverKeyboards()
	if err != nil || len(candidates) == 0 {
		return false, "no keyboard devices found under /dev/input"
	}

	file, err := os.Open(candidates[0].Path)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return false, "requires membership of the 'input' group"
		}
		return false, "input devices cannot be opened"
	}
	file.Close()
	return true, ""
}
