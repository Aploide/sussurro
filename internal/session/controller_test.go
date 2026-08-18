package session

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
)

type fakeRecognizer struct {
	mu       sync.Mutex
	started  []SessionID
	stopped  []SessionID
	canceled []SessionID
}

func (f *fakeRecognizer) StartCapture(id SessionID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, id)
}

func (f *fakeRecognizer) StopCapture(id SessionID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
}

func (f *fakeRecognizer) CancelCapture(id SessionID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.canceled = append(f.canceled, id)
}

func (f *fakeRecognizer) counts() (started, stopped, canceled int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started), len(f.stopped), len(f.canceled)
}

type fakeEditor struct {
	mu           sync.Mutex
	calls        int
	lastText     string
	lastInstruct string
	// reply, when set, is delivered back to the controller synchronously.
	reply    string
	autoBack *Controller
}

func (f *fakeEditor) ApplyEdit(id SessionID, text, instruction string) {
	f.mu.Lock()
	f.calls++
	f.lastText = text
	f.lastInstruct = instruction
	controller, reply := f.autoBack, f.reply
	f.mu.Unlock()

	if controller != nil {
		controller.OnEdited(id, reply)
	}
}

func (f *fakeEditor) stats() (calls int, text, instruction string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.lastText, f.lastInstruct
}

type fakeDeliverer struct {
	mu       sync.Mutex
	texts    []string
	submits  []bool
	err      error
	onCall   func()
	callDone int
}

func (f *fakeDeliverer) Deliver(text string, submit bool) error {
	f.mu.Lock()
	f.texts = append(f.texts, text)
	f.submits = append(f.submits, submit)
	hook, err := f.onCall, f.err
	f.callDone++
	f.mu.Unlock()

	// The hook lets a test interleave a cancel with an in-flight delivery.
	if hook != nil {
		hook()
	}
	return err
}

func (f *fakeDeliverer) delivered() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.texts...)
}

type fakePresenter struct {
	mu       sync.Mutex
	states   []ReviewState
	partials []string
	reviewed []string
	errs     []error
}

func (f *fakePresenter) OnReviewState(state ReviewState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states = append(f.states, state)
}

func (f *fakePresenter) OnPartialText(text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.partials = append(f.partials, text)
}

func (f *fakePresenter) OnReviewText(text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reviewed = append(f.reviewed, text)
}

func (f *fakePresenter) OnDeliveryError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs = append(f.errs, err)
}

func (f *fakePresenter) snapshot() (partials, reviewed []string, errs []error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.partials...), append([]string(nil), f.reviewed...), append([]error(nil), f.errs...)
}

type harness struct {
	controller *Controller
	recognizer *fakeRecognizer
	editor     *fakeEditor
	deliverer  *fakeDeliverer
	presenter  *fakePresenter
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		recognizer: &fakeRecognizer{},
		editor:     &fakeEditor{},
		deliverer:  &fakeDeliverer{},
		presenter:  &fakePresenter{},
	}
	h.controller = NewController(h.recognizer, h.editor, h.deliverer, h.presenter,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return h
}

// reachReady drives a session from Idle to Ready holding the given text.
func (h *harness) reachReady(t *testing.T, text string) SessionID {
	t.Helper()
	h.controller.Handle(InputPress)
	h.controller.Handle(InputRelease)
	id := h.controller.SessionID()
	h.controller.OnResult(id, text)
	if got := h.controller.State(); got != ReviewReady {
		t.Fatalf("state = %s, want ready", got)
	}
	return id
}

func TestReviewStateValues(t *testing.T) {
	want := map[ReviewState]string{
		ReviewIdle:         "idle",
		ReviewRecording:    "recording",
		ReviewFinalizing:   "finalizing",
		ReviewReady:        "ready",
		ReviewEditing:      "editing",
		ReviewApplyingEdit: "applying-edit",
		ReviewDelivering:   "delivering",
	}
	for state, name := range want {
		if !state.Valid() {
			t.Errorf("%s should be valid", name)
		}
		if got := state.String(); got != name {
			t.Errorf("String() = %q, want %q", got, name)
		}
	}

	invalid := ReviewState(reviewStateCount)
	if invalid.Valid() {
		t.Error("out-of-range state reported valid")
	}
	if got := invalid.String(); got != "invalid" {
		t.Errorf("String() = %q, want %q", got, "invalid")
	}
}

