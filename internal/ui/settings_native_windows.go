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
	procIsWindowVisible     = snUser32.NewProc("IsWindowVisible")
	procSetForegroundWindow = snUser32.NewProc("SetForegroundWindow")
	procSetWindowLongPtrW   = snUser32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW     = snUser32.NewProc("CallWindowProcW")
)

const (
	swHide      = 0
	swShow      = 5
	wmClose     = 0x0010
	gwlpWndProc = ^uintptr(3) // GWLP_WNDPROC (-4) as uintptr
)

func showWebviewWindow(win unsafe.Pointer) {
	procShowWindow.Call(uintptr(win), swShow)
	procSetForegroundWindow.Call(uintptr(win))
}

func hideWebviewWindow(win unsafe.Pointer) {
	procShowWindow.Call(uintptr(win), swHide)
}

// webviewWindowVisible reports whether the settings window is currently shown.
// Asking Win32 rather than tracking a flag in Go keeps this correct when the
// window is hidden by the WM_CLOSE subclass below, which never calls
// hideWebviewWindow.
func webviewWindowVisible(win unsafe.Pointer) bool {
	r, _, _ := procIsWindowVisible.Call(uintptr(win))
	return r != 0
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

// workAreaSize returns zero so the caller keeps its built-in budget.
//
// Not yet implemented for Windows: SystemParametersInfo(SPI_GETWORKAREA)
// would supply it, but the clamp only bites on displays smaller than the
// content needs, and the built-in budget is already safe there.
func workAreaSize() (int, int) {
	return 0, 0
}
