package input

import "github.com/aploide/sussurro/internal/session"

// Gesture is what the detector produces from raw key events.
type Gesture uint8

const (
	// GestureNone means the event changed nothing the workflow cares about.
	GestureNone Gesture = iota
	// GesturePress means the chord became fully held.
	GesturePress
	// GestureRelease means a fully-held chord stopped being held.
	GestureRelease
	// GestureCancel means the cancel chord became fully held.
	GestureCancel
	gestureCount
)

// Valid reports whether gesture is a defined gesture.
func (gesture Gesture) Valid() bool { return gesture < gestureCount }

func (gesture Gesture) String() string {
	switch gesture {
	case GestureNone:
		return "none"
	case GesturePress:
		return "press"
	case GestureRelease:
		return "release"
	case GestureCancel:
		return "cancel"
	default:
		return "invalid"
	}
}

// InputEvent maps a gesture onto a platform-neutral recording event, and
// reports whether it is a recording gesture. Cancel is a workflow action.
func (gesture Gesture) InputEvent() (session.InputEvent, bool) {
	switch gesture {
	case GesturePress:
		return session.InputPress, true
	case GestureRelease:
		return session.InputRelease, true
	default:
		return 0, false
	}
}

// Detector turns raw evdev key events into gestures. It is pure: no device,
// no timers, no goroutines, so every gesture rule is directly testable.
//
// Behavioral reference: flt-james/master internal/ptt/evdev.go (7c9c12e),
// which hardcodes Ctrl+Shift+Space and a fixed cancel combination. Here both
// chords come from configuration.
type Detector struct {
	chord  Chord
	cancel Chord

	// held tracks the keys currently down that either chord cares about.
	held map[KeyCode]bool
	// active reports whether the recording chord is currently satisfied.
	active bool
	// cancelActive suppresses repeated cancels while the chord stays held.
	cancelActive bool
}

// NewDetector builds a detector for a recording chord and an optional cancel
// chord. An empty cancel chord disables cancellation.
func NewDetector(chord, cancel Chord) *Detector {
	return &Detector{chord: chord, cancel: cancel, held: make(map[KeyCode]bool)}
}

// Chord returns the recording chord.
func (d *Detector) Chord() Chord { return d.chord }

// Keys returns every key code the detector needs a device to report.
func (d *Detector) Keys() []KeyCode {
	return append(d.chord.Keys(), d.cancel.Keys()...)
}

// Active reports whether the recording chord is currently held.
func (d *Detector) Active() bool { return d.active }

// Handle applies one key event and returns the gesture it produced.
//
// Autorepeat carries no state change, so it is ignored: a held chord must not
// re-fire a press. Releasing any one part of a held chord ends the gesture,
// which is what a user pressing Ctrl+Shift+Space and lifting only Space
// expects; the remaining keys cannot re-trigger a press until the chord is
// fully released and formed again.
func (d *Detector) Handle(code KeyCode, state KeyState) Gesture {
	tracked := d.chord.tracks(code) || d.cancel.tracks(code)
	if !tracked {
		return GestureNone
	}

	switch state {
	case KeyPressed:
		d.held[code] = true
	case KeyReleased:
		delete(d.held, code)
	default:
		// Autorepeat and any unknown value leave the held set untouched.
		return GestureNone
	}

	// Cancel is checked first: when both chords are satisfied the user asked
	// to abandon the session, not to record.
	if !d.cancel.Empty() {
		cancelHeld := d.cancel.heldBy(d.held)
		if cancelHeld && !d.cancelActive {
			d.cancelActive = true
			// A cancel supersedes any recording gesture in progress.
			d.active = false
			return GestureCancel
		}
		if !cancelHeld {
			d.cancelActive = false
		}
	}

	held := d.chord.heldBy(d.held)
	switch {
	case held && !d.active:
		d.active = true
		return GesturePress
	case !held && d.active:
		d.active = false
		return GestureRelease
	default:
		return GestureNone
	}
}

// Reset clears all held-key state. Used when a device is reopened, so keys
// held across the gap cannot leave the detector permanently armed.
func (d *Detector) Reset() {
	d.held = make(map[KeyCode]bool)
	d.active = false
	d.cancelActive = false
}
