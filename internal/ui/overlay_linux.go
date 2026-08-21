//go:build linux

package ui

/*
#cgo pkg-config: gtk+-3.0
#cgo LDFLAGS: -lm
#include <stdlib.h>
#include "overlay_linux.h"

// Forward-declare the Go-exported trampolines so C can call them.
extern void goHotkeyDown(void);
extern void goHotkeyToggle(void);
extern void goHotkeyUp(void);
extern void goOpenSettings(void);
extern void goQuit(void);

// Static helpers return function pointers for the trampolines.
static HotkeyDownCB      hotkeyDownCB(void)      { return (HotkeyDownCB)goHotkeyDown;           }
static HotkeyUpCB        hotkeyUpCB(void)        { return (HotkeyUpCB)goHotkeyUp;               }
static HotkeyDownCB      hotkeyToggleCB(void)    { return (HotkeyDownCB)goHotkeyToggle;         }
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

//export goHotkeyToggle
func goHotkeyToggle() {
	if globalToggleCB != nil {
		globalToggleCB()
	}
}

//export goHotkeyUp
func goHotkeyUp() {
	if globalUpCB != nil {
		globalUpCB()
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

// installHotkey registers the X11 global bindings (no-op on Wayland). Either
// binding may be empty, so a user can have push-to-talk, toggle, or both.
func (o *linuxOverlay) installHotkey(bindings HotkeyBindings) {
	globalDownCB = bindings.OnPress
	globalUpCB = bindings.OnRelease
	globalToggleCB = bindings.OnToggle

	cptt := C.CString(bindings.PushToTalk)
	defer C.free(unsafe.Pointer(cptt))
	ctoggle := C.CString(bindings.Toggle)
	defer C.free(unsafe.Pointer(ctoggle))

	C.overlay_install_hotkey(
		(*C.GtkWidget)(o.win),
		cptt,
		ctoggle,
		C.hotkeyDownCB(),
		C.hotkeyUpCB(),
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
	nativeState, ok := nativeOverlayState(state)
	if !ok {
		return
	}
	C.overlay_set_state_async((*C.GtkWidget)(o.win), nativeState)
}

func nativeOverlayState(state AppState) (C.int, bool) {
	switch state {
	case StateIdle:
		return C.OVERLAY_STATE_IDLE, true
	case StateRecording:
		return C.OVERLAY_STATE_RECORDING, true
	case StateTranscribing:
		return C.OVERLAY_STATE_TRANSCRIBING, true
	default:
		return 0, false
	}
}

// Present implements Presenter: it renders the review/streaming view model,
// showing live transcript text in the expanded panel.
func (o *linuxOverlay) Present(model ViewModel) {
	nativeState, ok := nativeOverlayState(model.State)
	if !ok {
		return
	}

	ctext := C.CString(model.Transcript)
	defer C.free(unsafe.Pointer(ctext))
	cstatus := C.CString(model.Status)
	defer C.free(unsafe.Pointer(cstatus))

	provisional := C.int(0)
	if model.Partial {
		provisional = 1
	}

	// One call, not a SetState followed by a transcript update: separate
	// idle callbacks let the GTK loop draw between them, which is how the
	// transcribing capsule appeared in place of text already on screen.
	C.overlay_present_async((*C.GtkWidget)(o.win), nativeState, ctext, cstatus, provisional)
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
