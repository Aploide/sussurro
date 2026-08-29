package review

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aploide/sussurro/internal/config"
	"github.com/aploide/sussurro/internal/delivery"
	"github.com/aploide/sussurro/internal/session"
)

// wired mirrors what cmd/sussurro assembles for review mode, so the assembled
// application path is covered rather than only the components in isolation.
type wired struct {
	controller *session.Controller
	dispatch   session.InputDispatcher
	backend    *recordingBackend
	models     *modelSink
	editor     *fakeTextEditor
}

// presented is one presentation event, flattened from the Presenter calls.
// The concrete view model lives in internal/ui, which cannot be imported here:
// it pulls in WebKitGTK, which the race detector cannot build. What matters at
// this level is that the controller drives presentation at all.
type presented struct {
	State      session.ReviewState
	Transcript string
	Partial    bool
	Error      error
}

// modelSink implements session.Presenter and records what it was told.
type modelSink struct {
	mu     sync.Mutex
	state  session.ReviewState
	events []presented
}

func (s *modelSink) OnReviewState(state session.ReviewState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	s.events = append(s.events, presented{State: state})
}

func (s *modelSink) OnPartialText(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, presented{State: s.state, Transcript: text, Partial: true})
}

func (s *modelSink) OnReviewText(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, presented{State: s.state, Transcript: text})
}

func (s *modelSink) OnDeliveryError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, presented{State: s.state, Error: err})
}

func (s *modelSink) snapshot() []presented {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]presented(nil), s.events...)
}

// wire assembles review mode the way main.go does: the controller is both the
// result consumer's target and the input dispatcher, with the presenter, LLM
// editor, and delivery backend attached.
func wire(t *testing.T, cfg *config.Config) *wired {
	t.Helper()

	backend, err := delivery.SelectBackend(
		delivery.BackendName(cfg.Workflow.Delivery.Backend),
		delivery.Capabilities{
			LookPath:  func(string) (string, error) { return "", errors.New("not found") },
			Clipboard: &recordingBackend{},
		},
	)
	if err != nil {
		t.Fatalf("SelectBackend() error = %v", err)
	}
	recorder, ok := backend.(*recordingBackend)
	if !ok {
		t.Fatalf("expected the clipboard fallback, got %s", backend.Name())
	}

	models := &modelSink{}
	model := &fakeTextEditor{result: "The revised text."}

	var controller *session.Controller
	editor := NewEditor(model, func(id session.SessionID, text string) {
		controller.OnEdited(id, text)
	}, discardLog())

	controller = session.NewController(
		stubRecognizer{},
		editor,
		delivery.NewDeliverer(backend, noWait),
		models,
		discardLog(),
	)

	return &wired{
		controller: controller,
		dispatch:   controller,
		backend:    recorder,
		models:     models,
		editor:     model,
	}
}

// reviewConfig returns a config with review mode enabled.
func reviewConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{}
	cfg.Workflow.Mode = config.ModeReview
	cfg.Workflow.Normalize()
	if err := cfg.Workflow.Validate(); err != nil {
		t.Fatalf("review config failed validation: %v", err)
	}
	return cfg
}

func TestWiredReviewModeHoldsTextUntilAsked(t *testing.T) {
	app := wire(t, reviewConfig(t))

	// A gesture from any input source arrives through the dispatcher.
	app.dispatch.Dispatch(session.InputPress)
	app.dispatch.Dispatch(session.InputRelease)
	app.controller.OnResult(app.controller.SessionID(), "the transcribed text")

	if got := app.controller.State(); got != session.ReviewReady {
		t.Fatalf("state = %s, want ready", got)
	}
	// Nothing may reach the focused window before the user asks.
	if len(app.backend.typed) != 0 {
		t.Fatalf("typed %v on reaching ready, want nothing", app.backend.typed)
	}

	if err := app.controller.Deliver(false); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if len(app.backend.typed) != 1 || app.backend.typed[0] != "the transcribed text" {
		t.Errorf("typed %v, want the reviewed text delivered once", app.backend.typed)
	}
}

func TestWiredReviewModePresentsProgress(t *testing.T) {
	app := wire(t, reviewConfig(t))

	app.dispatch.Dispatch(session.InputPress)
	app.controller.OnPartial(app.controller.SessionID(), "partial te")
	app.dispatch.Dispatch(session.InputRelease)
	app.controller.OnResult(app.controller.SessionID(), "Partial text.")

	events := app.models.snapshot()
	if len(events) == 0 {
		t.Fatal("nothing was presented, want the overlay driven")
	}

	var sawPartial, sawFinal bool
	for _, event := range events {
		if event.Transcript == "partial te" && event.Partial {
			sawPartial = true
		}
		if event.Transcript == "Partial text." && !event.Partial {
			sawFinal = true
		}
	}
	if !sawPartial {
		t.Error("partial text was never presented")
	}
	if !sawFinal {
		t.Error("final text was never presented")
	}
}

