package ui

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/aploide/sussurro/internal/session"
)

func TestViewModeValues(t *testing.T) {
	for mode, name := range map[ViewMode]string{ViewCompact: "compact", ViewExpanded: "expanded"} {
		if !mode.Valid() {
			t.Errorf("%s should be valid", name)
		}
		if got := mode.String(); got != name {
			t.Errorf("String() = %q, want %q", got, name)
		}
	}
	invalid := ViewMode(viewModeCount)
	if invalid.Valid() || invalid.String() != "invalid" {
		t.Errorf("out-of-range mode misdescribed: %s", invalid)
	}
}

func TestCompactModelPreservesImmediateBehavior(t *testing.T) {
	tests := []struct {
		state  AppState
		status string
	}{
		{state: session.StateIdle, status: ""},
		// The waveform occupies this slot while recording, so no word is shown.
		{state: session.StateRecording, status: ""},
		// "Finalizing" names the whole post-recording pass without exposing an
		// implementation detail or changing with the path that stopped recording.
		{state: session.StateTranscribing, status: "Finalizing"},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			model := CompactModel(tt.state)

			if model.State != tt.state {
				t.Errorf("State = %s, want %s", model.State, tt.state)
			}
			// Immediate mode must never expand into a transcript card.
			if model.Mode != ViewCompact {
				t.Errorf("Mode = %s, want compact", model.Mode)
			}
			if model.Reviewing {
				t.Error("Reviewing = true for an immediate-mode model")
			}
			if model.Transcript != "" {
				t.Errorf("Transcript = %q, want empty", model.Transcript)
			}
			if model.Status != tt.status {
				t.Errorf("Status = %q, want %q", model.Status, tt.status)
			}
		})
	}
}

func TestReviewModelExpandsOnlyWithText(t *testing.T) {
	tests := []struct {
		name       string
		transcript string
		want       ViewMode
	}{
		{name: "no text yet", transcript: "", want: ViewCompact},
		{name: "whitespace only", transcript: "   \n\t", want: ViewCompact},
		{name: "real text", transcript: "hello", want: ViewExpanded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := ReviewModel(session.ReviewRecording, tt.transcript, true)
			if model.Mode != tt.want {
				t.Errorf("Mode = %s, want %s", model.Mode, tt.want)
			}
		})
	}
}

func TestReviewModelMapsToLifecycleState(t *testing.T) {
	tests := []struct {
		review session.ReviewState
		want   AppState
	}{
		{review: session.ReviewIdle, want: session.StateIdle},
		{review: session.ReviewRecording, want: session.StateRecording},
		{review: session.ReviewEditing, want: session.StateRecording},
		{review: session.ReviewFinalizing, want: session.StateTranscribing},
		{review: session.ReviewApplyingEdit, want: session.StateTranscribing},
		{review: session.ReviewReady, want: session.StateIdle},
		{review: session.ReviewDelivering, want: session.StateIdle},
	}

	for _, tt := range tests {
		t.Run(tt.review.String(), func(t *testing.T) {
			model := ReviewModel(tt.review, "text", false)

			// The existing capsule and tray icon read State, so every review
			// state must map onto a state they already understand.
			if !model.State.Valid() {
				t.Fatalf("State = %v, want a valid lifecycle state", model.State)
			}
			if model.State != tt.want {
				t.Errorf("State = %s, want %s", model.State, tt.want)
			}
			if !model.Reviewing {
				t.Error("Reviewing = false for a review model")
			}
			if model.Review != tt.review {
				t.Errorf("Review = %s, want %s", model.Review, tt.review)
			}
		})
	}
}

func TestReviewModelMarksFinalizingTranscript(t *testing.T) {
	finalizing := ReviewModel(session.ReviewFinalizing, "almost final", true)
	if !finalizing.Finalizing {
		t.Error("Finalizing = false for ReviewFinalizing")
	}

	applyingEdit := ReviewModel(session.ReviewApplyingEdit, "revised text", false)
	if applyingEdit.Finalizing {
		t.Error("Finalizing = true while applying an edit")
	}
}

func TestReviewModelStatusDescribesAffordances(t *testing.T) {
	model := ReviewModel(session.ReviewReady, "text", false)
	if !strings.Contains(model.Status, "deliver") {
		t.Errorf("Status = %q, want it to describe delivery", model.Status)
	}
	if model.Partial {
		t.Error("Partial = true for a final transcript")
	}
}

