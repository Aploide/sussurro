//go:build darwin

package ui

import (
	"sync"
	"time"

	ihk "github.com/cesp99/sussurro/internal/hotkey"
	xhotkey "golang.design/x/hotkey"
)

// activeHK tracks the currently registered hotkey so it can be unregistered
// when the user changes the trigger in the settings window.
var (
	activeHK      *xhotkey.Hotkey
	activeHKStop  chan struct{}
	activeTapHK   *xhotkey.Hotkey
	activeTapStop chan struct{}
	activeHKMu    sync.Mutex
)

// installOverlayHotkey registers the global hotkey on macOS.
// golang.design/x/hotkey on darwin drives its own CFRunLoop thread, so it can
// be called from a regular goroutine once [NSApp run] is live.  We wait
// briefly to guarantee NSApp has started before registering the CGEventTap.
func installOverlayHotkey(overlay Overlay, trigger string, onDown, onUp func()) {
	mods, key, err := ihk.ParseTrigger(trigger)
	if err != nil {
		return
	}

	stop := make(chan struct{})

	go func() {
		// Give [NSApp run] time to initialise before attaching the event tap.
		time.Sleep(300 * time.Millisecond)

		hk := xhotkey.New(mods, key)
		if err := hk.Register(); err != nil {
			return
		}

		activeHKMu.Lock()
		activeHK = hk
		activeHKStop = stop
		activeHKMu.Unlock()

		for {
			select {
			case <-stop:
				return
			case <-hk.Keydown():
				onDown()
			case <-hk.Keyup():
				onUp()
			}
		}
	}()
}

func installOverlayToggleHotkey(_ Overlay, trigger string, onTap func()) {
	mods, key, err := ihk.ParseTrigger(trigger)
	if err != nil {
		return
	}

	stop := make(chan struct{})

	go func() {
		time.Sleep(300 * time.Millisecond)

		hk := xhotkey.New(mods, key)
		if err := hk.Register(); err != nil {
			return
		}

		activeHKMu.Lock()
		activeTapHK = hk
		activeTapStop = stop
		activeHKMu.Unlock()

		for {
			select {
			case <-stop:
				return
			case <-hk.Keydown():
				onTap()
			}
		}
	}()
}

// reinstallOverlayHotkey unregisters the current hotkey and registers a new
// one with the given trigger, reusing the same onDown/onUp callbacks.
func reinstallOverlayHotkey(_ Overlay, trigger string, onDown, onUp func()) {
	// Grab and clear the existing handle under the lock.
	activeHKMu.Lock()
	old := activeHK
	oldStop := activeHKStop
	activeHK = nil
	activeHKStop = nil
	activeHKMu.Unlock()

	if old != nil {
		old.Unregister()
	}
	if oldStop != nil {
		close(oldStop)
	}
	mods, key, err := ihk.ParseTrigger(trigger)
	if err != nil {
		return
	}

	// Brief pause so the OS releases the CGEventTap key grab before we
	// create a new one for the same (or overlapping) modifier set.
	time.Sleep(100 * time.Millisecond)

	stop := make(chan struct{})
	hk := xhotkey.New(mods, key)
	if err := hk.Register(); err != nil {
		return
	}

	activeHKMu.Lock()
	activeHK = hk
	activeHKStop = stop
	activeHKMu.Unlock()

	go func() {
		for {
			select {
			case <-stop:
				return
			case <-hk.Keydown():
				onDown()
			case <-hk.Keyup():
				onUp()
			}
		}
	}()
}

// installOverlayContextMenu wires right-click callbacks into the NSPanel overlay.
func installOverlayContextMenu(overlay Overlay, openSettings, quit func()) {
	overlaySetContextMenuCallbacks(openSettings, quit)
}
