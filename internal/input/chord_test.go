package input

import (
	"strings"
	"testing"

	"github.com/aploide/sussurro/internal/session"
)

// mustParse parses a chord or fails the test.
func mustParse(t *testing.T, spec string) Chord {
	t.Helper()
	chord, err := ParseChord(spec)
	if err != nil {
		t.Fatalf("ParseChord(%q) error = %v", spec, err)
	}
	return chord
}

func TestParseChordAcceptsConfiguredCombinations(t *testing.T) {
	tests := []struct {
		spec  string
		parts int
	}{
		{spec: "ctrl+shift+space", parts: 3},
		{spec: "CTRL+SHIFT+SPACE", parts: 3},
		{spec: " ctrl + shift + space ", parts: 3},
		{spec: "super+d", parts: 2},
		{spec: "alt+capslock", parts: 2},
		{spec: "ctrl+alt+shift+esc", parts: 4},
		{spec: "space", parts: 1},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			chord := mustParse(t, tt.spec)
			if len(chord.parts) != tt.parts {
				t.Errorf("parsed %d parts, want %d", len(chord.parts), tt.parts)
			}
		})
	}
}

func TestParseChordRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		spec string
		want string
	}{
		{name: "empty", spec: "   ", want: "empty"},
		{name: "empty component", spec: "ctrl++space", want: "empty component"},
		{name: "unknown key", spec: "ctrl+hyperspace", want: "unknown key"},
		{name: "duplicate", spec: "ctrl+ctrl+space", want: "more than once"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseChord(tt.spec)
			if err == nil {
				t.Fatalf("ParseChord(%q) error = nil, want a rejection", tt.spec)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not explain %q", err, tt.want)
			}
		})
	}
}

func TestParseChordErrorListsAcceptedNames(t *testing.T) {
	_, err := ParseChord("ctrl+nonsense")
	if err == nil {
		t.Fatal("ParseChord() error = nil, want a rejection")
	}
	// The message must be actionable without consulting the source.
	for _, name := range []string{"ctrl", "shift", "space"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not list %q as accepted", err, name)
		}
	}
}

func TestChordAcceptsEitherModifierSide(t *testing.T) {
	tests := []struct {
		name string
		keys []KeyCode
	}{
		{name: "left modifiers", keys: []KeyCode{KeyLeftCtrl, KeyLeftShift, KeySpace}},
		{name: "right modifiers", keys: []KeyCode{KeyRightCtrl, KeyRightShift, KeySpace}},
		{name: "mixed sides", keys: []KeyCode{KeyLeftCtrl, KeyRightShift, KeySpace}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})

			var last Gesture
			for _, key := range tt.keys {
				last = detector.Handle(key, KeyPressed)
			}
			if last != GesturePress {
				t.Errorf("final gesture = %s, want press", last)
			}
		})
	}
}

func TestChordDetectedInAnyPressOrder(t *testing.T) {
	orders := [][]KeyCode{
		{KeyLeftCtrl, KeyLeftShift, KeySpace},
		{KeySpace, KeyLeftCtrl, KeyLeftShift},
		{KeyLeftShift, KeySpace, KeyLeftCtrl},
		{KeySpace, KeyLeftShift, KeyLeftCtrl},
	}

	for _, order := range orders {
		t.Run(keyList(order), func(t *testing.T) {
			detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})

			presses := 0
			for _, key := range order {
				if detector.Handle(key, KeyPressed) == GesturePress {
					presses++
				}
			}
			if presses != 1 {
				t.Errorf("press gestures = %d, want exactly 1", presses)
			}
			if !detector.Active() {
				t.Error("Active() = false after the chord completed")
			}
		})
	}
}

func TestAutorepeatDoesNotRetrigger(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})
	hold(t, detector)

	// Holding the chord streams autorepeat; none of it is a new gesture.
	for i := 0; i < 10; i++ {
		for _, key := range []KeyCode{KeyLeftCtrl, KeyLeftShift, KeySpace} {
			if got := detector.Handle(key, KeyRepeated); got != GestureNone {
				t.Fatalf("autorepeat produced %s, want none", got)
			}
		}
	}
	if !detector.Active() {
		t.Error("Active() = false during autorepeat, want the chord still held")
	}
}

