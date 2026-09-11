package session

import (
	"errors"
	"log/slog"
	"sync"
	"time"
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
	// StartCapture begins recording for the given session. It reports whether
	// recording actually started; false leaves the controller where it was,
	// so a press during in-flight transcription cannot open a session that
	// will never see a result.
	StartCapture(id SessionID) bool
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

// tapThreshold separates a tap on the push-to-talk key over ready text, which
// delivers it, from a hold, which records a revision instruction. It matches
// the "tap to deliver" hint the overlay shows.
const tapThreshold = 250 * time.Millisecond

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

	// inputMu keeps state transitions and their recognizer side effects in
	// gesture order. Recognizer callbacks only take mu, so StartCapture and
	// StopCapture may call back synchronously without deadlocking.
	inputMu sync.Mutex
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
	// editOwner identifies which press entered Editing. Only its matching
	// release may stop that capture.
	editOwner editGestureOwner
	// editPressedAt is when the legacy press entered Editing, so its release
	// can tell a tap from a hold.
	editPressedAt time.Time

	// spawn runs work that must not block the gesture source, such as a
	// delivery that waits for key release and types into another window.
	spawn func(func())
	// now is the clock the tap threshold is measured against.
	now func() time.Time
}

type editGestureOwner uint8

const (
	editGestureNone editGestureOwner = iota
	editGestureLegacy
	editGestureDedicated
)

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
		spawn:      func(work func()) { go work() },
		now:        time.Now,
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
	c.inputMu.Lock()
	state, after := c.handle(event)
	c.inputMu.Unlock()
	if after != nil {
		after()
	}
	return state
}

// handle applies event under inputMu. The returned function, if any, is work
// the gesture requested that must run once inputMu is released, so a
// delivery started by a tap cannot hold up the next gesture or a cancel.
func (c *Controller) handle(event InputEvent) (ReviewState, func()) {
	switch event {
	case InputPress:
		return c.press(), nil
	case InputRelease:
		return c.release()
	case InputToggle:
		return c.toggle()
	case InputEditPress:
		return c.editPress(), nil
	case InputEditRelease:
		return c.editRelease(), nil
	default:
		c.log.Debug("Ignoring invalid input event", "event", event)
		return c.State(), nil
	}
}

// startCapture asks the recognizer to record for a new session while inputMu
// is held. It returns the session to transition into, or false when recording
// could not start — typically because the previous transcription is still in
// flight — in which case the controller stays where it is.
func (c *Controller) startCapture() (SessionID, bool) {
	c.mu.Lock()
	c.current++
	id := c.current
	c.mu.Unlock()

	if !c.recognizer.StartCapture(id) {
		c.log.Debug("Capture did not start", "session", id)
		return id, false
	}
	return id, true
}

// press starts a recording, or starts capturing an edit instruction when text
// is already held for review.
func (c *Controller) press() ReviewState {
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()

	switch state {
	case ReviewIdle:
		id, ok := c.startCapture()
		if !ok {
			return c.State()
		}

		c.mu.Lock()
		// The capture succeeded, but a callback may have moved on meanwhile.
		if c.state != ReviewIdle || c.current != id {
			state := c.state
			c.mu.Unlock()
			c.recognizer.CancelCapture(id)
			return state
		}
		c.text = ""
		c.previous = ""
		c.hasPrevious = false
		c.instruction = ""
		c.editOwner = editGestureNone
		notify := c.setState(ReviewRecording)
		c.mu.Unlock()
		notify()
		return ReviewRecording

	case ReviewReady:
		// Holding the gesture over ready text records a revision instead of
		// starting a fresh dictation; a tap delivers it (see release).
		id, ok := c.startCapture()
		if !ok {
			return c.State()
		}

		c.mu.Lock()
		if c.state != ReviewReady || c.current != id {
			state := c.state
			c.mu.Unlock()
			c.recognizer.CancelCapture(id)
			return state
		}
		c.instruction = ""
		c.editOwner = editGestureLegacy
		c.editPressedAt = c.now()
		notify := c.setState(ReviewEditing)
		c.mu.Unlock()
		notify()
		return ReviewEditing

	default:
		c.log.Debug("Ignoring press", "state", state)
		return state
	}
}

