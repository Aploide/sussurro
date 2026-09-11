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

// The UI-mode hotkey uses RegisterHotKey directly. Each binding owns one
// locked OS thread, because hotkey IDs and WM_QUIT are scoped to that thread.
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
	stop     chan struct{}
	stopOnce sync.Once
	stopped  chan struct{}
}

var (
	activeWindowsHotkeys []*winHotkeyLoop
	activeWindowsMu      sync.Mutex
)

func startWindowsHotkey(trigger string, onDown, onUp func()) *winHotkeyLoop {
	mods, key, err := ihk.ParseTrigger(trigger)
	if err != nil {
		slog.Error("invalid hotkey trigger", "trigger", trigger, "error", err)
		return nil
	}

	var modBits uintptr
	for _, mod := range mods {
		modBits |= uintptr(mod)
	}
	vk := uintptr(key)
	loop := &winHotkeyLoop{stop: make(chan struct{}), stopped: make(chan struct{})}
	ready := make(chan bool, 1)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(loop.stopped)

		loop.threadID = windows.GetCurrentThreadId()
		// Force creation of this thread's message queue before publishing ready.
		// PostThreadMessage fails for a thread without one.
		var msg winMsg
		procPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 0)
		if result, _, _ := procRegisterHotKey.Call(0, 1, modBits, vk); result == 0 {
			slog.Error("RegisterHotKey failed; the combination may be reserved by another application", "trigger", trigger)
			ready <- false
			return
		}
		defer procUnregisterHotKey.Call(0, 1)
		ready <- true

		for {
			result, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(result) <= 0 {
				return
			}
			if msg.Message != wmHotkey {
				continue
			}
			onDown()
			for {
				state, _, _ := procGetAsyncKeyState.Call(vk)
				if uint16(state)&0x8000 == 0 {
					onUp()
					break
				}
				select {
				case <-loop.stop:
					onUp()
					return
				default:
					time.Sleep(10 * time.Millisecond)
				}
			}
			// Remove autorepeat messages queued while the key was held.
			for {
				result, _, _ := procPeekMessageW.Call(
					uintptr(unsafe.Pointer(&msg)), 0, wmHotkey, wmHotkey, pmRemove)
				if result == 0 {
					break
				}
			}
		}
	}()

	if !<-ready {
		return nil
	}
	return loop
}

func stopWindowsHotkey(loop *winHotkeyLoop) bool {
	if loop == nil {
		return true
	}
	loop.stopOnce.Do(func() { close(loop.stop) })

	// Wake GetMessage when the loop is not inside held-key polling. Retry a
	// failed post rather than forgetting a registration that still owns its
	// OS thread and hotkey ID.
	deadline := time.Now().Add(2 * time.Second)
	for {
		result, _, err := procPostThreadMessageW.Call(uintptr(loop.threadID), wmQuit, 0, 0)
		if result != 0 {
			break
		}
		select {
		case <-loop.stopped:
			return true
		default:
		}
		if time.Now().After(deadline) {
			slog.Error("PostThreadMessageW failed; retaining live hotkey ownership", "error", err)
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case <-loop.stopped:
		return true
	case <-time.After(2 * time.Second):
		slog.Error("hotkey thread did not stop; retaining live ownership")
		return false
	}
}

func replaceWindowsHotkeys(bindings HotkeyBindings) {
	activeWindowsMu.Lock()
	defer activeWindowsMu.Unlock()

	var stillActive []*winHotkeyLoop
	for _, loop := range activeWindowsHotkeys {
		if !stopWindowsHotkey(loop) {
			stillActive = append(stillActive, loop)
		}
	}
	if len(stillActive) > 0 {
		activeWindowsHotkeys = stillActive
		return
	}
	activeWindowsHotkeys = nil

	specs := []struct {
		trigger string
		onDown  func()
		onUp    func()
	}{
		{bindings.PushToTalk, bindings.OnPress, bindings.OnRelease},
		{bindings.Toggle, bindings.OnToggle, func() {}},
		{bindings.Edit, bindings.OnEditPress, bindings.OnEditRelease},
	}
	for _, spec := range specs {
		if spec.trigger == "" {
			continue
		}
		if loop := startWindowsHotkey(spec.trigger, spec.onDown, spec.onUp); loop != nil {
			activeWindowsHotkeys = append(activeWindowsHotkeys, loop)
		}
	}
}

// installOverlayContextMenu wires the right-click menu on the Win32 overlay.
func installOverlayContextMenu(overlay Overlay, openSettings, quit func()) {
	if wo, ok := overlay.(*windowsOverlay); ok {
		wo.installContextMenu(openSettings, quit)
	}
}

func installOverlayHotkey(_ Overlay, bindings HotkeyBindings) {
	replaceWindowsHotkeys(bindings)
}

// reinstallOverlayHotkey releases every old registration before installing
// the complete new set, including bindings that were cleared in Settings.
func reinstallOverlayHotkey(_ Overlay, bindings HotkeyBindings) {
	replaceWindowsHotkeys(bindings)
}
