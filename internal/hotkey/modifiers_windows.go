//go:build windows

package hotkey

import (
	"fmt"

	"golang.design/x/hotkey"
)

// parseModifier parses a modifier string to hotkey.Modifier for Windows.
// On Windows the hotkey.Modifier values are the native Win32 MOD_* bits
// (MOD_ALT=0x1, MOD_CONTROL=0x2, MOD_SHIFT=0x4, MOD_WIN=0x8).
func parseModifier(part string) (hotkey.Modifier, error) {
	switch part {
	case "ctrl", "control":
		return hotkey.ModCtrl, nil
	case "shift":
		return hotkey.ModShift, nil
	case "alt", "option":
		return hotkey.ModAlt, nil
	case "cmd", "command", "super", "meta", "win":
		return hotkey.ModWin, nil
	default:
		return 0, fmt.Errorf("unknown modifier: %s", part)
	}
}