func TestErrorModelKeepsTranscriptVisible(t *testing.T) {
	model := ErrorModel(session.ReviewReady, "precious text", "Delivery failed: no target")

	if model.Transcript != "precious text" {
		t.Errorf("Transcript = %q, want the text kept on screen", model.Transcript)
	}
	if model.Mode != ViewExpanded {
		t.Errorf("Mode = %s, want the card to stay expanded", model.Mode)
	}
	if !strings.Contains(model.Status, "failed") {
		t.Errorf("Status = %q, want it to report the failure", model.Status)
	}
}

// capturingOverlay records what the Manager asked the platform to render.
type capturingOverlay struct {
	mu     sync.Mutex
	states []AppState
}

func (o *capturingOverlay) Show()           {}
func (o *capturingOverlay) Hide()           {}
func (o *capturingOverlay) Close()          {}
func (o *capturingOverlay) PushRMS(float32) {}

func (o *capturingOverlay) SetState(state AppState) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.states = append(o.states, state)
}

func (o *capturingOverlay) recorded() []AppState {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]AppState(nil), o.states...)
}

// presentingOverlay also implements Presenter.
type presentingOverlay struct {
	capturingOverlay
	models []ViewModel
}

func (o *presentingOverlay) Present(model ViewModel) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.models = append(o.models, model)
}

func (o *presentingOverlay) presented() []ViewModel {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]ViewModel(nil), o.models...)
}

func TestPresentFallsBackToSetStateWithoutPresenter(t *testing.T) {
	overlay := &capturingOverlay{}
	// A platform that cannot render text must still get the capsule state.
	present(overlay, ReviewModel(session.ReviewRecording, "partial text", true), true)

	states := overlay.recorded()
	if len(states) != 1 || states[0] != session.StateRecording {
		t.Errorf("states = %v, want the recording capsule state", states)
	}
}

func TestPresentUsesPresenterWhenAvailable(t *testing.T) {
	overlay := &presentingOverlay{}
	present(overlay, ReviewModel(session.ReviewReady, "final text", false), true)

	models := overlay.presented()
	if len(models) != 1 || models[0].Transcript != "final text" {
		t.Fatalf("presented %v, want the full model", models)
	}
	if got := overlay.recorded(); len(got) != 0 {
		t.Errorf("also called SetState %v, want the presenter path only", got)
	}
}

// collect returns a sink and the models published to it.
func collect() (func(ViewModel), func() []ViewModel) {
	var mu sync.Mutex
	var models []ViewModel
	return func(model ViewModel) {
			mu.Lock()
			defer mu.Unlock()
			models = append(models, model)
		}, func() []ViewModel {
			mu.Lock()
			defer mu.Unlock()
			return append([]ViewModel(nil), models...)
		}
}

func TestReviewPresenterShowsPartialThenFinalText(t *testing.T) {
	sink, published := collect()
	presenter := NewReviewPresenter(sink)

	presenter.OnReviewState(session.ReviewRecording)
	presenter.OnPartialText("hello wor")
	presenter.OnReviewState(session.ReviewFinalizing)
	presenter.OnReviewText("Hello, world.")

	models := published()
	if len(models) != 4 {
		t.Fatalf("published %d models, want 4", len(models))
	}
	if models[1].Transcript != "hello wor" || !models[1].Partial {
		t.Errorf("partial model = %+v, want the partial text flagged", models[1])
	}
	// The transcript must survive the state change between partial and final.
	if models[2].Transcript != "hello wor" {
		t.Errorf("model after state change = %q, want the text retained", models[2].Transcript)
	}
	if models[3].Transcript != "Hello, world." || models[3].Partial {
		t.Errorf("final model = %+v, want the final text unflagged", models[3])
	}
}

func TestReviewPresenterClearsTextOnNewRecording(t *testing.T) {
	sink, published := collect()
	presenter := NewReviewPresenter(sink)

	presenter.OnReviewText("previous dictation")
	presenter.OnReviewState(session.ReviewRecording)

	models := published()
	last := models[len(models)-1]
	// Showing the previous dictation while recording a new one is confusing.
	if last.Transcript != "" {
		t.Errorf("Transcript = %q at the start of a new recording, want empty", last.Transcript)
	}
	if last.Mode != ViewCompact {
		t.Errorf("Mode = %s, want compact with no text", last.Mode)
	}
}

func TestReviewPresenterClearsTextOnIdle(t *testing.T) {
	sink, published := collect()
	presenter := NewReviewPresenter(sink)

	presenter.OnReviewText("delivered text")
	presenter.OnReviewState(session.ReviewIdle)

	models := published()
	if last := models[len(models)-1]; last.Transcript != "" {
		t.Errorf("Transcript = %q after returning to idle, want empty", last.Transcript)
	}
}

