//go:build darwin

package ui

import (
	"sync"
	"time"

	ihk "github.com/aploide/sussurro/internal/hotkey"
	xhotkey "golang.design/x/hotkey"
)

// macHotkeySet owns every live registration. Replacing the settings swaps the
// whole set, which prevents old delayed registrations from appearing after a
// newer save and ensures all previous event taps are released.
type macHotkeySet struct {
	stop    chan struct{}
	hotkeys []*xhotkey.Hotkey
}

var (
	activeMacHotkeys *macHotkeySet
	activeMacMu      sync.Mutex
)

type macHotkeySpec struct {
	trigger string
	onDown  func()
	onUp    func()
}

func hotkeySpecs(bindings HotkeyBindings) []macHotkeySpec {
	return []macHotkeySpec{
		{bindings.PushToTalk, bindings.OnPress, bindings.OnRelease},
		{bindings.Toggle, bindings.OnToggle, func() {}},
		{bindings.Edit, bindings.OnEditPress, bindings.OnEditRelease},
	}
}

func replaceMacHotkeys(bindings HotkeyBindings, delay time.Duration) {
	set := &macHotkeySet{stop: make(chan struct{})}

	activeMacMu.Lock()
	old := activeMacHotkeys
	activeMacHotkeys = set
	activeMacMu.Unlock()

	if old != nil {
		close(old.stop)
		for _, hk := range old.hotkeys {
			hk.Unregister()
		}
	}

	go func() {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-set.stop:
				timer.Stop()
				return
			}
		}

		for _, spec := range hotkeySpecs(bindings) {
			if spec.trigger == "" {
				continue
			}
			mods, key, err := ihk.ParseTrigger(spec.trigger)
			if err != nil {
				continue
			}
			hk := xhotkey.New(mods, key)
			if err := hk.Register(); err != nil {
				continue
			}

			activeMacMu.Lock()
			if activeMacHotkeys != set {
				activeMacMu.Unlock()
				hk.Unregister()
				return
			}
			set.hotkeys = append(set.hotkeys, hk)
			activeMacMu.Unlock()

			go func(hk *xhotkey.Hotkey, onDown, onUp func()) {
				for {
					select {
					case <-set.stop:
						return
					case <-hk.Keydown():
						onDown()
					case <-hk.Keyup():
						onUp()
					}
				}
			}(hk, spec.onDown, spec.onUp)
		}
	}()
}

// installOverlayContextMenu wires right-click callbacks into the NSPanel overlay.
func installOverlayContextMenu(overlay Overlay, openSettings, quit func()) {
	overlaySetContextMenuCallbacks(openSettings, quit)
}

// installOverlayHotkey waits briefly for NSApp's run loop, then registers all
// configured bindings on their own CFRunLoop-backed hotkeys.
func installOverlayHotkey(_ Overlay, bindings HotkeyBindings) {
	replaceMacHotkeys(bindings, 300*time.Millisecond)
}

// reinstallOverlayHotkey releases the complete old set before installing the
// current settings. It is called after NSApp is already running.
func reinstallOverlayHotkey(_ Overlay, bindings HotkeyBindings) {
	replaceMacHotkeys(bindings, 100*time.Millisecond)
}
