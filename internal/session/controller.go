package session

import (
	"log/slog"
	"sync"
)

// ReviewState is the lifecycle of a review-mode dictation. Immediate mode does
// not use it and keeps the simpler State above.
type ReviewState uint8

const (
	// ReviewIdle is the resting state: nothing recorded, nothing pending.
	ReviewIdle ReviewState = iota
	// ReviewRecording is capturing speech, possibly showing partial text.
	ReviewRecording
	// ReviewFinalizing is running final transcription and cleanup.
	ReviewFinalizing
	// ReviewReady holds finished text awaiting delivery, editing, or cancel.
	ReviewReady
	// ReviewEditing is capturing a spoken instruction to revise the text.
	ReviewEditing
	// ReviewApplyingEdit is rewriting the held text from that instruction.
	ReviewApplyingEdit
	// ReviewDelivering is inserting the text into the focused window.
	ReviewDelivering
	reviewStateCount
)

// Valid reports whether state is a defined review state.
func (state ReviewState) Valid() bool { return state < reviewStateCount }

func (state ReviewState) String() string {
	switch state {
	case ReviewIdle:
		return "idle"
	case ReviewRecording:
		return "recording"
	case ReviewFinalizing:
		return "finalizing"
	case ReviewReady:
		return "ready"
	case ReviewEditing:
		return "editing"
	case ReviewApplyingEdit:
		return "applying-edit"
	case ReviewDelivering:
		return "delivering"
	default:
		return "invalid"
	}
}

// SessionID identifies one review session. It increases monotonically, so a
// late callback can be matched against the session that is current now.
type SessionID uint64

// Recognizer drives capture and transcription for the controller. Calls are
// made from the controller's goroutine and must not block.
type Recognizer interface {
	// StartCapture begins recording for the given session.
	StartCapture(id SessionID)
	// StopCapture ends recording and starts final transcription. The result
	// is expected via Controller.OnResult.
	StopCapture(id SessionID)
	// CancelCapture abandons any capture or transcription in flight.
	CancelCapture(id SessionID)
}

// Editor revises held text from a spoken instruction. The result is expected
// via Controller.OnEdited.
type Editor interface {
	ApplyEdit(id SessionID, text, instruction string)
}

// Deliverer inserts reviewed text into the focused window. It reports whether
// delivery succeeded; on failure the controller keeps the text in Ready so it
// is never lost.
type Deliverer interface {
	Deliver(text string, submit bool) error
}

// Presenter renders review state for the user. Implementations must be
// non-blocking.
type Presenter interface {
	OnReviewState(state ReviewState)
	OnPartialText(text string)
	OnReviewText(text string)
	OnDeliveryError(err error)
}

// Controller is the review-mode state machine. It is platform-neutral: input
// gestures, transcription, editing, delivery, and presentation all arrive
// through adapters.
//
// Behavioral reference: flt-james/master cmd/sussurro-stream/main.go
// (7c9c12e), which guards async completions by comparing the current state
// only. That cannot tell "still editing" from "editing again in a new
// session", so a stale callback can resurrect cancelled text. Every
// asynchronous entry point here is keyed by SessionID instead.
type Controller struct {
	recognizer Recognizer
	editor     Editor
	deliverer  Deliverer
	presenter  Presenter
	log        *slog.Logger

	mu      sync.Mutex
	state   ReviewState
	current SessionID
	// text is the reviewed text held in Ready and beyond.
	text string
	// previous is the text as it stood before the most recent edit, kept so
	// an unwanted revision can be undone without re-dictating.
	previous string
	// hasPrevious distinguishes "no edit yet" from "the previous text was
	// legitimately empty".
	hasPrevious bool
	// instruction is the spoken edit captured in Editing.
	instruction string
}

// NewController builds a review controller. The presenter may be nil for
// headless use; the other adapters are required.
func NewController(
	recognizer Recognizer,
	editor Editor,
	deliverer Deliverer,
	presenter Presenter,
	log *slog.Logger,
) *Controller {
	return &Controller{
		recognizer: recognizer,
		editor:     editor,
		deliverer:  deliverer,
		presenter:  presenter,
		log:        log,
	}
}

// State returns the current review state.
func (c *Controller) State() ReviewState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// SessionID returns the current session identifier.
func (c *Controller) SessionID() SessionID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

// Text returns the reviewed text currently held.
func (c *Controller) Text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text
}