func TestReviewPresenterKeepsTextOnDeliveryError(t *testing.T) {
	sink, published := collect()
	presenter := NewReviewPresenter(sink)

	presenter.OnReviewState(session.ReviewReady)
	presenter.OnReviewText("precious text")
	presenter.OnDeliveryError(errors.New("no window focused"))

	models := published()
	last := models[len(models)-1]
	if last.Transcript != "precious text" {
		t.Errorf("Transcript = %q, want the text kept after a failed delivery", last.Transcript)
	}
	if !strings.Contains(last.Status, "no window focused") {
		t.Errorf("Status = %q, want the backend error surfaced", last.Status)
	}
}

func TestReviewPresenterWithoutSinkDoesNotPanic(t *testing.T) {
	presenter := NewReviewPresenter(nil)
	presenter.OnReviewState(session.ReviewReady)
	presenter.OnPartialText("text")
	presenter.OnReviewText("text")
	presenter.OnDeliveryError(errors.New("failure"))
}

func TestReviewPresenterConcurrentUse(t *testing.T) {
	sink, _ := collect()
	presenter := NewReviewPresenter(sink)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(4)
		go func() { defer wg.Done(); presenter.OnReviewState(session.ReviewRecording) }()
		go func() { defer wg.Done(); presenter.OnPartialText("partial") }()
		go func() { defer wg.Done(); presenter.OnReviewText("final") }()
		go func() { defer wg.Done(); presenter.OnDeliveryError(errors.New("failure")) }()
	}
	wg.Wait()
}

func TestManagerPublishesModelsToOverlay(t *testing.T) {
	overlay := &presentingOverlay{}
	manager := &Manager{
		stateChangeCh: make(chan ViewModel, 16),
		rmsCh:         make(chan float32, 16),
		quitCh:        make(chan struct{}),
		overlay:       overlay,
	}

	manager.OnStateChange(session.StateRecording)
	manager.Present(ReviewModel(session.ReviewReady, "reviewed", false))

	// Drain synchronously rather than racing the real processUpdates loop.
	for len(manager.stateChangeCh) > 0 {
		present(manager.overlay, <-manager.stateChangeCh, true)
	}

	models := overlay.presented()
	if len(models) != 2 {
		t.Fatalf("presented %d models, want 2", len(models))
	}
	if models[0].Mode != ViewCompact || models[0].Reviewing {
		t.Errorf("first model = %+v, want the compact immediate-mode view", models[0])
	}
	if models[1].Transcript != "reviewed" {
		t.Errorf("second model transcript = %q, want reviewed", models[1].Transcript)
	}
}

func TestManagerRejectsInvalidModels(t *testing.T) {
	manager := &Manager{stateChangeCh: make(chan ViewModel, 4)}

	manager.OnStateChange(AppState(99))
	manager.Present(ViewModel{State: AppState(99)})
	manager.Present(ViewModel{State: session.StateIdle, Mode: ViewMode(99)})

	if queued := len(manager.stateChangeCh); queued != 0 {
		t.Errorf("queued %d invalid models, want 0", queued)
	}
}

func TestManagerDropsUpdatesWhenSaturated(t *testing.T) {
	manager := &Manager{stateChangeCh: make(chan ViewModel, 2)}

	// Publishing must never block a pipeline goroutine.
	for i := 0; i < 100; i++ {
		manager.OnStateChange(session.StateRecording)
	}
	if queued := len(manager.stateChangeCh); queued != 2 {
		t.Errorf("queued %d models, want the channel capacity 2", queued)
	}
}

// TestPostRecordingStatusDoesNotDependOnWhetherTextWasShown covers the
// recurring sussurro-xvj.34 failure. Every stop path must use the same
// user-facing label, so a missing partial cannot expose "Transcribing" again.
func TestPostRecordingStatusDoesNotDependOnWhetherTextWasShown(t *testing.T) {
	for name, model := range map[string]ViewModel{
		"without streaming": CompactModel(session.StateTranscribing),
		"with streaming":    StreamingCompactModel(session.StateTranscribing),
	} {
		if model.Status != "Finalizing" {
			t.Errorf("%s status = %q, want %q", name, model.Status, "Finalizing")
		}
	}

	// Cleanup is named the same either way: it describes the work itself,
	// not what preceded it.
	if got := CompactModel(session.StateCleaningUp).Status; got != "Cleaning up" {
		t.Errorf("cleanup status = %q, want %q", got, "Cleaning up")
	}
	if got := StreamingCompactModel(session.StateCleaningUp).Status; got != "Cleaning up" {
		t.Errorf("streaming cleanup status = %q, want %q", got, "Cleaning up")
	}
}