func TestReachesReadyWithoutDelivering(t *testing.T) {
	h := newHarness(t)
	h.reachReady(t, "the reviewed text")

	if got := h.controller.Text(); got != "the reviewed text" {
		t.Errorf("Text() = %q, want the reviewed text", got)
	}
	// Reaching Ready must never insert anything into the focused window.
	if got := h.deliverer.delivered(); len(got) != 0 {
		t.Fatalf("delivered %v on the way to ready, want nothing", got)
	}
	_, reviewed, _ := h.presenter.snapshot()
	if len(reviewed) != 1 || reviewed[0] != "the reviewed text" {
		t.Errorf("presented %v, want one reviewed text", reviewed)
	}
}

func TestDeliverInsertsAndReturnsToIdle(t *testing.T) {
	h := newHarness(t)
	h.reachReady(t, "hello world")

	if err := h.controller.Deliver(false); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if got := h.controller.State(); got != ReviewIdle {
		t.Errorf("state = %s, want idle", got)
	}
	if got := h.deliverer.delivered(); len(got) != 1 || got[0] != "hello world" {
		t.Errorf("delivered %v, want one %q", got, "hello world")
	}
	if got := h.controller.Text(); got != "" {
		t.Errorf("Text() = %q, want cleared after delivery", got)
	}
}

func TestDeliverSubmitFlagIsForwarded(t *testing.T) {
	h := newHarness(t)
	h.reachReady(t, "text")

	if err := h.controller.Deliver(true); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	h.deliverer.mu.Lock()
	defer h.deliverer.mu.Unlock()
	if len(h.deliverer.submits) != 1 || !h.deliverer.submits[0] {
		t.Errorf("submit flags = %v, want one true", h.deliverer.submits)
	}
}

func TestDeliveryFailureKeepsTextInReady(t *testing.T) {
	h := newHarness(t)
	h.deliverer.err = errors.New("no paste target")
	h.reachReady(t, "precious text")

	if err := h.controller.Deliver(false); err == nil {
		t.Fatal("Deliver() error = nil, want the backend failure")
	}
	if got := h.controller.State(); got != ReviewReady {
		t.Errorf("state = %s, want ready so the text survives", got)
	}
	if got := h.controller.Text(); got != "precious text" {
		t.Errorf("Text() = %q, want the text preserved", got)
	}
	if _, _, errs := h.presenter.snapshot(); len(errs) != 1 {
		t.Errorf("presented %d delivery errors, want 1", len(errs))
	}
}

func TestDeliverIgnoredOutsideReady(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(h *harness)
		want  ReviewState
	}{
		{name: "idle", setup: func(h *harness) {}, want: ReviewIdle},
		{name: "recording", setup: func(h *harness) { h.controller.Handle(InputPress) }, want: ReviewRecording},
		{name: "finalizing", setup: func(h *harness) {
			h.controller.Handle(InputPress)
			h.controller.Handle(InputRelease)
		}, want: ReviewFinalizing},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			tt.setup(h)

			if err := h.controller.Deliver(false); !errors.Is(err, ErrNothingToDeliver) {
				t.Fatalf("Deliver() error = %v, want ErrNothingToDeliver", err)
			}
			if got := h.controller.State(); got != tt.want {
				t.Errorf("state = %s, want %s", got, tt.want)
			}
			if got := h.deliverer.delivered(); len(got) != 0 {
				t.Errorf("delivered %v outside ready, want nothing", got)
			}
		})
	}
}

