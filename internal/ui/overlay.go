package ui

// Overlay is the platform-independent interface for the capsule overlay window.
type Overlay interface {
	Show()
	Hide()
	SetState(state AppState)
	PushRMS(rms float32)
	Close()
}

// Presenter is the optional review-mode extension to Overlay. Platforms that
// can render transcript text implement it; those that cannot keep working
// through SetState alone, so review mode degrades to the capsule rather than
// failing to build.
type Presenter interface {
	// Present renders an immutable view model. Implementations must not
	// take input focus: the user is dictating into another window.
	Present(model ViewModel)
}

// present renders model on overlay, using the review-aware path when the
// platform provides one and falling back to the capsule state otherwise.
//
// Visibility is driven here rather than by the platform overlays, so all three
// share one policy. The state is set before showing so the first painted frame
// is already correct.
//
// trayReady releases the overlay to hide when idle. Until the tray appears,
// the capsule's right-click menu is the only documented route to Settings and
// Quit, so it stays on screen regardless of state.
func present(overlay Overlay, model ViewModel, trayReady bool) {
	if presenter, ok := overlay.(Presenter); ok {
		presenter.Present(model)
	} else {
		overlay.SetState(model.State)
	}

	if model.Visible() || !trayReady {
		overlay.Show()
		return
	}
	overlay.Hide()
}

// HotkeyBindings describes the keyboard bindings that start and stop
// recording. Each binding is optional: a user may have push-to-talk, toggle,
// or both, which the previous single-trigger-plus-mode design could not
// express.
type HotkeyBindings struct {
	PushToTalk string
	Toggle     string

	// OnPress and OnRelease drive the push-to-talk binding; OnToggle fires
	// once per press of the toggle binding.
	OnPress   func()
	OnRelease func()
	OnToggle  func()
}
