//go:build darwin

package ui

/*
#cgo CFLAGS: -x objective-c -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Cocoa -framework QuartzCore -framework CoreVideo
#include "overlay_state.h"

extern void* overlay_create_macos(void);
extern void  overlay_set_state_macos(int state);
extern void  overlay_push_rms_macos(float rms);
extern void  overlay_show_macos(void);
extern void  overlay_hide_macos(void);
extern void  overlay_set_context_menu_callbacks_macos(void);
extern void  overlay_terminate_macos(void);
*/
import "C"

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
	C.overlay_create_macos()
	return &darwinOverlay{}
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
	case StateTranscribing, StateCleaningUp:
		// Both are work-in-progress after speech; the shimmer is shared and
		// the label distinguishes them.
		return C.OVERLAY_STATE_TRANSCRIBING, true
	default:
		return 0, false
	}
}

func (o *darwinOverlay) PushRMS(rms float32) {
	C.overlay_push_rms_macos(C.float(rms))
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