func TestCancelFromEveryStatePreventsDelivery(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(h *harness) SessionID
	}{
		{
			name: "recording",
			setup: func(h *harness) SessionID {
				h.controller.Handle(InputPress)
				return h.controller.SessionID()
			},
		},
		{
			name: "finalizing",
			setup: func(h *harness) SessionID {
				h.controller.Handle(InputPress)
				h.controller.Handle(InputRelease)
				return h.controller.SessionID()
			},
		},
		{
			name: "ready",
			setup: func(h *harness) SessionID {
				return h.reachReady(t, "text")
			},
		},
		{
			name: "editing",
			setup: func(h *harness) SessionID {
				h.reachReady(t, "text")
				h.controller.Handle(InputPress)
				return h.controller.SessionID()
			},
		},
		{
			name: "applying edit",
			setup: func(h *harness) SessionID {
				h.reachReady(t, "text")
				h.controller.Handle(InputPress)
				h.controller.Handle(InputRelease)
				return h.controller.SessionID()
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			id := tt.setup(h)

			h.controller.Cancel()
			if got := h.controller.State(); got != ReviewIdle {
				t.Fatalf("state = %s after cancel, want idle", got)
			}
			if got := h.controller.Text(); got != "" {
				t.Errorf("Text() = %q after cancel, want cleared", got)
			}

			// Every late callback for the cancelled session must be inert.
			h.controller.OnPartial(id, "late partial")
			h.controller.OnResult(id, "late final")
			h.controller.OnEdited(id, "late edit")

			if got := h.controller.State(); got != ReviewIdle {
				t.Errorf("state = %s, want idle after stale callbacks", got)
			}
			if got := h.controller.Text(); got != "" {
				t.Errorf("Text() = %q, want cancelled text to stay gone", got)
			}
			if got := h.deliverer.delivered(); len(got) != 0 {
				t.Errorf("delivered %v after cancel, want nothing", got)
			}
			partials, _, _ := h.presenter.snapshot()
			if len(partials) != 0 {
				t.Errorf("presented partials %v after cancel, want none", partials)
			}
		})
	}
}

func TestCancelNotifiesRecognizerWithCancelledSession(t *testing.T) {
	h := newHarness(t)
	h.controller.Handle(InputPress)
	id := h.controller.SessionID()

	h.controller.Cancel()

	h.recognizer.mu.Lock()
	defer h.recognizer.mu.Unlock()
	if len(h.recognizer.canceled) != 1 || h.recognizer.canceled[0] != id {
		t.Errorf("cancelled sessions = %v, want [%d]", h.recognizer.canceled, id)
	}
}

func TestCancelWhenIdleIsANoOp(t *testing.T) {
	h := newHarness(t)
	h.controller.Cancel()

	if got := h.controller.State(); got != ReviewIdle {
		t.Errorf("state = %s, want idle", got)
	}
	if _, _, canceled := h.recognizer.counts(); canceled != 0 {
		t.Errorf("cancelled %d captures while idle, want 0", canceled)
	}
}

func TestSupersededSessionCallbacksIgnored(t *testing.T) {
	h := newHarness(t)

	h.controller.Handle(InputPress)
	first := h.controller.SessionID()
	h.controller.Cancel()

	h.controller.Handle(InputPress)
	second := h.controller.SessionID()
	if second == first {
		t.Fatalf("second session id = %d, want distinct from %d", second, first)
	}

	// The first session's results arrive late; the second must be unaffected.
	h.controller.OnPartial(first, "stale partial")
	h.controller.OnResult(first, "stale final")
	if got := h.controller.State(); got != ReviewRecording {
		t.Fatalf("state = %s, want the new session still recording", got)
	}

	partials, _, _ := h.presenter.snapshot()
	if len(partials) != 0 {
		t.Errorf("presented stale partials %v, want none", partials)
	}

	// The current session still works end to end.
	h.controller.Handle(InputRelease)
	h.controller.OnResult(second, "fresh text")
	if got := h.controller.Text(); got != "fresh text" {
		t.Errorf("Text() = %q, want fresh text", got)
	}
}

