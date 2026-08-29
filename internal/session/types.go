// Package session defines platform-neutral application states and input events.
package session

// State represents the user-visible lifecycle of the current dictation.
type State uint8

const (
	StateIdle State = iota
	StateRecording
	// StateTranscribing covers ASR only. It must not be used for the work
	// that follows recognition, which is what StateCleaningUp names.
	StateTranscribing
	// StateCleaningUp covers everything after recognition: filler removal by
	// the LLM, context lookup, and delivery. Reusing StateTranscribing here
	// misreported the reuse path, which runs no ASR at all.
	StateCleaningUp
	stateCount
)

// Valid reports whether state is a defined application state.
func (state State) Valid() bool {
	return state < stateCount
}

func (state State) String() string {
	switch state {
	case StateIdle:
		return "idle"
	case StateRecording:
		return "recording"
	case StateTranscribing:
		return "transcribing"
	case StateCleaningUp:
		return "cleaning up"
	default:
		return "invalid"
	}
}

// InputEvent represents a high-level recording gesture independently of the
// hotkey, compositor trigger, or future input adapter that produced it.
type InputEvent uint8

const (
	InputPress InputEvent = iota
	InputRelease
	InputToggle
	// InputEditPress and InputEditRelease are dedicated review-edit gestures.
	// Immediate mode ignores them, so an edit binding can never start an
	// ordinary dictation while no reviewed text is waiting.
	InputEditPress
	InputEditRelease
	inputEventCount
)

// Valid reports whether event is a defined input event.
func (event InputEvent) Valid() bool {
	return event < inputEventCount
}

func (event InputEvent) String() string {
	switch event {
	case InputPress:
		return "press"
	case InputRelease:
		return "release"
	case InputToggle:
		return "toggle"
	case InputEditPress:
		return "edit-press"
	case InputEditRelease:
		return "edit-release"
	default:
		return "invalid"
	}
}

// InputOutcome reports what a dispatched gesture actually did.
type InputOutcome uint8

const (
	InputIgnored InputOutcome = iota
	InputStarted
	InputStopped
)

// Stopped reports whether the gesture ended a capture.
func (outcome InputOutcome) Stopped() bool { return outcome == InputStopped }

// Recorder is the immediate-mode recording control consumed by input events.
type Recorder interface {
	StartRecording()
	StopRecording() bool
}

// DispatchImmediateInput applies an input event to the current immediate-mode
// recording controls. The return value reports whether a recording was
// stopped. Invalid events are rejected without invoking the recorder.
func DispatchImmediateInput(recorder Recorder, event InputEvent) (recordingStopped bool) {
	return dispatchImmediateInput(recorder, event).Stopped()
}

func dispatchImmediateInput(recorder Recorder, event InputEvent) InputOutcome {
	switch event {
	case InputPress:
		recorder.StartRecording()
		return InputStarted
	case InputRelease:
		if recorder.StopRecording() {
			return InputStopped
		}
	case InputToggle:
		if recorder.StopRecording() {
			return InputStopped
		}
		recorder.StartRecording()
		return InputStarted
	}
	return InputIgnored
}
