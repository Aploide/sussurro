//go:build linux

package ui

/*
#cgo pkg-config: gtk+-3.0
#cgo LDFLAGS: -lm
#include <stdlib.h>
#include "overlay_linux.h"

// Forward-declare the Go-exported trampolines so C can call them.
extern void goHotkeyDown(void);
extern void goHotkeyUp(void);
extern void goHotkeyToggle(void);
extern void goOpenSettings(void);
extern void goQuit(void);

// Static helpers return function pointers for the trampolines.
static HotkeyDownCB      hotkeyDownCB(void)      { return (HotkeyDownCB)goHotkeyDown;           }
static HotkeyUpCB        hotkeyUpCB(void)        { return (HotkeyUpCB)goHotkeyUp;               }
static HotkeyToggleCB    hotkeyToggleCB(void)    { return (HotkeyToggleCB)goHotkeyToggle;       }
static MenuOpenSettingsCB menuOpenSettingsCB(void) { return (MenuOpenSettingsCB)goOpenSettings;   }
static MenuQuitCB         menuQuitCB(void)         { return (MenuQuitCB)goQuit;                   }
*/
import "C"
import (
	"os"
	"unsafe"
)

// linuxOverlay wraps the CGO GTK3 overlay window.
type linuxOverlay struct {
	win unsafe.Pointer // *C.GtkWidget stored as unsafe.Pointer
}

// Singleton callbacks — only one overlay per process.
var (
	globalDownCB         func()
	globalUpCB           func()
	globalToggleCB       func()
	globalOpenSettingsCB func()
	globalQuitCB         func()
)

//export goHotkeyDown
func goHotkeyDown() {
	if globalDownCB != nil {
		globalDownCB()
	}
}

//export goHotkeyUp
func goHotkeyUp() {
	if globalUpCB != nil {
		globalUpCB()
	}
}

//export goHotkeyToggle
func goHotkeyToggle() {
	if globalToggleCB != nil {
		globalToggleCB()
	}
}

//export goOpenSettings
func goOpenSettings() {
	if globalOpenSettingsCB != nil {
		globalOpenSettingsCB()
	}
}

//export goQuit
func goQuit() {
	if globalQuitCB != nil {
		globalQuitCB()
	}
}

// newOverlay creates the GTK3 overlay window.
// Calls gtk_init() first so both the overlay and the subsequent webview share
// the same GTK context. Must be called from the main goroutine before webview.Run().
func newOverlay() Overlay {
	// Force X11 backend: the overlay uses override_redirect and XGrabKey which
	// are X11-only. Without this, GTK picks Wayland when WAYLAND_DISPLAY is set
	// (e.g. Konsole), making both features no-ops and breaking the overlay.
	// gtk-layer-shell is not available so we always run via XWayland anyway.
	os.Setenv("GDK_BACKEND", "x11") //nolint:errcheck

	// gtk_init(NULL, NULL) — idempotent if already initialised by webview.
	C.gtk_init(nil, nil)
	win := C.overlay_create()
	return &linuxOverlay{win: unsafe.Pointer(win)}
}

// installHotkey registers an X11 global hotkey (no-op on Wayland).
func (o *linuxOverlay) installHotkey(trigger string, onDown, onUp func()) {
	globalDownCB = onDown
	globalUpCB = onUp
	ctrig := C.CString(trigger)
	defer C.free(unsafe.Pointer(ctrig))
	C.overlay_install_hotkey(
		(*C.GtkWidget)(o.win),
		ctrig,
		C.hotkeyDownCB(),
		C.hotkeyUpCB(),
	)
}

func (o *linuxOverlay) installToggleHotkey(trigger string, onTap func()) {
	globalToggleCB = onTap
	ctrig := C.CString(trigger)
	defer C.free(unsafe.Pointer(ctrig))
	C.overlay_install_toggle_hotkey(
		(*C.GtkWidget)(o.win),
		ctrig,
		C.hotkeyToggleCB(),
	)
}

func (o *linuxOverlay) Show() {
	C.overlay_show((*C.GtkWidget)(o.win))
}

func (o *linuxOverlay) Hide() {
	C.overlay_hide((*C.GtkWidget)(o.win))
}

func (o *linuxOverlay) SetState(state AppState) {
	C.overlay_set_state_async((*C.GtkWidget)(o.win), C.int(state))
}

func (o *linuxOverlay) PushRMS(rms float32) {
	C.overlay_push_rms_async((*C.GtkWidget)(o.win), C.float(rms))
}

func (o *linuxOverlay) Close() {
	o.Hide()
}

// installContextMenu wires the right-click popup on the overlay window.
func (o *linuxOverlay) installContextMenu(openSettings, quit func()) {
	globalOpenSettingsCB = openSettings
	globalQuitCB = quit
	C.overlay_install_context_menu(
		(*C.GtkWidget)(o.win),
		C.menuOpenSettingsCB(),
		C.menuQuitCB(),
	)
}