func TestAutorepeatCannotCompleteAChord(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})

	detector.Handle(KeyLeftCtrl, KeyPressed)
	detector.Handle(KeyLeftShift, KeyPressed)

	// Autorepeat carries no state change, so a repeat of a key that was never
	// pressed must not complete the chord.
	if got := detector.Handle(KeySpace, KeyRepeated); got != GestureNone {
		t.Fatalf("gesture = %s, want autorepeat to carry no state change", got)
	}
	if detector.Active() {
		t.Error("Active() = true, want autorepeat unable to complete a chord")
	}
}

func TestAutorepeatDoesNotResurrectAReleasedKey(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})
	hold(t, detector)

	if got := detector.Handle(KeySpace, KeyReleased); got != GestureRelease {
		t.Fatalf("gesture = %s, want release", got)
	}
	// A stale repeat for the released key must not re-arm the chord.
	if got := detector.Handle(KeySpace, KeyRepeated); got != GestureNone {
		t.Errorf("gesture = %s, want the released key to stay released", got)
	}
	if detector.Active() {
		t.Error("Active() = true after a stale autorepeat, want false")
	}
}

func TestPartialReleaseEndsTheGesture(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})
	hold(t, detector)

	// Lifting only Space ends the gesture; the user stopped speaking.
	if got := detector.Handle(KeySpace, KeyReleased); got != GestureRelease {
		t.Fatalf("gesture = %s, want release", got)
	}
	if detector.Active() {
		t.Error("Active() = true after a partial release")
	}

	// The still-held modifiers must not re-arm anything on their own.
	if got := detector.Handle(KeyLeftShift, KeyReleased); got != GestureNone {
		t.Errorf("gesture = %s, want none", got)
	}
	if got := detector.Handle(KeyLeftCtrl, KeyReleased); got != GestureNone {
		t.Errorf("gesture = %s, want none", got)
	}
}

func TestChordCanBeFormedAgainAfterRelease(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})

	for round := 0; round < 3; round++ {
		hold(t, detector)
		if got := detector.Handle(KeySpace, KeyReleased); got != GestureRelease {
			t.Fatalf("round %d: gesture = %s, want release", round, got)
		}
		detector.Handle(KeyLeftShift, KeyReleased)
		detector.Handle(KeyLeftCtrl, KeyReleased)
	}
}

func TestSpaceAloneDoesNotTriggerAModifierChord(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})

	// Ordinary typing must never start a recording.
	if got := detector.Handle(KeySpace, KeyPressed); got != GestureNone {
		t.Errorf("gesture = %s for space alone, want none", got)
	}
	if detector.Active() {
		t.Error("Active() = true for space alone")
	}
}

func TestUntrackedKeysAreIgnored(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})

	// A key outside the chord must not participate at all.
	if got := detector.Handle(KeyCapsLock, KeyPressed); got != GestureNone {
		t.Errorf("gesture = %s for an untracked key, want none", got)
	}
	hold(t, detector)
	if got := detector.Handle(KeyCapsLock, KeyReleased); got != GestureNone {
		t.Errorf("gesture = %s, want the held chord undisturbed", got)
	}
	if !detector.Active() {
		t.Error("Active() = false, want the chord still held")
	}
}

func TestCancelChordProducesCancel(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), mustParse(t, "ctrl+shift+alt"))

	detector.Handle(KeyLeftCtrl, KeyPressed)
	detector.Handle(KeyLeftShift, KeyPressed)
	if got := detector.Handle(KeyLeftAlt, KeyPressed); got != GestureCancel {
		t.Fatalf("gesture = %s, want cancel", got)
	}
	// Holding the cancel chord must not repeat the cancel.
	if got := detector.Handle(KeyLeftAlt, KeyRepeated); got != GestureNone {
		t.Errorf("gesture = %s from autorepeat while holding cancel, want none", got)
	}
	// Re-pressing one part while the chord stays satisfied is not a new cancel.
	if got := detector.Handle(KeyLeftShift, KeyPressed); got != GestureNone {
		t.Errorf("gesture = %s while cancel stays held, want none", got)
	}
}

func TestCancelSupersedesAnActiveRecording(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), mustParse(t, "ctrl+shift+alt"))
	hold(t, detector)

	// The user reaches for cancel without letting go of the chord.
	if got := detector.Handle(KeyLeftAlt, KeyPressed); got != GestureCancel {
		t.Fatalf("gesture = %s, want cancel", got)
	}
	if detector.Active() {
		t.Error("Active() = true after cancel, want the recording abandoned")
	}
}