// setState updates the state and notifies the presenter. Caller holds c.mu;
// the notification is deferred to the caller to avoid presenting under lock.
func (c *Controller) setState(state ReviewState) func() {
	c.state = state
	if c.presenter == nil {
		return func() {}
	}
	return func() { c.presenter.OnReviewState(state) }
}

// Handle applies an input gesture to the current state and returns the
// resulting state.
func (c *Controller) Handle(event InputEvent) ReviewState {
	switch event {
	case InputPress:
		return c.press()
	case InputRelease:
		return c.release()
	case InputToggle:
		return c.toggle()
	default:
		c.log.Debug("Ignoring invalid input event", "event", event)
		return c.State()
	}
}

// press starts a recording, or starts capturing an edit instruction when text
// is already held for review.
func (c *Controller) press() ReviewState {
	c.mu.Lock()

	var notify func()
	switch c.state {
	case ReviewIdle:
		c.current++
		c.text = ""
		c.previous = ""
		c.hasPrevious = false
		c.instruction = ""
		notify = c.setState(ReviewRecording)
		id := c.current
		c.mu.Unlock()
		notify()
		c.recognizer.StartCapture(id)
		return ReviewRecording

	case ReviewReady:
		// Holding the gesture over ready text records a revision instead of
		// starting a fresh dictation.
		c.current++
		c.instruction = ""
		notify = c.setState(ReviewEditing)
		id := c.current
		c.mu.Unlock()
		notify()
		c.recognizer.StartCapture(id)
		return ReviewEditing

	default:
		state := c.state
		c.mu.Unlock()
		c.log.Debug("Ignoring press", "state", state)
		return state
	}
}

// release ends a recording or an edit instruction and begins the async work.
func (c *Controller) release() ReviewState {
	c.mu.Lock()

	switch c.state {
	case ReviewRecording:
		notify := c.setState(ReviewFinalizing)
		id := c.current
		c.mu.Unlock()
		notify()
		c.recognizer.StopCapture(id)
		return ReviewFinalizing

	case ReviewEditing:
		notify := c.setState(ReviewApplyingEdit)
		id := c.current
		c.mu.Unlock()
		notify()
		c.recognizer.StopCapture(id)
		return ReviewApplyingEdit

	default:
		state := c.state
		c.mu.Unlock()
		c.log.Debug("Ignoring release", "state", state)
		return state
	}
}

// toggle starts a recording when idle and ends it when recording, so a single
// gesture can drive the whole flow.
func (c *Controller) toggle() ReviewState {
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()

	switch state {
	case ReviewRecording, ReviewEditing:
		return c.release()
	default:
		return c.press()
	}
}

// OnPartial presents partial text for the session it belongs to. Partials from
// superseded sessions are dropped.
func (c *Controller) OnPartial(id SessionID, text string) {
	c.mu.Lock()
	if id != c.current || (c.state != ReviewRecording && c.state != ReviewEditing) {
		state := c.state
		c.mu.Unlock()
		c.log.Debug("Discarding stale partial", "session", id, "state", state)
		return
	}
	c.mu.Unlock()

	if c.presenter != nil {
		c.presenter.OnPartialText(text)
	}
}

// OnResult accepts a completed transcription. In Finalizing it becomes the
// reviewed text; in ApplyingEdit it is the spoken instruction, which is handed
// to the editor. Results from superseded or cancelled sessions are dropped.
func (c *Controller) OnResult(id SessionID, text string) {
	c.mu.Lock()

	if id != c.current {
		state := c.state
		c.mu.Unlock()
		c.log.Debug("Discarding stale result", "session", id, "state", state)
		return
	}

	switch c.state {
	case ReviewFinalizing:
		c.text = text
		notify := c.setState(ReviewReady)
		reviewed := c.text
		c.mu.Unlock()
		notify()
		if c.presenter != nil {
			c.presenter.OnReviewText(reviewed)
		}

	case ReviewApplyingEdit:
		c.instruction = text
		current, instruction := c.text, c.instruction
		c.mu.Unlock()
		// An empty instruction cannot revise anything; keep the text as-is.
		if instruction == "" {
			c.finishEdit(id, current)
			return
		}
		c.editor.ApplyEdit(id, current, instruction)

	default:
		state := c.state
		c.mu.Unlock()
		c.log.Debug("Discarding result for unexpected state", "session", id, "state", state)
	}
}

// OnEdited accepts revised text from the editor. Edits from superseded or
// cancelled sessions are dropped.
func (c *Controller) OnEdited(id SessionID, text string) {
	c.finishEdit(id, text)
}

