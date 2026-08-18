//go:build linux

package input

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"
)

// inputEvent mirrors the kernel's struct input_event.
type inputEvent struct {
	Sec   int64
	Usec  int64
	Type  uint16
	Code  uint16
	Value int32
}

// inputEventSize is the wire size of one event.
const inputEventSize = int(unsafe.Sizeof(inputEvent{}))

// evKey is the event type carrying key presses.
const evKey = 1

// Device paths. by-id names are stable across reboots and USB port changes,
// so they are preferred over the event* numbering, which is assignment order.
const (
	byIDDir     = "/dev/input/by-id"
	eventGlob   = "/dev/input/event*"
	sysInputDir = "/sys/class/input"
)

// keyboardSuffix marks the by-id symlinks the kernel creates for keyboards.
const keyboardSuffix = "-event-kbd"

// DeviceCandidate is a discovered input device.
type DeviceCandidate struct {
	// Path is the device node to open.
	Path string
	// Name is the human-readable device name from sysfs, when available.
	Name string
	// Stable reports whether Path came from the by-id directory.
	Stable bool
}

// DiscoverKeyboards lists candidate keyboard devices, stable paths first.
// Discovery reads only directory entries and sysfs, so it works without
// permission to open the devices themselves.
func DiscoverKeyboards() ([]DeviceCandidate, error) {
	var candidates []DeviceCandidate
	seen := make(map[string]bool)

	// Prefer /dev/input/by-id: those names survive reboots and re-plugging.
	entries, err := os.ReadDir(byIDDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("reading %s: %w", byIDDir, err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), keyboardSuffix) {
			continue
		}
		path := filepath.Join(byIDDir, entry.Name())
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		candidates = append(candidates, DeviceCandidate{
			Path:   path,
			Name:   deviceName(resolved),
			Stable: true,
		})
	}

	// Fall back to the event* nodes for devices with no by-id entry.
	matches, err := filepath.Glob(eventGlob)
	if err != nil {
		return nil, fmt.Errorf("globbing %s: %w", eventGlob, err)
	}
	sort.Strings(matches)
	for _, path := range matches {
		if seen[path] {
			continue
		}
		seen[path] = true
		candidates = append(candidates, DeviceCandidate{Path: path, Name: deviceName(path)})
	}

	return candidates, nil
}

// deviceName reads a device's name from sysfs, returning "" when unavailable.
func deviceName(path string) string {
	data, err := os.ReadFile(filepath.Join(sysInputDir, filepath.Base(path), "device", "name"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// MatchDevice selects a device by a case-insensitive substring of its name, or
// by an exact path. An empty pattern selects the first stable keyboard.
//
// Behavioral reference: flt-james/master internal/ptt/evdev.go (7c9c12e)
// matches on name substring only and hardcodes the device, so a rename or a
// second matching keyboard silently selects the wrong one. Matches here are
// validated and ambiguity is reported.
func MatchDevice(pattern string, candidates []DeviceCandidate) (DeviceCandidate, error) {
	if len(candidates) == 0 {
		return DeviceCandidate{}, fmt.Errorf("no keyboard devices found under /dev/input")
	}

	if pattern == "" {
		// Prefer a stable by-id path so the choice survives a reboot.
		for _, candidate := range candidates {
			if candidate.Stable {
				return candidate, nil
			}
		}
		return candidates[0], nil
	}

	// An exact path wins outright: the user named a specific device.
	for _, candidate := range candidates {
		if candidate.Path == pattern {
			return candidate, nil
		}
	}

	needle := strings.ToLower(pattern)
	var matched []DeviceCandidate
	for _, candidate := range candidates {
		if strings.Contains(strings.ToLower(candidate.Name), needle) ||
			strings.Contains(strings.ToLower(filepath.Base(candidate.Path)), needle) {
			matched = append(matched, candidate)
		}
	}

	switch len(matched) {
	case 0:
		return DeviceCandidate{}, fmt.Errorf("no input device matches %q; available: %s",
			pattern, describeCandidates(candidates))
	case 1:
		return matched[0], nil
	default:
		// Silently picking one would bind the hotkey to the wrong keyboard.
		return DeviceCandidate{}, fmt.Errorf("%q matches %d devices; use a more specific name or an exact path: %s",
			pattern, len(matched), describeCandidates(matched))
	}
}

// describeCandidates renders devices for diagnostics.
func describeCandidates(candidates []DeviceCandidate) string {
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Name != "" {
			parts = append(parts, fmt.Sprintf("%s (%s)", candidate.Path, candidate.Name))
			continue
		}
		parts = append(parts, candidate.Path)
	}
	return strings.Join(parts, "; ")
}

// PermissionAdvice turns an open failure into an actionable message. Reading
// /dev/input normally requires membership of the input group, and a group
// change only takes effect on a new login session.
func PermissionAdvice(path string, err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("cannot read %s: %w\n"+
			"evdev input requires membership of the 'input' group. Add yourself with:\n"+
			"    sudo usermod -aG input $USER\n"+
			"then log out and back in for the group to take effect. "+
			"Alternatively set workflow.input.backend to native or trigger", path, err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("input device %s does not exist: %w", path, err)
	}
	return fmt.Errorf("cannot open %s: %w", path, err)
}

// decodeEvent parses one input_event from a buffer.
func decodeEvent(buf []byte) (inputEvent, bool) {
	if len(buf) < inputEventSize {
		return inputEvent{}, false
	}
	return inputEvent{
		Sec:   int64(binary.LittleEndian.Uint64(buf[0:8])),
		Usec:  int64(binary.LittleEndian.Uint64(buf[8:16])),
		Type:  binary.LittleEndian.Uint16(buf[16:18]),
		Code:  binary.LittleEndian.Uint16(buf[18:20]),
		Value: int32(binary.LittleEndian.Uint32(buf[20:24])),
	}, true
}