func TestPartialPresentedOnlyWhileCapturing(t *testing.T) {
	h := newHarness(t)
	h.controller.Handle(InputPress)
	id := h.controller.SessionID()

	h.controller.OnPartial(id, "partial one")
	h.controller.Handle(InputRelease)
	// Finalizing is past the point where partials are meaningful.
	h.controller.OnPartial(id, "partial two")

	partials, _, _ := h.presenter.snapshot()
	if len(partials) != 1 || partials[0] != "partial one" {
		t.Errorf("partials = %v, want only the one captured while recording", partials)
	}
}

func TestEditRevisesHeldText(t *testing.T) {
	h := newHarness(t)
	h.editor.autoBack = h.controller
	h.editor.reply = "The quick brown fox."
	h.reachReady(t, "the quick brown focks")

	// Holding the gesture over ready text records a revision instruction.
	if got := h.controller.Handle(InputPress); got != ReviewEditing {
		t.Fatalf("state = %s, want editing", got)
	}
	if got := h.controller.Handle(InputRelease); got != ReviewApplyingEdit {
		t.Fatalf("state = %s, want applying-edit", got)
	}
	h.controller.OnResult(h.controller.SessionID(), "spell fox correctly")

	if got := h.controller.State(); got != ReviewReady {
		t.Fatalf("state = %s, want ready after the edit", got)
	}
	if got := h.controller.Text(); got != "The quick brown fox." {
		t.Errorf("Text() = %q, want the revised text", got)
	}

	calls, text, instruction := h.editor.stats()
	if calls != 1 {
		t.Errorf("editor calls = %d, want 1", calls)
	}
	if text != "the quick brown focks" || instruction != "spell fox correctly" {
		t.Errorf("editor got (%q, %q), want the held text and the instruction", text, instruction)
	}
}

func TestEmptyEditInstructionKeepsTextUnchanged(t *testing.T) {
	h := newHarness(t)
	h.reachReady(t, "original text")

	h.controller.Handle(InputPress)
	h.controller.Handle(InputRelease)
	// A silent edit recording yields no instruction to apply.
	h.controller.OnResult(h.controller.SessionID(), "")

	if got := h.controller.State(); got != ReviewReady {
		t.Fatalf("state = %s, want ready", got)
	}
	if got := h.controller.Text(); got != "original text" {
		t.Errorf("Text() = %q, want the text unchanged", got)
	}
	if calls, _, _ := h.editor.stats(); calls != 0 {
		t.Errorf("editor calls = %d, want 0 for an empty instruction", calls)
	}
}

func TestStaleEditIgnoredAfterCancel(t *testing.T) {
	h := newHarness(t)
	h.reachReady(t, "original")

	h.controller.Handle(InputPress)
	h.controller.Handle(InputRelease)
	editing := h.controller.SessionID()

	h.controller.Cancel()
	// The editor finishes after the user gave up on the session.
	h.controller.OnEdited(editing, "revised too late")

	if got := h.controller.State(); got != ReviewIdle {
		t.Errorf("state = %s, want idle", got)
	}
	if got := h.controller.Text(); got != "" {
		t.Errorf("Text() = %q, want nothing resurrected", got)
	}
}

func TestCancelDuringDeliveryLeavesIdle(t *testing.T) {
	h := newHarness(t)
	h.reachReady(t, "text")

	// Cancel lands while the backend is still inserting text.
	h.deliverer.onCall = func() { h.controller.Cancel() }

	if err := h.controller.Deliver(false); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if got := h.controller.State(); got != ReviewIdle {
		t.Errorf("state = %s, want idle", got)
	}
	if got := h.controller.Text(); got != "" {
		t.Errorf("Text() = %q, want cleared", got)
	}
}

func TestCancelDuringFailedDeliveryDoesNotRestoreText(t *testing.T) {
	h := newHarness(t)
	h.deliverer.err = errors.New("paste failed")
	h.reachReady(t, "text")
	h.deliverer.onCall = func() { h.controller.Cancel() }

	// The failure must not drag a cancelled session back into Ready.
	_ = h.controller.Deliver(false)

	if got := h.controller.State(); got != ReviewIdle {
		t.Errorf("state = %s, want idle after cancel", got)
	}
	if got := h.controller.Text(); got != "" {
		t.Errorf("Text() = %q, want cancelled text to stay gone", got)
	}
}