// editPress starts an edit capture only when reviewed text is ready. Unlike
// the legacy overloaded press gesture, it is inert in Idle and every other
// state, so a dedicated edit key cannot begin an ordinary dictation.
func (c *Controller) editPress() ReviewState {
	c.mu.Lock()
	if c.state != ReviewReady {
		state := c.state
		c.mu.Unlock()
		c.log.Debug("Ignoring edit press", "state", state)
		return state
	}
	c.mu.Unlock()

	id, ok := c.startCapture()
	if !ok {
		return c.State()
	}

	c.mu.Lock()
	if c.state != ReviewReady || c.current != id {
		state := c.state
		c.mu.Unlock()
		c.recognizer.CancelCapture(id)
		return state
	}
	c.instruction = ""
	c.editOwner = editGestureDedicated
	notify := c.setState(ReviewEditing)
	c.mu.Unlock()

	notify()
	return ReviewEditing
}

// editRelease ends only an edit capture. A stray edit release cannot stop a
// normal dictation or move any other review state forward.
func (c *Controller) editRelease() ReviewState {
	c.mu.Lock()
	if c.state != ReviewEditing || c.editOwner != editGestureDedicated {
		state := c.state
		owner := c.editOwner
		c.mu.Unlock()
		c.log.Debug("Ignoring edit release", "state", state, "owner", owner)
		return state
	}

	c.editOwner = editGestureNone
	notify := c.setState(ReviewApplyingEdit)
	id := c.current
	c.mu.Unlock()

	notify()
	c.recognizer.StopCapture(id)
	return ReviewApplyingEdit
}

// release ends a recording or an edit instruction and begins the async work.
// The returned function, if any, delivers the held text after inputMu is
// released: a tap on the push-to-talk key over ready text delivers rather
// than editing.
func (c *Controller) release() (ReviewState, func()) {
	c.mu.Lock()

	switch c.state {
	case ReviewRecording:
		notify := c.setState(ReviewFinalizing)
		id := c.current
		c.mu.Unlock()
		notify()
		c.recognizer.StopCapture(id)
		return ReviewFinalizing, nil

	case ReviewEditing:
		if c.editOwner != editGestureLegacy {
			state := c.state
			owner := c.editOwner
			c.mu.Unlock()
			c.log.Debug("Ignoring release", "state", state, "owner", owner)
			return state, nil
		}
		c.editOwner = editGestureNone
		id := c.current
		if c.now().Sub(c.editPressedAt) < tapThreshold {
			// Too brief to hold an instruction: the user tapped to deliver.
			// The capture is abandoned and the text goes back to Ready so
			// Deliver finds it there.
			notify := c.setState(ReviewReady)
			c.mu.Unlock()
			c.recognizer.CancelCapture(id)
			notify()
			return ReviewReady, func() {
				c.spawn(func() {
					// Backend failures are already presented by Deliver;
					// ErrNothingToDeliver means a newer gesture won the race.
					if err := c.Deliver(false); err != nil {
						c.log.Debug("Tap delivery did not complete", "error", err)
					}
				})
			}
		}
		notify := c.setState(ReviewApplyingEdit)
		c.mu.Unlock()
		notify()
		c.recognizer.StopCapture(id)
		return ReviewApplyingEdit, nil

	default:
		// Ready is reached without a release when the recording hit the
		// duration cap while the key was still held; that release is spent.
		state := c.state
		c.mu.Unlock()
		c.log.Debug("Ignoring release", "state", state)
		return state, nil
	}
}

