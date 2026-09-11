package ui

import (
	"testing"
	"time"

	"github.com/aploide/sussurro/internal/config"
	"github.com/aploide/sussurro/internal/session"
)

// newReviewManager builds a Manager for a review-mode config, as main.go does.
func newReviewManager(t *testing.T) *Manager {
	t.Helper()
	cfg := &config.Config{}
	cfg.Workflow.Mode = config.ModeReview
	manager, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	manager.overlay = &presentingOverlay{}
	return manager
}

// TestPipelineNotificationsAreIgnoredInReviewMode covers H4: the pipeline's
// deferred OnFinished follows the controller's Ready card on the same channel
// and used to replace it with an idle model that hid the overlay a second
// later. Its state changes likewise collapsed held text during editing.
func TestPipelineNotificationsAreIgnoredInReviewMode(t *testing.T) {
	manager := newReviewManager(t)

	manager.OnFinished("the reviewed text")
	manager.OnStateChange(session.StateRecording)
	manager.OnPhase(session.StateTranscribing, "partial")

	select {
	case model := <-manager.stateChangeCh:
		t.Errorf("pipeline notification published %+v in review mode, want it dropped", model)
	default:
	}
}

// The review presenter's own models still flow: they are what the controller
// uses to show recording, editing, and the Ready card.
func TestReviewPresenterStillPublishesInReviewMode(t *testing.T) {
	manager := newReviewManager(t)
	presenter := NewReviewPresenter(manager.Present)

	presenter.OnReviewText("held text")
	presenter.OnReviewState(session.ReviewEditing)

	var models []ViewModel
	for len(manager.stateChangeCh) > 0 {
		models = append(models, <-manager.stateChangeCh)
	}
	if len(models) != 2 {
		t.Fatalf("published %d models, want the review text and the editing state", len(models))
	}
	editing := models[1]
	if editing.State != session.StateRecording || editing.Review != session.ReviewEditing {
		t.Errorf("editing model = state %v review %v, want recording/editing", editing.State, editing.Review)
	}
	if editing.Transcript != "held text" {
		t.Errorf("Transcript = %q while editing, want the held text kept on screen", editing.Transcript)
	}
}

// Immediate mode is unchanged: the pipeline is the only source of updates.
func TestPipelineNotificationsFlowInImmediateMode(t *testing.T) {
	manager, err := NewManager(&config.Config{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	manager.overlay = &presentingOverlay{}

	manager.OnStateChange(session.StateRecording)
	manager.OnFinished("done")

	if got := len(manager.stateChangeCh); got != 2 {
		t.Errorf("published %d models in immediate mode, want 2", got)
	}
}

// When review mode is configured but could not be wired, main.go falls back
// to immediate dictation and tells the Manager so; the pipeline is then the
// only source of overlay updates and must be heard again.
func TestSetReviewModeReleasesPipelineNotifications(t *testing.T) {
	manager := newReviewManager(t)
	manager.SetReviewMode(false)

	manager.OnFinished("done")

	if got := len(manager.stateChangeCh); got != 1 {
		t.Errorf("published %d models after falling back to immediate, want 1", got)
	}
}

// TestUnhostedTrayKeepsTheOverlayUp covers H2: systray reports readiness
// before it has registered with any host, so readiness alone must not release
// the overlay — on a desktop with no StatusNotifier host it is the only route
// to Settings and Quit.
func TestUnhostedTrayKeepsTheOverlayUp(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay, hideLingerOverride: testLinger}

	manager.render(CompactModel(session.StateIdle))
	manager.onTrayHosted(false)

	if manager.trayReady.Load() {
		t.Error("trayReady = true with no tray host")
	}
	time.Sleep(testLinger + 20*time.Millisecond)
	if !overlay.shown() {
		t.Error("overlay hidden with no tray host, stranding the fallback")
	}
}

// Without a tray host the overlay stays up, but the finished text must still
// age out: otherwise the last dictation sits on the desktop until the next
// key press.
func TestUnhostedTrayStillClearsFinishedText(t *testing.T) {
	overlay := &shownPresentingOverlay{}
	manager := &Manager{overlay: overlay, hideLingerOverride: testLinger}
	manager.onTrayHosted(false)

	manager.render(CompactModel(session.StateRecording))
	manager.render(ViewModel{
		State:      session.StateIdle,
		Transcript: "the quick brown fox",
		Mode:       ViewExpanded,
	})

	waitFor(t, func() bool {
		models := overlay.presented()
		return len(models) > 1 && models[len(models)-1].Transcript == ""
	}, "finished text never cleared without a tray host")
	if !overlay.shown() {
		t.Error("overlay hidden with no tray host after the linger")
	}
}

// shownPresentingOverlay renders text and tracks visibility, for tests that
// need both.
type shownPresentingOverlay struct {
	visibilityOverlay
	models []ViewModel
}

func (o *shownPresentingOverlay) Present(model ViewModel) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.models = append(o.models, model)
}

func (o *shownPresentingOverlay) presented() []ViewModel {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]ViewModel(nil), o.models...)
}

func TestHostedTrayReleasesTheOverlay(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay, hideLingerOverride: testLinger}

	manager.render(CompactModel(session.StateIdle))
	manager.onTrayHosted(true)

	if !manager.trayReady.Load() {
		t.Fatal("trayReady = false with a tray host")
	}
	waitFor(t, func() bool { return !overlay.shown() },
		"overlay still shown after a hosted tray registered")
}

// A tray widget can be removed while the app runs (on KDE the watcher
// service keeps running regardless), so losing the host must bring the
// overlay back as the route to Settings and Quit.
func TestTrayHostGoingAwayBringsTheOverlayBack(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay, hideLingerOverride: testLinger, stateChangeCh: make(chan ViewModel, 4)}

	manager.render(CompactModel(session.StateIdle))
	manager.onTrayHosted(true)
	waitFor(t, func() bool { return !overlay.shown() }, "overlay still shown with a hosted tray")

	manager.onTrayHosted(false)
	if manager.trayReady.Load() {
		t.Error("trayReady = true after the host unregistered")
	}
	// The re-render is queued for the update loop; run it by hand here.
	select {
	case model := <-manager.stateChangeCh:
		manager.render(model)
	default:
		t.Fatal("losing the tray host queued no re-render")
	}
	if !overlay.shown() {
		t.Error("overlay hidden after the tray host went away")
	}
}

// TestStaleLingerDoesNotHideANewerModel covers the linger race: Stop() cannot
// cancel a callback that has already started and is waiting on hideMu, so a
// linger that finds a different timer installed must not hide what the newer
// model put on screen.
func TestStaleLingerDoesNotHideANewerModel(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay, hideLingerOverride: time.Hour}
	manager.trayReady.Store(true)

	finished := ViewModel{State: session.StateIdle, Transcript: "final words", Mode: ViewExpanded}
	manager.render(finished)
	manager.hideMu.Lock()
	stale := manager.hideSeq
	installed := manager.hideTimer != nil
	manager.hideMu.Unlock()
	if !installed {
		t.Fatal("no linger installed for the finished text")
	}

	// A new dictation supersedes the linger; its callback then fires late.
	manager.render(CompactModel(session.StateRecording))
	manager.lingerExpired(stale, finished, true)

	if !overlay.shown() {
		t.Error("a stale linger hid the overlay during a new recording")
	}
	manager.hideMu.Lock()
	visible := manager.overlayVisible
	manager.hideMu.Unlock()
	if !visible {
		t.Error("a stale linger marked the overlay hidden")
	}
}
