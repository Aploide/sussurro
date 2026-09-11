//go:build darwin

package ui

/*
#cgo CFLAGS: -x objective-c -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Cocoa -framework QuartzCore -framework CoreVideo
#include <stdlib.h>
#include "overlay_state.h"
#include "overlay_palette.h"

extern void* overlay_create_macos(const OverlayPalette *dark_palette,
                                  const OverlayPalette *light_palette);
extern void  overlay_set_state_macos(int state);
extern void  overlay_present_macos(int state, const char *text, const char *status,
                                   int provisional, int copied, int finalizing);
extern void  overlay_push_rms_macos(float rms);
extern void  overlay_push_fill_macos(double fill);
extern void  overlay_set_theme_macos(int mode,
                                     const OverlayPalette *dark_palette,
                                     const OverlayPalette *light_palette);
extern void  overlay_show_macos(void);
extern void  overlay_hide_macos(void);
extern void  overlay_set_context_menu_callbacks_macos(void);
extern void  overlay_terminate_macos(void);
*/
import "C"

import (
	"unsafe"

	"github.com/aploide/sussurro/internal/config"
)

var (
	contextMenuOpenSettings func()
	contextMenuQuit         func()
)

//export overlayGoOpenSettings
func overlayGoOpenSettings() {
	if contextMenuOpenSettings != nil {
		contextMenuOpenSettings()
	}
}

//export overlayGoQuit
func overlayGoQuit() {
	if contextMenuQuit != nil {
		contextMenuQuit()
	}
}

// overlaySetContextMenuCallbacks stores the Go callbacks and signals ObjC that
// right-click context menu is active.
func overlaySetContextMenuCallbacks(openSettings, quit func()) {
	contextMenuOpenSettings = openSettings
	contextMenuQuit = quit
	C.overlay_set_context_menu_callbacks_macos()
}

type darwinOverlay struct{}

func newOverlay() Overlay {
	dark := nativeOverlayPalette(darwinDarkOverlayPalette)
	light := nativeOverlayPalette(lightOverlayPalette)
	C.overlay_create_macos(&dark, &light)
	return &darwinOverlay{}
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
		finalizing:   nativeOverlayColor(palette.Finalizing),
		track:        nativeOverlayColor(palette.Track),
		fill:         nativeOverlayColor(palette.Fill),
		warning:      nativeOverlayColor(palette.Warning),
		shimmer_base: nativeOverlayColor(palette.ShimmerBase),
		shimmer_peak: nativeOverlayColor(palette.ShimmerPeak),
	}
}

func (o *darwinOverlay) SetTheme(theme config.Theme) {
	dark := nativeOverlayPalette(darwinDarkOverlayPalette)
	light := nativeOverlayPalette(lightOverlayPalette)
	C.overlay_set_theme_macos(C.int(overlayThemeMode(theme)), &dark, &light)
}

func (o *darwinOverlay) Show() {
	C.overlay_show_macos()
}

func (o *darwinOverlay) Hide() {
	C.overlay_hide_macos()
}

func (o *darwinOverlay) SetState(state AppState) {
	nativeState, ok := nativeOverlayState(state)
	if !ok {
		return
	}
	C.overlay_set_state_macos(nativeState)
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

// Present implements Presenter: it renders the review/streaming view model,
// showing live transcript text in the panel.
func (o *darwinOverlay) Present(model ViewModel) {
	nativeState, ok := nativeOverlayState(model.State)
	if !ok {
		return
	}

	ctext := C.CString(model.Transcript)
	defer C.free(unsafe.Pointer(ctext))
	cstatus := C.CString(model.Status)
	defer C.free(unsafe.Pointer(cstatus))

	// One call, not a SetState followed by a transcript update: separate
	// main-queue blocks let the run loop draw between them, which is how the
	// transcribing capsule appeared in place of text already on screen.
	C.overlay_present_macos(
		nativeState, ctext, cstatus,
		cBool(model.Partial), cBool(model.Copied), cBool(model.Finalizing),
	)
}

// cBool renders a Go bool as the C int the native layer takes.
func cBool(value bool) C.int {
	if value {
		return 1
	}
	return 0
}

func (o *darwinOverlay) PushRMS(rms float32) {
	C.overlay_push_rms_macos(C.float(rms))
}

// PushBufferFill implements FillIndicator.
func (o *darwinOverlay) PushBufferFill(fill float64) {
	C.overlay_push_fill_macos(C.double(fill))
}

func (o *darwinOverlay) Close() {
	o.Hide()
}

// platformExit stops the CVDisplayLink, hides the overlay, then calls _exit()
// to terminate without running C++ global destructors.  This avoids the
// whisper.cpp ggml-metal render-encoder assertion that fires when the normal
// C exit() path destroys Metal objects while they are still in use.
func platformExit() {
	C.overlay_terminate_macos()
}
