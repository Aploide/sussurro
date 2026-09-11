package ui

import (
	"runtime"
	"testing"

	"github.com/aploide/sussurro/internal/config"
)

// Whether review mode and live transcription are offered at all comes down to
// a package-level type assertion on the platform's overlay type. A backend
// that stopped implementing Presenter would withdraw both features silently,
// with nothing failing to compile, so the expectation is pinned per platform.
func TestOverlayPresentsMatchesThePlatform(t *testing.T) {
	want := runtime.GOOS == "linux" || runtime.GOOS == "darwin"
	if got := OverlayPresents(); got != want {
		t.Errorf("OverlayPresents() = %v on %s, want %v", got, runtime.GOOS, want)
	}
}

// The settings model and the overlay have to agree on this host, not only on a
// synthetic probe: hostCapabilities is the single place the two are joined, and
// a review mode the overlay can serve but Settings refuses to offer is
// unreachable.
func TestReviewModeOfferedOnThisHostFollowsTheOverlay(t *testing.T) {
	probe := hostCapabilities()
	if probe.OverlayPresents != OverlayPresents() {
		t.Fatalf("hostCapabilities().OverlayPresents = %v, want it to follow OverlayPresents() = %v",
			probe.OverlayPresents, OverlayPresents())
	}

	mode := findChoice(t, buildWorkflowSettings(defaultConfig(), probe).Modes, string(config.ModeReview))
	if mode.Available != OverlayPresents() {
		t.Errorf("review mode available = %v on %s, want %v (reason %q)",
			mode.Available, runtime.GOOS, OverlayPresents(), mode.Reason)
	}
}
