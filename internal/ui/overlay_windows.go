//go:build windows

package ui

/*
#cgo LDFLAGS: -lgdiplus -lgdi32 -luser32 -ladvapi32
#include "overlay_windows.h"

// Forward-declare the Go-exported trampolines so C can call them.
extern void goOpenSettings(void);
extern void goQuit(void);

// Static helpers return function pointers for the trampolines.
static MenuOpenSettingsCB menuOpenSettingsCB(void) { return (MenuOpenSettingsCB)goOpenSettings; }
static MenuQuitCB         menuQuitCB(void)         { return (MenuQuitCB)goQuit;                 }
*/
import "C"
import (
	"unsafe"

	"github.com/aploide/sussurro/internal/config"
)

// windowsOverlay wraps the CGO Win32/GDI+ overlay window.
type windowsOverlay struct {
	hwnd unsafe.Pointer // HWND stored as unsafe.Pointer
}

// Singleton callbacks — only one overlay per process.
var (
	globalOpenSettingsCB func()
	globalQuitCB         func()
)

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

// newOverlay creates the layered Win32 overlay window on the calling thread
// (the main thread — its messages are pumped by the webview loop that
// Manager.Run enters afterwards).
func newOverlay() Overlay {
	dark := nativeOverlayPalette(windowsDarkOverlayPalette)
	light := nativeOverlayPalette(lightOverlayPalette)
	return &windowsOverlay{hwnd: C.overlay_create(&dark, &light)}
}

func nativeOverlayColor(color overlayColor) C.OverlayColor {
	return C.OverlayColor{r: C.double(color.R), g: C.double(color.G), b: C.double(color.B), a: C.double(color.A)}
}

func nativeOverlayPalette(palette overlayPalette) C.OverlayPalette {
	return C.OverlayPalette{
		background:   nativeOverlayColor(palette.Background),
		border:       nativeOverlayColor(palette.Border),
		primary:      nativeOverlayColor(palette.Primary),
		secondary:    nativeOverlayColor(palette.Secondary),
		provisional:  nativeOverlayColor(palette.Provisional),
		copied:       nativeOverlayColor(palette.Copied),
		track:        nativeOverlayColor(palette.Track),
		fill:         nativeOverlayColor(palette.Fill),
		warning:      nativeOverlayColor(palette.Warning),
		shimmer_base: nativeOverlayColor(palette.ShimmerBase),
		shimmer_peak: nativeOverlayColor(palette.ShimmerPeak),
	}
}

func (o *windowsOverlay) SetTheme(theme config.Theme) {
	dark := nativeOverlayPalette(windowsDarkOverlayPalette)
	light := nativeOverlayPalette(lightOverlayPalette)
	C.overlay_set_theme_async(o.hwnd, C.int(overlayThemeMode(theme)), &dark, &light)
}

func (o *windowsOverlay) Show() {
	C.overlay_show(o.hwnd)
}

func (o *windowsOverlay) Hide() {
	C.overlay_hide(o.hwnd)
}

func (o *windowsOverlay) SetState(state AppState) {
	nativeState, ok := nativeOverlayState(state)
	if !ok {
		return
	}
	C.overlay_set_state_async(o.hwnd, nativeState)
}

func nativeOverlayState(state AppState) (C.int, bool) {
	switch state {
	case StateIdle:
		return C.OVERLAY_STATE_IDLE, true
	case StateRecording:
		return C.OVERLAY_STATE_RECORDING, true
	case StateTranscribing:
		return C.OVERLAY_STATE_TRANSCRIBING, true
	case StateCleaningUp:
		// Shares the transcribing shimmer, but the native layer draws its own
		// label, so it must receive its own state rather than being folded
		// into transcribing.
		return C.OVERLAY_STATE_CLEANING_UP, true
	default:
		return 0, false
	}
}

func (o *windowsOverlay) PushRMS(rms float32) {
	C.overlay_push_rms_async(o.hwnd, C.float(rms))
}

func (o *windowsOverlay) Close() {
	o.Hide()
}

// installContextMenu wires the right-click popup on the overlay window.
func (o *windowsOverlay) installContextMenu(openSettings, quit func()) {
	globalOpenSettingsCB = openSettings
	globalQuitCB = quit
	C.overlay_install_context_menu(o.hwnd, C.menuOpenSettingsCB(), C.menuQuitCB())
}
