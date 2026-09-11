// Package input provides recording gesture sources. The native hotkey and the
// Wayland trigger socket remain the defaults; evdev is an optional Linux
// adapter for compositors that cannot deliver global key press and release.
package input

import (
	"fmt"
	"sort"
	"strings"
)

// KeyCode is a Linux evdev key code (the codes in linux/input-event-codes.h).
type KeyCode uint16

// Key codes used by chords. Only the keys a chord can name are listed; the
// detector ignores everything else.
const (
	KeyEsc        KeyCode = 1
	KeyLeftCtrl   KeyCode = 29
	KeyLeftShift  KeyCode = 42
	KeyRightShift KeyCode = 54
	KeyLeftAlt    KeyCode = 56
	KeySpace      KeyCode = 57
	KeyCapsLock   KeyCode = 58
	KeyRightCtrl  KeyCode = 97
	KeyRightAlt   KeyCode = 100
	KeyLeftMeta   KeyCode = 125
	KeyRightMeta  KeyCode = 126
)

// KeyState is the value field of an evdev key event.
type KeyState int32

const (
	// KeyReleased is reported when a key goes up.
	KeyReleased KeyState = 0
	// KeyPressed is reported when a key goes down.
	KeyPressed KeyState = 1
	// KeyRepeated is autorepeat while a key is held. It carries no state
	// change and must never re-trigger a gesture.
	KeyRepeated KeyState = 2
)

// modifierAliases maps configuration names to the key codes that satisfy them.
// Either side satisfies a modifier, so a chord works whichever Ctrl the user
// presses. Names match the existing hotkey parser so one config string can
// drive both backends.
var modifierAliases = map[string][]KeyCode{
	"ctrl":    {KeyLeftCtrl, KeyRightCtrl},
	"control": {KeyLeftCtrl, KeyRightCtrl},
	"shift":   {KeyLeftShift, KeyRightShift},
	"alt":     {KeyLeftAlt, KeyRightAlt},
	"option":  {KeyLeftAlt, KeyRightAlt},
	"super":   {KeyLeftMeta, KeyRightMeta},
	"meta":    {KeyLeftMeta, KeyRightMeta},
	"cmd":     {KeyLeftMeta, KeyRightMeta},
	"command": {KeyLeftMeta, KeyRightMeta},
}

// literalKeys maps configuration names for non-modifier keys.
var literalKeys = map[string]KeyCode{
	"space":    KeySpace,
	"esc":      KeyEsc,
	"escape":   KeyEsc,
	"capslock": KeyCapsLock,
}

// keyNames renders key codes in diagnostics.
var keyNames = map[KeyCode]string{
	KeyEsc:        "esc",
	KeyLeftCtrl:   "left-ctrl",
	KeyRightCtrl:  "right-ctrl",
	KeyLeftShift:  "left-shift",
	KeyRightShift: "right-shift",
	KeyLeftAlt:    "left-alt",
	KeyRightAlt:   "right-alt",
	KeySpace:      "space",
	KeyCapsLock:   "capslock",
	KeyLeftMeta:   "left-super",
	KeyRightMeta:  "right-super",
}

// String renders a key code for logs and errors.
func (code KeyCode) String() string {
	if name, ok := keyNames[code]; ok {
		return name
	}
	return fmt.Sprintf("key-%d", uint16(code))
}

// chordPart is one component of a chord: the set of key codes that satisfy it.
// A modifier is satisfied by either side; a literal key by exactly itself.
type chordPart struct {
	name  string
	codes []KeyCode
}

// satisfiedBy reports whether any held key satisfies this part.
func (part chordPart) satisfiedBy(held map[KeyCode]bool) bool {
	for _, code := range part.codes {
		if held[code] {
			return true
		}
	}
	return false
}

// Chord is a parsed key combination. Detection is order-independent: the user
// may press the parts in any sequence.
type Chord struct {
	parts []chordPart
}

// ParseChord parses a chord string such as "ctrl+shift+space". Parts may be
// given in any order and any letter case. Errors name the offending part and
// the accepted names.
func ParseChord(spec string) (Chord, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return Chord{}, fmt.Errorf("chord: empty specification")
	}

	var chord Chord
	seen := make(map[string]bool)

	for _, raw := range strings.Split(trimmed, "+") {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			return Chord{}, fmt.Errorf("chord %q: empty component", spec)
		}
		if seen[name] {
			return Chord{}, fmt.Errorf("chord %q: %q appears more than once", spec, name)
		}
		seen[name] = true

		if codes, ok := modifierAliases[name]; ok {
			chord.parts = append(chord.parts, chordPart{name: name, codes: codes})
			continue
		}
		if code, ok := literalKeys[name]; ok {
			chord.parts = append(chord.parts, chordPart{name: name, codes: []KeyCode{code}})
			continue
		}
		if code, ok := letterKey(name); ok {
			chord.parts = append(chord.parts, chordPart{name: name, codes: []KeyCode{code}})
			continue
		}
		return Chord{}, fmt.Errorf("chord %q: unknown key %q; accepted names: %s", spec, name, knownKeyNames())
	}

	return chord, nil
}

// Keys returns every key code the chord can involve, so a device can be
// checked for the capability to report them.
func (c Chord) Keys() []KeyCode {
	var codes []KeyCode
	for _, part := range c.parts {
		codes = append(codes, part.codes...)
	}
	return codes
}

// Empty reports whether the chord has no parts.
func (c Chord) Empty() bool { return len(c.parts) == 0 }

// String renders the chord as it was parsed.
func (c Chord) String() string {
	names := make([]string, 0, len(c.parts))
	for _, part := range c.parts {
		names = append(names, part.name)
	}
	return strings.Join(names, "+")
}

// tracks reports whether a key code participates in the chord.
func (c Chord) tracks(code KeyCode) bool {
	for _, part := range c.parts {
		for _, candidate := range part.codes {
			if candidate == code {
				return true
			}
		}
	}
	return false
}

// heldBy reports whether every part of the chord is satisfied.
func (c Chord) heldBy(held map[KeyCode]bool) bool {
	if len(c.parts) == 0 {
		return false
	}
	for _, part := range c.parts {
		if !part.satisfiedBy(held) {
			return false
		}
	}
	return true
}

// letterKey maps a single letter or digit to its evdev code.
func letterKey(name string) (KeyCode, bool) {
	if len([]rune(name)) != 1 {
		return 0, false
	}
	code, ok := singleKeyCodes[name]
	return code, ok
}

// singleKeyCodes holds the evdev codes for letters and digits, which have no
// arithmetic relationship to their characters on the Linux keyboard map.
var singleKeyCodes = map[string]KeyCode{
	"1": 2, "2": 3, "3": 4, "4": 5, "5": 6,
	"6": 7, "7": 8, "8": 9, "9": 10, "0": 11,
	"q": 16, "w": 17, "e": 18, "r": 19, "t": 20,
	"y": 21, "u": 22, "i": 23, "o": 24, "p": 25,
	"a": 30, "s": 31, "d": 32, "f": 33, "g": 34,
	"h": 35, "j": 36, "k": 37, "l": 38,
	"z": 44, "x": 45, "c": 46, "v": 47, "b": 48,
	"n": 49, "m": 50,
}

// knownKeyNames lists every accepted chord component for error messages.
func knownKeyNames() string {
	names := make([]string, 0, len(modifierAliases)+len(literalKeys)+len(singleKeyCodes))
	for name := range modifierAliases {
		names = append(names, name)
	}
	for name := range literalKeys {
		names = append(names, name)
	}
	for name := range singleKeyCodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
