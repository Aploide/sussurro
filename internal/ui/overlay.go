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
func present(overlay Overlay, model ViewModel) {
	if presenter, ok := overlay.(Presenter); ok {
		presenter.Present(model)
		return
	}
	overlay.SetState(model.State)
}