func TestIgnoredGesturesDoNotChangeState(t *testing.T) {
	h := newHarness(t)

	// A release with nothing recording is meaningless.
	if got := h.controller.Handle(InputRelease); got != ReviewIdle {
		t.Errorf("state = %s, want idle", got)
	}
	if started, _, _ := h.recognizer.counts(); started != 0 {
		t.Errorf("started %d captures, want 0", started)
	}

	// A press while finalizing must not start a competing capture.
	h.controller.Handle(InputPress)
	h.controller.Handle(InputRelease)
	if got := h.controller.Handle(InputPress); got != ReviewFinalizing {
		t.Errorf("state = %s, want finalizing", got)
	}
	if started, _, _ := h.recognizer.counts(); started != 1 {
		t.Errorf("started %d captures, want 1", started)
	}
}

func TestInvalidEventIgnored(t *testing.T) {
	h := newHarness(t)
	if got := h.controller.Handle(InputEvent(inputEventCount)); got != ReviewIdle {
		t.Errorf("state = %s, want idle", got)
	}
	if started, _, _ := h.recognizer.counts(); started != 0 {
		t.Errorf("started %d captures for an invalid event, want 0", started)
	}
}

func TestToggleDrivesRecordingAndFinalization(t *testing.T) {
	h := newHarness(t)

	if got := h.controller.Handle(InputToggle); got != ReviewRecording {
		t.Fatalf("state = %s, want recording", got)
	}
	if got := h.controller.Handle(InputToggle); got != ReviewFinalizing {
		t.Fatalf("state = %s, want finalizing", got)
	}

	h.controller.OnResult(h.controller.SessionID(), "toggled text")
	if got := h.controller.Text(); got != "toggled text" {
		t.Errorf("Text() = %q, want toggled text", got)
	}
}

func TestResultInUnexpectedStateIgnored(t *testing.T) {
	h := newHarness(t)
	id := h.reachReady(t, "held text")

	// Ready is not awaiting a transcription; a result here is spurious.
	h.controller.OnResult(id, "unexpected")

	if got := h.controller.Text(); got != "held text" {
		t.Errorf("Text() = %q, want the held text untouched", got)
	}
}

func TestConcurrentCallbacksAndCancels(t *testing.T) {
	h := newHarness(t)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(4)
		go func() { defer wg.Done(); h.controller.Handle(InputPress) }()
		go func() { defer wg.Done(); h.controller.Handle(InputRelease) }()
		go func() { defer wg.Done(); h.controller.OnResult(h.controller.SessionID(), "text") }()
		go func() { defer wg.Done(); h.controller.Cancel() }()
	}
	wg.Wait()

	if got := h.controller.State(); !got.Valid() {
		t.Errorf("state = %v, want a defined state after concurrent use", got)
	}
}

// applyEdit drives a Ready controller through one edit, returning to Ready.
func (h *harness) applyEdit(t *testing.T, instruction string) {
	t.Helper()
	h.controller.Handle(InputPress)
	h.controller.Handle(InputRelease)
	h.controller.OnResult(h.controller.SessionID(), instruction)
	if got := h.controller.State(); got != ReviewReady {
		t.Fatalf("state = %s after the edit, want ready", got)
	}
}

func TestEditNeverAutoDelivers(t *testing.T) {
	h := newHarness(t)
	h.editor.autoBack = h.controller
	h.editor.reply = "revised text"
	h.reachReady(t, "original text")

	h.applyEdit(t, "revise it")

	// An edit returns to Ready for another look; it must not insert anything.
	if got := h.deliverer.delivered(); len(got) != 0 {
		t.Fatalf("delivered %v after an edit, want nothing until asked", got)
	}
	if got := h.controller.Text(); got != "revised text" {
		t.Errorf("Text() = %q, want the revised text held for review", got)
	}
}

