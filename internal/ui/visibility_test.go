package ui

import (
	"sync"
	"testing"

	"github.com/aploide/sussurro/internal/session"
)

// visibilityOverlay records show/hide calls alongside the states it was given.
type visibilityOverlay struct {
	mu      sync.Mutex
	calls   []string
	visible bool
}

func (o *visibilityOverlay) Show() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, "show")
	o.visible = true
}

func (o *visibilityOverlay) Hide() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, "hide")
	o.visible = false
}

func (o *visibilityOverlay) SetState(AppState) {}
func (o *visibilityOverlay) PushRMS(float32)   {}
func (o *visibilityOverlay) Close()            {}

func (o *visibilityOverlay) shown() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.visible
}

func (o *visibilityOverlay) history() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.calls...)
}

func TestOverlayHiddenWhenIdle(t *testing.T) {
	// The capsule is feedback about work in progress, not a permanent fixture.
	if CompactModel(session.StateIdle).Visible() {
		t.Error("Visible() = true when idle, want the overlay hidden")
	}
}

func TestOverlayVisibleWhileWorking(t *testing.T) {
	for _, state := range []AppState{session.StateRecording, session.StateTranscribing} {
		t.Run(state.String(), func(t *testing.T) {
			if !CompactModel(state).Visible() {
				t.Errorf("Visible() = false while %s, want the overlay shown", state)
			}
		})
	}
}

func TestReviewVisibilityFollowsTheSession(t *testing.T) {
	tests := []struct {
		review session.ReviewState
		want   bool
	}{
		{review: session.ReviewIdle, want: false},
		{review: session.ReviewRecording, want: true},
		{review: session.ReviewFinalizing, want: true},
		// Ready is waiting on the user, so hiding it would strand a session
		// with no indication that held text exists.
		{review: session.ReviewReady, want: true},
		{review: session.ReviewEditing, want: true},
		{review: session.ReviewApplyingEdit, want: true},
		{review: session.ReviewDelivering, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.review.String(), func(t *testing.T) {
			model := ReviewModel(tt.review, "text", false)
			if got := model.Visible(); got != tt.want {
				t.Errorf("Visible() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReviewIdleHiddenEvenWithStaleText(t *testing.T) {
	// Returning to idle must not keep the capsule up just because the model
	// still carries the text that was delivered.
	model := ReviewModel(session.ReviewIdle, "delivered text", false)
	if model.Visible() {
		t.Error("Visible() = true for an idle review session, want hidden")
	}
}

func TestPresentShowsAndHidesWithState(t *testing.T) {
	overlay := &visibilityOverlay{}

	present(overlay, CompactModel(session.StateRecording), true)
	if !overlay.shown() {
		t.Fatal("overlay hidden while recording")
	}

	present(overlay, CompactModel(session.StateTranscribing), true)
	if !overlay.shown() {
		t.Fatal("overlay hidden while transcribing")
	}

	present(overlay, CompactModel(session.StateIdle), true)
	if overlay.shown() {
		t.Fatal("overlay still shown after returning to idle")
	}

	want := []string{"show", "show", "hide"}
	got := overlay.history()
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls = %v, want %v", got, want)
		}
	}
}

func TestOverlayStaysUpUntilTheTrayAppears(t *testing.T) {
	overlay := &visibilityOverlay{}

	// The right-click menu is the documented fallback into Settings and Quit,
	// so an idle overlay must not vanish before the tray can take over.
	present(overlay, CompactModel(session.StateIdle), false)
	if !overlay.shown() {
		t.Fatal("overlay hidden while idle with no tray, stranding the fallback")
	}

	present(overlay, CompactModel(session.StateIdle), true)
	if overlay.shown() {
		t.Error("overlay still shown once the tray is available")
	}
}

func TestMarkTrayReadyTakesTheFallbackDown(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay}

	manager.render(CompactModel(session.StateIdle))
	if !overlay.shown() {
		t.Fatal("overlay hidden before the tray was known")
	}

	manager.markTrayReady()
	if overlay.shown() {
		t.Error("overlay still shown after the tray appeared")
	}
}

func TestMarkTrayReadyIsIdempotent(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay}

	manager.markTrayReady()
	before := len(overlay.history())
	manager.markTrayReady()

	// A second call must not re-render; the tray only appears once.
	if after := len(overlay.history()); after != before {
		t.Errorf("second markTrayReady produced %d extra calls, want 0", after-before)
	}
}

func TestRecordingShowsEvenBeforeTheTrayAppears(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay}

	manager.render(CompactModel(session.StateRecording))
	if !overlay.shown() {
		t.Error("overlay hidden while recording")
	}
}

func TestVisibilityIsRaceFree(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); manager.render(CompactModel(session.StateRecording)) }()
		go func() { defer wg.Done(); manager.render(CompactModel(session.StateIdle)) }()
		go func() { defer wg.Done(); manager.markTrayReady() }()
	}
	wg.Wait()
}