func TestWiredReviewModeAppliesVoiceEdit(t *testing.T) {
	app := wire(t, reviewConfig(t))

	app.dispatch.Dispatch(session.InputPress)
	app.dispatch.Dispatch(session.InputRelease)
	app.controller.OnResult(app.controller.SessionID(), "the original text")

	// Holding again over ready text records a correction.
	app.dispatch.Dispatch(session.InputPress)
	app.dispatch.Dispatch(session.InputRelease)
	app.controller.OnResult(app.controller.SessionID(), "fix the wording")

	waitFor(t, func() bool { return app.controller.State() == session.ReviewReady })

	if got := app.controller.Text(); got != "The revised text." {
		t.Errorf("Text() = %q, want the revised text", got)
	}
	if len(app.backend.typed) != 0 {
		t.Errorf("typed %v after an edit, want nothing until asked", app.backend.typed)
	}
}

func TestWiredReviewModeCancelDiscardsEverything(t *testing.T) {
	app := wire(t, reviewConfig(t))

	app.dispatch.Dispatch(session.InputPress)
	app.dispatch.Dispatch(session.InputRelease)
	app.controller.OnResult(app.controller.SessionID(), "text to discard")

	app.controller.Cancel()

	if got := app.controller.State(); got != session.ReviewIdle {
		t.Errorf("state = %s, want idle", got)
	}
	if err := app.controller.Deliver(false); !errors.Is(err, session.ErrNothingToDeliver) {
		t.Fatalf("Deliver() error = %v, want ErrNothingToDeliver", err)
	}
	if len(app.backend.typed) != 0 {
		t.Errorf("typed %v after cancel, want nothing", app.backend.typed)
	}
}

func TestWiredReviewModeSubmitSendsEnter(t *testing.T) {
	app := wire(t, reviewConfig(t))

	app.dispatch.Dispatch(session.InputPress)
	app.dispatch.Dispatch(session.InputRelease)
	app.controller.OnResult(app.controller.SessionID(), "text")

	if err := app.controller.Deliver(true); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if app.backend.submits != 1 {
		t.Errorf("submits = %d, want 1", app.backend.submits)
	}
}

func TestWiredDispatcherReportsRecordingStopped(t *testing.T) {
	app := wire(t, reviewConfig(t))

	// The trigger server and hotkey callbacks use this to log progress.
	if outcome := app.dispatch.Dispatch(session.InputPress); outcome != session.InputStarted {
		t.Errorf("press outcome = %v, want started", outcome)
	}
	if outcome := app.dispatch.Dispatch(session.InputRelease); outcome != session.InputStopped {
		t.Errorf("release outcome = %v, want stopped", outcome)
	}
}

func TestWiredReviewModeSelectsClipboardOnThisHost(t *testing.T) {
	cfg := reviewConfig(t)
	// auto on a host without wtype or ydotool, which is the primary target.
	if cfg.Workflow.Delivery.Backend != config.DeliveryAuto {
		t.Fatalf("Delivery.Backend = %q, want the auto default", cfg.Workflow.Delivery.Backend)
	}

	app := wire(t, cfg)
	app.dispatch.Dispatch(session.InputPress)
	app.dispatch.Dispatch(session.InputRelease)
	app.controller.OnResult(app.controller.SessionID(), "text")

	if err := app.controller.Deliver(false); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if len(app.backend.typed) != 1 {
		t.Errorf("clipboard fallback typed %v, want the delivery routed through it", app.backend.typed)
	}
}

func TestWiredReviewModeSurvivesDeliveryFailure(t *testing.T) {
	app := wire(t, reviewConfig(t))
	app.backend.typeErr = errors.New("no window focused")

	app.dispatch.Dispatch(session.InputPress)
	app.dispatch.Dispatch(session.InputRelease)
	app.controller.OnResult(app.controller.SessionID(), "precious text")

	if err := app.controller.Deliver(false); err == nil {
		t.Fatal("Deliver() error = nil, want the backend failure")
	}
	if got := app.controller.State(); got != session.ReviewReady {
		t.Errorf("state = %s, want ready so the text is not lost", got)
	}
	if got := app.controller.Text(); got != "precious text" {
		t.Errorf("Text() = %q, want the text preserved", got)
	}

	// The failure must be visible to the user, not only returned.
	var sawError bool
	for _, event := range app.models.snapshot() {
		if event.Error != nil {
			sawError = true
		}
	}
	if !sawError {
		t.Error("the delivery failure was never presented")
	}
}

func TestImmediateModeConfigDoesNotEnableReview(t *testing.T) {
	cfg := &config.Config{}
	cfg.Workflow.Normalize()

	// The default must remain the untouched immediate path.
	if cfg.Workflow.ReviewEnabled() {
		t.Error("ReviewEnabled() = true for a default config, want false")
	}
}

func TestWiredReviewModeIsRaceFree(t *testing.T) {
	app := wire(t, reviewConfig(t))

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(5)
		go func() { defer wg.Done(); app.dispatch.Dispatch(session.InputPress) }()
		go func() { defer wg.Done(); app.dispatch.Dispatch(session.InputRelease) }()
		go func() { defer wg.Done(); app.controller.OnResult(app.controller.SessionID(), "text") }()
		go func() { defer wg.Done(); _ = app.controller.Deliver(false) }()
		go func() { defer wg.Done(); app.controller.Cancel() }()
	}
	wg.Wait()

	// Give any queued edit goroutines a chance to land before the test ends.
	time.Sleep(50 * time.Millisecond)
	if got := app.controller.State(); !got.Valid() {
		t.Errorf("state = %v, want a defined state", got)
	}
}
