package hotkey

import (
	"strings"
	"testing"
)

func TestParseTriggerAcceptsDigits(t *testing.T) {
	// A digit trigger previously failed to register, so --no-ui exited at
	// startup for anyone using one.
	for _, trigger := range []string{
		"super+7", "ctrl+shift+0", "alt+1", "ctrl+9",
	} {
		t.Run(trigger, func(t *testing.T) {
			mods, key, err := ParseTrigger(trigger)
			if err != nil {
				t.Fatalf("ParseTrigger(%q) error = %v", trigger, err)
			}
			if len(mods) == 0 {
				t.Errorf("ParseTrigger(%q) returned no modifiers", trigger)
			}
			if key == 0 {
				t.Errorf("ParseTrigger(%q) returned no key", trigger)
			}
		})
	}
}

func TestParseTriggerAcceptsTheUsualKeys(t *testing.T) {
	for _, trigger := range []string{
		"ctrl+shift+space", "ctrl+alt+f5", "super+d", "alt+tab", "ctrl+esc",
	} {
		t.Run(trigger, func(t *testing.T) {
			if _, _, err := ParseTrigger(trigger); err != nil {
				t.Errorf("ParseTrigger(%q) error = %v", trigger, err)
			}
		})
	}
}

func TestParseTriggerErrorNamesTheTriggerAndAlternatives(t *testing.T) {
	_, _, err := ParseTrigger("ctrl+hyperspace")
	if err == nil {
		t.Fatal("ParseTrigger() error = nil, want a rejection")
	}

	// The old message printed only the offending fragment, which read as a
	// keycode and sent the reader looking in the wrong place.
	if !strings.Contains(err.Error(), "ctrl+hyperspace") {
		t.Errorf("error %q does not name the trigger the user configured", err)
	}
	if !strings.Contains(err.Error(), "space") {
		t.Errorf("error %q does not list the accepted key names", err)
	}
}

func TestParseTriggerRejectsEmpty(t *testing.T) {
	if _, _, err := ParseTrigger(""); err == nil {
		t.Error("ParseTrigger(\"\") error = nil, want a rejection")
	}
}
