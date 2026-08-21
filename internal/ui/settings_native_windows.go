//go:build windows

package ui

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	snUser32                = windows.NewLazySystemDLL("user32.dll")
	procShowWindow          = snUser32.NewProc("ShowWindow")
	procSetForegroundWindow = snUser32.NewProc("SetForegroundWindow")
	procSetWindowLongPtrW   = snUser32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW     = snUser32.NewProc("CallWindowProcW")
)

const (
	swHide       = 0
	swShow       = 5
	wmClose      = 0x0010
	gwlpWndProc  = ^uintptr(3) // GWLP_WNDPROC (-4) as uintptr
)

func showWebviewWindow(win unsafe.Pointer) {
	procShowWindow.Call(uintptr(win), swShow)
	procSetForegroundWindow.Call(uintptr(win))
}

func hideWebviewWindow(win unsafe.Pointer) {
	procShowWindow.Call(uintptr(win), swHide)
}

// prevSettingsProc holds the webview window's original WndProc so the
// subclass can forward everything except WM_CLOSE.
var prevSettingsProc uintptr

// interceptSettingsClose subclasses the webview HWND so the titlebar close
// button hides the window instead of destroying it (the Win32 analogue of the
// GTK delete-event handler). Must be called from the thread that owns the
// window, which newSettingsWindow guarantees.
func interceptSettingsClose(win unsafe.Pointer) {
	cb := syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
		if msg == wmClose {
			procShowWindow.Call(hwnd, swHide)
			return 0
		}
		r, _, _ := procCallWindowProcW.Call(prevSettingsProc, hwnd, msg, wparam, lparam)
		return r
	})
	prevSettingsProc, _, _ = procSetWindowLongPtrW.Call(uintptr(win), gwlpWndProc, cb)
}

// windowScale reports the display's content scaling factor.
//
// Windows applies DPI scaling to window geometry itself when the process is
// DPI-aware, so content and window sizes already agree and no correction is
// needed. Returning 1 keeps the cross-platform caller simple.
func windowScale() float64 {
	return 1.0
}