func TestCancelRearmsAfterRelease(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), mustParse(t, "ctrl+shift+alt"))

	press := func() Gesture {
		detector.Handle(KeyLeftCtrl, KeyPressed)
		detector.Handle(KeyLeftShift, KeyPressed)
		return detector.Handle(KeyLeftAlt, KeyPressed)
	}
	release := func() {
		detector.Handle(KeyLeftAlt, KeyReleased)
		detector.Handle(KeyLeftShift, KeyReleased)
		detector.Handle(KeyLeftCtrl, KeyReleased)
	}

	if got := press(); got != GestureCancel {
		t.Fatalf("first cancel = %s, want cancel", got)
	}
	release()
	if got := press(); got != GestureCancel {
		t.Errorf("second cancel = %s, want cancel after re-forming the chord", got)
	}
}

func TestNoCancelChordDisablesCancellation(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})

	detector.Handle(KeyLeftCtrl, KeyPressed)
	detector.Handle(KeyLeftShift, KeyPressed)
	if got := detector.Handle(KeyLeftAlt, KeyPressed); got != GestureNone {
		t.Errorf("gesture = %s with no cancel chord configured, want none", got)
	}
}

func TestResetClearsHeldKeys(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})
	hold(t, detector)

	// Reopening a device must not leave the detector armed by stale keys.
	detector.Reset()
	if detector.Active() {
		t.Error("Active() = true after Reset()")
	}
	if got := detector.Handle(KeySpace, KeyReleased); got != GestureNone {
		t.Errorf("gesture = %s after Reset(), want none", got)
	}
}

func TestGestureMapsToInputEvents(t *testing.T) {
	tests := []struct {
		gesture Gesture
		want    session.InputEvent
		isInput bool
	}{
		{gesture: GesturePress, want: session.InputPress, isInput: true},
		{gesture: GestureRelease, want: session.InputRelease, isInput: true},
		{gesture: GestureCancel, isInput: false},
		{gesture: GestureNone, isInput: false},
	}

	for _, tt := range tests {
		t.Run(tt.gesture.String(), func(t *testing.T) {
			got, isInput := tt.gesture.InputEvent()
			if isInput != tt.isInput {
				t.Fatalf("isInput = %v, want %v", isInput, tt.isInput)
			}
			if isInput && got != tt.want {
				t.Errorf("event = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGestureValues(t *testing.T) {
	for gesture, name := range map[Gesture]string{
		GestureNone:    "none",
		GesturePress:   "press",
		GestureRelease: "release",
		GestureCancel:  "cancel",
	} {
		if !gesture.Valid() {
			t.Errorf("%s should be valid", name)
		}
		if got := gesture.String(); got != name {
			t.Errorf("String() = %q, want %q", got, name)
		}
	}
	invalid := Gesture(gestureCount)
	if invalid.Valid() || invalid.String() != "invalid" {
		t.Errorf("out-of-range gesture misdescribed: %s", invalid)
	}
}

func TestDetectorKeysCoverBothChords(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+space"), mustParse(t, "alt+esc"))

	wanted := map[KeyCode]bool{
		KeyLeftCtrl: false, KeyRightCtrl: false, KeySpace: false,
		KeyLeftAlt: false, KeyRightAlt: false, KeyEsc: false,
	}
	for _, key := range detector.Keys() {
		if _, ok := wanted[key]; ok {
			wanted[key] = true
		}
	}
	for key, found := range wanted {
		if !found {
			t.Errorf("Keys() omits %s, so device capability checks would miss it", key)
		}
	}
}

// hold presses a full ctrl+shift+space chord, asserting the press fires.
func hold(t *testing.T, detector *Detector) {
	t.Helper()
	detector.Handle(KeyLeftCtrl, KeyPressed)
	detector.Handle(KeyLeftShift, KeyPressed)
	if got := detector.Handle(KeySpace, KeyPressed); got != GesturePress {
		t.Fatalf("gesture = %s, want press", got)
	}
}

// keyList renders key codes for subtest names.
func keyList(keys []KeyCode) string {
	names := make([]string, 0, len(keys))
	for _, key := range keys {
		names = append(names, key.String())
	}
	return strings.Join(names, ",")
}
