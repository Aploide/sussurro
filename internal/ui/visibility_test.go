package ui

import (
	"sync"
	"testing"
	"time"

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
	// present() hides immediately; the linger lives in Manager.render.
}

func TestMarkTrayReadyTakesTheFallbackDown(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay}

	manager.render(CompactModel(session.StateIdle))
	if !overlay.shown() {
		t.Fatal("overlay hidden before the tray was known")
	}

	manager.markTrayReady()

	// Hiding is deferred by hideLinger so finished text can be read.
	waitFor(t, func() bool { return !overlay.shown() },
		"overlay still shown after the tray appeared")
}

// waitFor polls until condition holds, failing with msg on timeout.
func waitFor(t *testing.T, condition func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(hideLinger + 2*time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error(msg)
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

func TestOverlayLingersBeforeHiding(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay}
	manager.trayReady.Store(true)

	manager.render(CompactModel(session.StateRecording))
	if !overlay.shown() {
		t.Fatal("overlay hidden while recording")
	}

	manager.render(CompactModel(session.StateIdle))

	// Hiding the instant a dictation ends leaves no time to read the result.
	if !overlay.shown() {
		t.Error("overlay hid immediately, want it to linger")
	}

	waitFor(t, func() bool { return !overlay.shown() },
		"overlay never hid after the linger elapsed")
}

func TestNewDictationCancelsAPendingHide(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay}
	manager.trayReady.Store(true)

	manager.render(CompactModel(session.StateIdle))
	manager.render(CompactModel(session.StateRecording))

	// The previous dictation's linger must not hide the new one mid-flow.
	time.Sleep(hideLinger + 200*time.Millisecond)
	if !overlay.shown() {
		t.Error("a pending hide fired during a new recording")
	}
}

func TestTranscribingKeepsTextOnScreen(t *testing.T) {
	overlay := &presentingOverlay{}
	manager := &Manager{
		stateChangeCh: make(chan ViewModel, 8),
		overlay:       overlay,
	}

	manager.OnTranscribing("the text so far")

	select {
	case model := <-manager.stateChangeCh:
		// Blanking text the user is reading, only to restore the same words
		// moments later, reads as the app losing their dictation.
		if model.Transcript != "the text so far" {
			t.Errorf("Transcript = %q, want the partial retained", model.Transcript)
		}
		if model.State != session.StateTranscribing {
			t.Errorf("State = %s, want transcribing", model.State)
		}
		if !model.Visible() {
			t.Error("Visible() = false while transcribing with text")
		}
	default:
		t.Fatal("OnTranscribing queued nothing")
	}
}

func TestFinishedTextIsShownBeforeHiding(t *testing.T) {
	overlay := &presentingOverlay{}
	manager := &Manager{overlay: overlay}
	manager.trayReady.Store(true)

	manager.render(ViewModel{
		State:      session.StateIdle,
		Transcript: "the complete transcription",
		Status:     "Done",
		Mode:       ViewExpanded,
	})

	// The finished text must go on screen immediately. Deferring the draw as
	// well as the hide meant the user never saw the last words at all.
	models := overlay.presented()
	if len(models) == 0 {
		t.Fatal("nothing was presented; the finished text was never drawn")
	}
	if models[0].Transcript != "the complete transcription" {
		t.Errorf("first presented model = %q, want the finished text", models[0].Transcript)
	}

	waitFor(t, func() bool {
		shown := overlay.presented()
		return len(shown) > 1 && shown[len(shown)-1].Transcript == ""
	}, "the overlay never cleared the text after the linger")
}

func TestFinishedTextStaysForTheFullLinger(t *testing.T) {
	overlay := &presentingOverlay{}
	manager := &Manager{overlay: overlay}
	manager.trayReady.Store(true)

	manager.render(ViewModel{
		State:      session.StateIdle,
		Transcript: "final words",
		Mode:       ViewExpanded,
	})

	// Half a linger in, the text must still be there: the second is measured
	// from when it was displayed, not from when the key was released.
	time.Sleep(hideLinger / 2)
	models := overlay.presented()
	if models[len(models)-1].Transcript != "final words" {
		t.Errorf("text cleared after %v, want it held for the full linger", hideLinger/2)
	}
}

func TestOnFinishedCarriesTheText(t *testing.T) {
	manager := &Manager{stateChangeCh: make(chan ViewModel, 4)}

	manager.OnFinished("what was actually transcribed")

	select {
	case model := <-manager.stateChangeCh:
		if model.Transcript != "what was actually transcribed" {
			t.Errorf("Transcript = %q, want the finished text", model.Transcript)
		}
	default:
		t.Fatal("OnFinished queued nothing")
	}
}

func TestBothHotkeyBindingsAreInstalled(t *testing.T) {
	manager := &Manager{}

	// The point of the redesign: a held key and a tapped key at once, which
	// the single-trigger-plus-mode design could not express.
	manager.InstallHotkey(HotkeyBindings{
		PushToTalk: "super+7",
		Toggle:     "super+8",
		OnPress:    func() {},
		OnRelease:  func() {},
		OnToggle:   func() {},
	})

	if manager.bindings.PushToTalk != "super+7" {
		t.Errorf("PushToTalk = %q, want super+7", manager.bindings.PushToTalk)
	}
	if manager.bindings.Toggle != "super+8" {
		t.Errorf("Toggle = %q, want super+8", manager.bindings.Toggle)
	}
}

func TestEitherBindingMayBeEmpty(t *testing.T) {
	for _, tt := range []struct{ name, ptt, toggle string }{
		{name: "toggle only", toggle: "super+8"},
		{name: "push to talk only", ptt: "super+7"},
		{name: "neither"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manager := &Manager{}
			// Must not panic or refuse: an unset binding is valid, since
			// Wayland uses the trigger socket instead.
			manager.InstallHotkey(HotkeyBindings{PushToTalk: tt.ptt, Toggle: tt.toggle})

			if manager.bindings.PushToTalk != tt.ptt {
				t.Errorf("PushToTalk = %q, want %q", manager.bindings.PushToTalk, tt.ptt)
			}
			if manager.bindings.Toggle != tt.toggle {
				t.Errorf("Toggle = %q, want %q", manager.bindings.Toggle, tt.toggle)
			}
		})
	}
}
