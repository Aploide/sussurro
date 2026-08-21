//go:build windows

package ui

import (
	"log/slog"
	"runtime"
	"sync"
	"time"
	"unsafe"

	ihk "github.com/aploide/sussurro/internal/hotkey"
	"golang.org/x/sys/windows"
)

// The UI-mode hotkey uses a hand-rolled RegisterHotKey message loop instead of
// golang.design/x/hotkey's Windows backend: that backend busy-polls for key
// release while keyboard autorepeat keeps queueing WM_HOTKEY messages, which
// then replay as phantom down/up pairs after release. Draining the queue after
// each release avoids that. RegisterHotKey is preferred over a WH_KEYBOARD_LL
// hook because it still fires while an elevated window has focus and cannot be
// silently removed by the low-level-hook timeout.

var (
	hkUser32               = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey     = hkUser32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = hkUser32.NewProc("UnregisterHotKey")
	procGetMessageW        = hkUser32.NewProc("GetMessageW")
	procPeekMessageW       = hkUser32.NewProc("PeekMessageW")
	procPostThreadMessageW = hkUser32.NewProc("PostThreadMessageW")
	procGetAsyncKeyState   = hkUser32.NewProc("GetAsyncKeyState")
)

const (
	wmHotkey = 0x0312
	wmQuit   = 0x0012
	pmRemove = 0x0001
)

// winMsg mirrors the Win32 MSG struct (padded to its full x64 size).
type winMsg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
	_       uint32
}

type winHotkeyLoop struct {
	threadID uint32
	stopped  chan struct{}
}

// activeHK tracks the currently registered hotkey loop so it can be replaced
// when the user changes the trigger in the settings window.
var (
	activeHK   *winHotkeyLoop
	activeHKMu sync.Mutex
)

// installOverlayHotkey registers the global hotkey on Windows. The overlay
// parameter is ignored (as on macOS): the hotkey lives on its own locked OS
// thread and is independent of the overlay window, which does not exist yet
// when Manager.InstallHotkey runs.
func installOneHotkey(_ Overlay, trigger string, onDown, onUp func()) {
	mods, key, err := ihk.ParseTrigger(trigger)
	if err != nil {
		slog.Error("invalid hotkey trigger", "trigger", trigger, "error", err)
		return
	}

	// On Windows hotkey.Modifier values are the native MOD_* bits and
	// hotkey.Key values are virtual-key codes, so they feed RegisterHotKey
	// directly.
	var modBits uintptr
	for _, m := range mods {
		modBits |= uintptr(m)
	}
	vk := uintptr(key)

	loop := &winHotkeyLoop{stopped: make(chan struct{})}
	ready := make(chan bool, 1)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(loop.stopped)

		loop.threadID = windows.GetCurrentThreadId()

		if r, _, _ := procRegisterHotKey.Call(0, 1, modBits, vk); r == 0 {
			slog.Error("RegisterHotKey failed; the combination may be reserved by another application", "trigger", trigger)
			ready <- false
			return
		}
		defer procUnregisterHotKey.Call(0, 1)
		ready <- true

		var msg winMsg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(r) <= 0 { // WM_QUIT or error
				return
			}
			if msg.Message != wmHotkey {
				continue
			}
			onDown()
			for {
				s, _, _ := procGetAsyncKeyState.Call(vk)
				if uint16(s)&0x8000 == 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			onUp()
			// Drain WM_HOTKEY messages queued by keyboard autorepeat while
			// the key was held, so they don't replay as phantom presses.
			for {
				r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, wmHotkey, wmHotkey, pmRemove)
				if r == 0 {
					break
				}
			}
		}
	}()

	if !<-ready {
		return
	}

	activeHKMu.Lock()
	activeHK = loop
	activeHKMu.Unlock()
}

// reinstallOverlayHotkey stops the current hotkey loop and registers a new one
// with the given trigger, reusing the same onDown/onUp callbacks.
func reinstallOneHotkey(_ Overlay, trigger string, onDown, onUp func()) {
	activeHKMu.Lock()
	old := activeHK
	activeHK = nil
	activeHKMu.Unlock()

	if old != nil {
		procPostThreadMessageW.Call(uintptr(old.threadID), wmQuit, 0, 0)
		select {
		case <-old.stopped:
		case <-time.After(time.Second):
		}
	}

	installOverlayHotkey(nil, trigger, onDown, onUp)
}

// installOverlayContextMenu wires the right-click menu on the Win32 overlay.
func installOverlayContextMenu(overlay Overlay, openSettings, quit func()) {
	if wo, ok := overlay.(*windowsOverlay); ok {
		wo.installContextMenu(openSettings, quit)
	}
}

// installOverlayHotkey registers each configured binding. Push-to-talk and
// toggle are independent and either may be unset, so a user can hold one key
// and tap another — the previous single-trigger design allowed only one.
func installOverlayHotkey(overlay Overlay, bindings HotkeyBindings) {
	if bindings.PushToTalk != "" {
		installOneHotkey(overlay, bindings.PushToTalk, bindings.OnPress, bindings.OnRelease)
	}
	if bindings.Toggle != "" {
		// A toggle acts on press; the release carries no meaning.
		installOneHotkey(overlay, bindings.Toggle, bindings.OnToggle, func() {})
	}
}

// reinstallOverlayHotkey re-registers both bindings with new triggers.
func reinstallOverlayHotkey(overlay Overlay, bindings HotkeyBindings) {
	installOverlayHotkey(overlay, bindings)
}