// finishEdit installs revised text and returns to Ready if the session is
// still current.
func (c *Controller) finishEdit(id SessionID, text string) {
	c.mu.Lock()
	if id != c.current || c.state != ReviewApplyingEdit {
		state := c.state
		c.mu.Unlock()
		c.log.Debug("Discarding stale edit", "session", id, "state", state)
		return
	}

	// Keep the pre-edit text so an unwanted revision can be undone.
	c.previous = c.text
	c.hasPrevious = true
	c.text = text
	notify := c.setState(ReviewReady)
	reviewed := c.text
	c.mu.Unlock()

	notify()
	if c.presenter != nil {
		c.presenter.OnReviewText(reviewed)
	}
}

// CanUndoEdit reports whether a revision is available to undo.
func (c *Controller) CanUndoEdit() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hasPrevious && c.state == ReviewReady
}

// UndoEdit restores the text as it stood before the most recent edit. Only one
// revision is kept, so a second undo does nothing. Valid only in Ready.
func (c *Controller) UndoEdit() bool {
	c.mu.Lock()
	if c.state != ReviewReady || !c.hasPrevious {
		c.mu.Unlock()
		return false
	}

	c.text = c.previous
	c.previous = ""
	c.hasPrevious = false
	restored := c.text
	c.mu.Unlock()

	if c.presenter != nil {
		c.presenter.OnReviewText(restored)
	}
	return true
}

// Deliver inserts the reviewed text. When submit is true the delivery backend
// also sends Enter. Delivery is only valid from Ready; on failure the text is
// kept in Ready so it is never lost.
func (c *Controller) Deliver(submit bool) error {
	c.mu.Lock()
	if c.state != ReviewReady {
		state := c.state
		c.mu.Unlock()
		c.log.Debug("Ignoring deliver", "state", state)
		return nil
	}

	notify := c.setState(ReviewDelivering)
	id, text := c.current, c.text
	c.mu.Unlock()
	notify()

	err := c.deliverer.Deliver(text, submit)

	c.mu.Lock()
	// A cancel during delivery must not drag the controller back out of Idle.
	if id != c.current || c.state != ReviewDelivering {
		state := c.state
		c.mu.Unlock()
		c.log.Debug("Discarding delivery outcome for superseded session", "session", id, "state", state)
		return err
	}

	if err != nil {
		// Keep the text reviewable rather than losing it to a failed paste.
		notify = c.setState(ReviewReady)
		c.mu.Unlock()
		notify()
		if c.presenter != nil {
			c.presenter.OnDeliveryError(err)
		}
		return err
	}

	c.text = ""
	c.previous = ""
	c.hasPrevious = false
	c.instruction = ""
	notify = c.setState(ReviewIdle)
	c.mu.Unlock()
	notify()
	return nil
}

// Cancel abandons the session from any state, discarding held text. Bumping
// the session ID means every callback still in flight is ignored.
func (c *Controller) Cancel() {
	c.mu.Lock()

	if c.state == ReviewIdle {
		c.mu.Unlock()
		return
	}

	id := c.current
	c.current++
	c.text = ""
	c.previous = ""
	c.hasPrevious = false
	c.instruction = ""
	notify := c.setState(ReviewIdle)
	c.mu.Unlock()

	c.recognizer.CancelCapture(id)
	notify()
}

// InputDispatcher routes a gesture to whichever workflow is active. It lets
// callers wire input once instead of branching on interaction mode at every
// hotkey, trigger, and adapter call site.
type InputDispatcher interface {
	// Dispatch applies event and reports whether the gesture ended a
	// recording, which callers use for user-facing progress messages.
	Dispatch(event InputEvent) (recordingStopped bool)
}

// immediateDispatcher drives the unchanged immediate-mode recorder.
type immediateDispatcher struct{ recorder Recorder }

func (d immediateDispatcher) Dispatch(event InputEvent) bool {
	return DispatchImmediateInput(d.recorder, event)
}

// NewImmediateDispatcher returns the dispatcher for immediate mode.
func NewImmediateDispatcher(recorder Recorder) InputDispatcher {
	return immediateDispatcher{recorder: recorder}
}

// Dispatch implements InputDispatcher for review mode. A gesture that leaves
// the controller finalizing or applying an edit is reported as having stopped
// a recording.
func (c *Controller) Dispatch(event InputEvent) bool {
	state := c.Handle(event)
	return state == ReviewFinalizing || state == ReviewApplyingEdit
}