// toggle starts a recording when idle and ends it when recording, so a single
// gesture can drive the whole flow.
func (c *Controller) toggle() (ReviewState, func()) {
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()

	switch state {
	case ReviewRecording, ReviewEditing:
		return c.release()
	default:
		return c.press(), nil
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
//
// Recording and Editing are accepted too: the pipeline finalises on its own
// when a recording hits the duration cap, so the result can arrive while the
// key is still held. The release that follows then finds nothing to stop.
//
// An empty result means the pipeline published nothing — the recording was
// too short, silent, or recognition failed. There is no text to review, so
// the session ends rather than waiting in Finalizing for a result that will
// never come.
func (c *Controller) OnResult(id SessionID, text string) {
	c.mu.Lock()

	if id != c.current {
		state := c.state
		c.mu.Unlock()
		c.log.Debug("Discarding stale result", "session", id, "state", state)
		return
	}

	switch c.state {
	case ReviewRecording, ReviewFinalizing:
		if text == "" {
			c.text = ""
			notify := c.setState(ReviewIdle)
			c.mu.Unlock()
			c.log.Debug("No text to review", "session", id)
			notify()
			return
		}
		c.text = text
		notify := c.setState(ReviewReady)
		reviewed := c.text
		c.mu.Unlock()
		notify()
		if c.presenter != nil {
			c.presenter.OnReviewText(reviewed)
		}

	case ReviewEditing, ReviewApplyingEdit:
		// A capped edit capture leaves Editing without its release; the
		// release must then not stop a capture that is already over.
		c.editOwner = editGestureNone
		c.instruction = text
		current, instruction := c.text, c.instruction
		notify := func() {}
		if c.state == ReviewEditing {
			notify = c.setState(ReviewApplyingEdit)
		}
		c.mu.Unlock()
		notify()
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

// ErrNothingToDeliver reports that delivery was requested outside Ready, so
// there was no reviewed text to insert. It is not a failure: callers use it to
// avoid reporting a delivery that never happened.
var ErrNothingToDeliver = errors.New("no reviewed text to deliver")

// Deliver inserts the reviewed text. When submit is true the delivery backend
// also sends Enter. Delivery is only valid from Ready; outside it the call is
// a no-op reporting ErrNothingToDeliver. On backend failure the text is kept
// in Ready so it is never lost.
func (c *Controller) Deliver(submit bool) error {
	c.mu.Lock()
	if c.state != ReviewReady {
		state := c.state
		c.mu.Unlock()
		c.log.Debug("Ignoring deliver", "state", state)
		return ErrNothingToDeliver
	}

	notify := c.setState(ReviewDelivering)
	text := c.text
	c.mu.Unlock()
	notify()

	err := c.deliverer.Deliver(text, submit)

	c.mu.Lock()
	// A cancel during delivery must not drag the controller back out of Idle.
	// The check is on state alone: every takeover that matters (Cancel) leaves
	// Delivering, whereas a refused press bumps the session id without
	// changing state, and nothing else would ever leave Delivering then.
	if c.state != ReviewDelivering {
		state := c.state
		c.mu.Unlock()
		c.log.Debug("Discarding delivery outcome for superseded session", "state", state)
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
	c.inputMu.Lock()
	defer c.inputMu.Unlock()
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
	c.editOwner = editGestureNone
	notify := c.setState(ReviewIdle)
	c.mu.Unlock()

	c.recognizer.CancelCapture(id)
	notify()
}

// InputDispatcher routes a gesture to whichever workflow is active. It lets
// callers wire input once instead of branching on interaction mode at every
// hotkey, trigger, and adapter call site.
type InputDispatcher interface {
	// Dispatch applies event and reports whether it started, stopped, or was
	// ignored. Trigger protocol replies depend on the distinction.
	Dispatch(event InputEvent) InputOutcome
}

// immediateDispatcher drives the unchanged immediate-mode recorder.
type immediateDispatcher struct{ recorder Recorder }

func (d immediateDispatcher) Dispatch(event InputEvent) InputOutcome {
	return dispatchImmediateInput(d.recorder, event)
}

// NewImmediateDispatcher returns the dispatcher for immediate mode.
func NewImmediateDispatcher(recorder Recorder) InputDispatcher {
	return immediateDispatcher{recorder: recorder}
}

// Dispatch implements InputDispatcher for review mode.
func (c *Controller) Dispatch(event InputEvent) InputOutcome {
	c.inputMu.Lock()
	before := c.State()
	after, work := c.handle(event)
	c.inputMu.Unlock()
	if work != nil {
		work()
	}

	if after == before {
		return InputIgnored
	}
	switch after {
	case ReviewRecording, ReviewEditing:
		return InputStarted
	case ReviewFinalizing, ReviewApplyingEdit:
		return InputStopped
	default:
		return InputIgnored
	}
}
