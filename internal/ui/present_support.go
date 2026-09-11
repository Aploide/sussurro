package ui

// OverlayPresents reports whether this platform's overlay renders transcript
// text — that is, whether newOverlay returns a Presenter. Streaming partials
// and review text have nowhere to go on a platform whose overlay draws only
// the capsule, so callers can avoid producing them. It is answerable before
// Run() creates the overlay, which is when the pipeline has to be wired.
func OverlayPresents() bool { return overlayPresents }