func TestUndoRestoresTextFromBeforeTheEdit(t *testing.T) {
	h := newHarness(t)
	h.editor.autoBack = h.controller
	h.editor.reply = "an unwanted revision"
	h.reachReady(t, "the original text")

	if h.controller.CanUndoEdit() {
		t.Error("CanUndoEdit() = true before any edit, want false")
	}

	h.applyEdit(t, "revise it")
	if !h.controller.CanUndoEdit() {
		t.Fatal("CanUndoEdit() = false after an edit, want true")
	}

	if !h.controller.UndoEdit() {
		t.Fatal("UndoEdit() = false, want the revision undone")
	}
	if got := h.controller.Text(); got != "the original text" {
		t.Errorf("Text() = %q, want the pre-edit text restored", got)
	}
	// Only one revision is kept.
	if h.controller.CanUndoEdit() {
		t.Error("CanUndoEdit() = true after undoing, want false")
	}
	if h.controller.UndoEdit() {
		t.Error("UndoEdit() = true on a second attempt, want false")
	}
}

func TestUndoPresentsTheRestoredText(t *testing.T) {
	h := newHarness(t)
	h.editor.autoBack = h.controller
	h.editor.reply = "revised"
	h.reachReady(t, "original")
	h.applyEdit(t, "revise it")

	h.controller.UndoEdit()

	_, reviewed, _ := h.presenter.snapshot()
	if len(reviewed) == 0 || reviewed[len(reviewed)-1] != "original" {
		t.Errorf("presented %v, want the restored text shown last", reviewed)
	}
}

func TestUndoRejectedOutsideReady(t *testing.T) {
	h := newHarness(t)
	h.editor.autoBack = h.controller
	h.editor.reply = "revised"
	h.reachReady(t, "original")
	h.applyEdit(t, "revise it")

	// Recording again leaves Ready, so undo no longer applies.
	h.controller.Handle(InputPress)
	if h.controller.CanUndoEdit() {
		t.Error("CanUndoEdit() = true outside ready, want false")
	}
	if h.controller.UndoEdit() {
		t.Error("UndoEdit() = true outside ready, want false")
	}
}

func TestRevisionHistoryClearedByNewDictation(t *testing.T) {
	h := newHarness(t)
	h.editor.autoBack = h.controller
	h.editor.reply = "revised"
	h.reachReady(t, "original")
	h.applyEdit(t, "revise it")

	// Delivering ends the session, so the next dictation starts clean.
	if err := h.controller.Deliver(false); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	h.reachReady(t, "a new dictation")

	if h.controller.CanUndoEdit() {
		t.Error("CanUndoEdit() = true for a fresh dictation, want false")
	}
}

func TestRevisionHistoryClearedByCancel(t *testing.T) {
	h := newHarness(t)
	h.editor.autoBack = h.controller
	h.editor.reply = "revised"
	h.reachReady(t, "original")
	h.applyEdit(t, "revise it")

	h.controller.Cancel()
	h.reachReady(t, "a new dictation")

	if h.controller.CanUndoEdit() {
		t.Error("CanUndoEdit() = true after a cancel, want false")
	}
	if got := h.controller.Text(); got != "a new dictation" {
		t.Errorf("Text() = %q, want the new dictation", got)
	}
}

func TestSecondEditReplacesTheRetainedRevision(t *testing.T) {
	h := newHarness(t)
	h.editor.autoBack = h.controller
	h.reachReady(t, "first")

	h.editor.mu.Lock()
	h.editor.reply = "second"
	h.editor.mu.Unlock()
	h.applyEdit(t, "revise it")

	h.editor.mu.Lock()
	h.editor.reply = "third"
	h.editor.mu.Unlock()
	h.applyEdit(t, "revise it again")

	// Only one revision back is kept, so undo lands on the second text.
	if !h.controller.UndoEdit() {
		t.Fatal("UndoEdit() = false, want the last revision undone")
	}
	if got := h.controller.Text(); got != "second" {
		t.Errorf("Text() = %q, want the immediately preceding text", got)
	}
}
