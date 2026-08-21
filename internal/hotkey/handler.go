package hotkey

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"golang.design/x/hotkey"
)

// Handler manages global hotkeys
type Handler struct {
	hk   *hotkey.Hotkey
	log  *slog.Logger
	done chan struct{}

	onKeyDown func()
	onKeyUp   func()
}

// NewHandler creates a new hotkey handler
func NewHandler(trigger string, log *slog.Logger) (*Handler, error) {
	// Check if we're on Wayland
	if IsWayland() {
		log.Error("Wayland detected: Global hotkeys are not supported")
		log.Error("Solution 1: Log out and select an X11 session instead")
		log.Error("Solution 2: Configure your desktop environment to bind Ctrl+Shift+Space to trigger recording")
		return nil, fmt.Errorf("global hotkeys require X11 - Wayland does not support them")
	}

	mods, key, err := parseTrigger(trigger)
	if err != nil {
		return nil, err
	}

	hk := hotkey.New(mods, key)

	return &Handler{
		hk:   hk,
		log:  log,
		done: make(chan struct{}),
	}, nil
}

// Register registers the hotkey and starts listening
func (h *Handler) Register(onKeyDown, onKeyUp func()) error {
	h.onKeyDown = onKeyDown
	h.onKeyUp = onKeyUp

	if err := h.hk.Register(); err != nil {
		return fmt.Errorf("failed to register hotkey: %w", err)
	}

	// Start listening loop in a goroutine
	go h.listen()

	return nil
}

// Unregister unregisters the hotkey and stops the listener
func (h *Handler) Unregister() {
	close(h.done)
	h.hk.Unregister()
}

func (h *Handler) listen() {
	for {
		select {
		case <-h.done:
			h.log.Debug("Hotkey listener stopping")
			return
		case <-h.hk.Keydown():
			h.log.Debug("Hotkey pressed")
			if h.onKeyDown != nil {
				h.onKeyDown()
			}
		case <-h.hk.Keyup():
			h.log.Debug("Hotkey released")
			if h.onKeyUp != nil {
				h.onKeyUp()
			}
		}
	}
}

// ParseTrigger parses a string like "ctrl+shift+space" into modifiers and key.
// Exported so platform-specific UI code can reuse it without duplicating the mapping.
func ParseTrigger(trigger string) ([]hotkey.Modifier, hotkey.Key, error) {
	return parseTrigger(trigger)
}

// parseTrigger parses a string like "ctrl+shift+space" into modifiers and key
func parseTrigger(trigger string) ([]hotkey.Modifier, hotkey.Key, error) {
	parts := strings.Split(strings.ToLower(trigger), "+")
	if len(parts) == 0 {
		return nil, 0, fmt.Errorf("empty hotkey trigger")
	}

	var mods []hotkey.Modifier
	var key hotkey.Key

	// Map strings to hotkey constants
	// Note: specific mapping depends on golang.design/x/hotkey definitions
	// We'll implement a basic mapping here

	for i, part := range parts {
		// Last part is the key
		if i == len(parts)-1 {
			k, ok := keyMap[part]
			if !ok {
				return nil, 0, fmt.Errorf("hotkey %q: unknown key %q; accepted: %s",
					trigger, part, knownKeyNames())
			}
			key = k
			continue
		}

		// Modifiers
		mod, err := parseModifier(part)
		if err != nil {
			return nil, 0, err
		}
		mods = append(mods, mod)
	}

	return mods, key, nil
}

// Basic key map - expand as needed
var keyMap = map[string]hotkey.Key{
	"space": hotkey.KeySpace,
	"enter": hotkey.KeyReturn,
	"tab":   hotkey.KeyTab,
	"esc":   hotkey.KeyEscape,
	// Digits were omitted, so a trigger like "super+7" failed to register and
	// the process exited. The overlay's own X11 grab accepted them, so this
	// only broke --no-ui.
	"0":   hotkey.Key0,
	"1":   hotkey.Key1,
	"2":   hotkey.Key2,
	"3":   hotkey.Key3,
	"4":   hotkey.Key4,
	"5":   hotkey.Key5,
	"6":   hotkey.Key6,
	"7":   hotkey.Key7,
	"8":   hotkey.Key8,
	"9":   hotkey.Key9,
	"f1":  hotkey.KeyF1,
	"f2":  hotkey.KeyF2,
	"f3":  hotkey.KeyF3,
	"f4":  hotkey.KeyF4,
	"f5":  hotkey.KeyF5,
	"f6":  hotkey.KeyF6,
	"f7":  hotkey.KeyF7,
	"f8":  hotkey.KeyF8,
	"f9":  hotkey.KeyF9,
	"f10": hotkey.KeyF10,
	"f11": hotkey.KeyF11,
	"f12": hotkey.KeyF12,
	"a":   hotkey.KeyA,
	"b":   hotkey.KeyB,
	"c":   hotkey.KeyC,
	"d":   hotkey.KeyD,
	"e":   hotkey.KeyE,
	"f":   hotkey.KeyF,
	"g":   hotkey.KeyG,
	"h":   hotkey.KeyH,
	"i":   hotkey.KeyI,
	"j":   hotkey.KeyJ,
	"k":   hotkey.KeyK,
	"l":   hotkey.KeyL,
	"m":   hotkey.KeyM,
	"n":   hotkey.KeyN,
	"o":   hotkey.KeyO,
	"p":   hotkey.KeyP,
	"q":   hotkey.KeyQ,
	"r":   hotkey.KeyR,
	"s":   hotkey.KeyS,
	"t":   hotkey.KeyT,
	"u":   hotkey.KeyU,
	"v":   hotkey.KeyV,
	"w":   hotkey.KeyW,
	"x":   hotkey.KeyX,
	"y":   hotkey.KeyY,
	"z":   hotkey.KeyZ,
}

// knownKeyNames lists the accepted key names, so a rejected trigger says what
// would work instead of leaving the user guessing.
func knownKeyNames() string {
	names := make([]string, 0, len(keyMap))
	for name := range keyMap {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
