//go:build linux

package input

import (
	"fmt"
	"log/slog"

	"github.com/aploide/sussurro/internal/session"
)

// Options configure the evdev backend.
type Options struct {
	// Device selects the input device by name substring or exact path.
	Device string
	// Chord is the recording key combination.
	Chord string
	// CancelChord abandons a review session. Empty disables cancellation.
	CancelChord string
}

// Evdev is a started evdev input backend.
type Evdev struct {
	reader *Reader
	device DeviceCandidate
}

// Device returns the device the backend opened.
func (e *Evdev) Device() DeviceCandidate { return e.device }

// Stop releases the device.
func (e *Evdev) Stop() { e.reader.Stop() }

// StartEvdev opens the configured device and begins producing gestures.
//
// This is only reached when evdev is explicitly configured. auto never opens
// /dev/input: native hotkeys work without special permissions, and requiring
// input group membership by default would break dictation on a normal host.
func StartEvdev(opts Options, dispatch session.InputDispatcher, onCancel func(), log *slog.Logger) (*Evdev, error) {
	chord, err := ParseChord(opts.Chord)
	if err != nil {
		return nil, err
	}

	var cancel Chord
	if opts.CancelChord != "" {
		cancel, err = ParseChord(opts.CancelChord)
		if err != nil {
			return nil, err
		}
	}

	candidates, err := DiscoverKeyboards()
	if err != nil {
		return nil, err
	}
	device, err := MatchDevice(opts.Device, candidates)
	if err != nil {
		return nil, err
	}

	file, err := OpenDevice(device.Path)
	if err != nil {
		return nil, err
	}

	log.Info("Using evdev input",
		"device", device.Path, "name", device.Name, "chord", chord.String())

	reader := NewReader(file, NewDetector(chord, cancel), dispatch, onCancel, log)
	reader.Start()

	return &Evdev{reader: reader, device: device}, nil
}

// Describe renders the discovered devices, for diagnostics when the configured
// device cannot be found.
func Describe() string {
	candidates, err := DiscoverKeyboards()
	if err != nil {
		return fmt.Sprintf("device discovery failed: %v", err)
	}
	if len(candidates) == 0 {
		return "no keyboard devices found under /dev/input"
	}
	return describeCandidates(candidates)
}
