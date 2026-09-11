package pipeline

import (
	"log/slog"
	"sync"

	"github.com/aploide/sussurro/internal/session"
)

// SessionRecognizer adapts the pipeline to the review controller's Recognizer.
// It tags every recognition with the session that requested it, so results
// arriving after a cancellation are discarded by the controller rather than
// resurrecting an abandoned session.
type SessionRecognizer struct {
	pipeline *Pipeline
	log      *slog.Logger

	mu sync.Mutex
	// active is the session currently capturing, if any.
	active session.SessionID
	// capturing distinguishes "session 0 is active" from "nothing active".
	capturing bool

	// onResult receives the tagged final transcription.
	onResult func(id session.SessionID, text string)
}

// NewSessionRecognizer wires a pipeline to a review controller. Install the
// returned recognizer as the controller's Recognizer, and route the pipeline's
// results through Consume.
//
// The pipeline publishes nothing for a recording that is too short, silent,
// or fails recognition, so the recognizer also hooks the pipeline's
// completion: a pass that ends without a result reports an empty one, and the
// controller ends the session instead of waiting for text that never comes.
func NewSessionRecognizer(pipe *Pipeline, onResult func(id session.SessionID, text string), log *slog.Logger) *SessionRecognizer {
	r := &SessionRecognizer{pipeline: pipe, onResult: onResult, log: log}
	pipe.SetOnCompletion(r.onCompleted)
	return r
}

// StartCapture begins recording for the given session. It reports false, and
// leaves no session active, when the pipeline refuses to record — typically
// because the previous recording is still being transcribed.
func (r *SessionRecognizer) StartCapture(id session.SessionID) bool {
	if !r.pipeline.StartRecording() {
		r.log.Debug("Pipeline refused to start recording", "session", id)
		return false
	}

	r.mu.Lock()
	r.active = id
	r.capturing = true
	r.mu.Unlock()
	return true
}

// Active returns the session currently capturing or awaiting its result, and
// whether there is one. Partial transcriptions are attributed to it: the
// streamer's own generation counter advances on a different schedule and does
// not identify controller sessions.
func (r *SessionRecognizer) Active() (session.SessionID, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active, r.capturing
}

// StopCapture ends recording and lets final transcription run. The session
// stays active so its result can be tagged when it arrives.
func (r *SessionRecognizer) StopCapture(id session.SessionID) {
	r.mu.Lock()
	if !r.capturing || r.active != id {
		r.mu.Unlock()
		r.log.Debug("Ignoring stop for inactive session", "session", id)
		return
	}
	r.mu.Unlock()

	r.pipeline.StopRecording()
}

// CancelCapture abandons the session. Any transcription already running still
// completes, but Consume will no longer attribute it to a live session.
func (r *SessionRecognizer) CancelCapture(id session.SessionID) {
	r.mu.Lock()
	if !r.capturing || r.active != id {
		r.mu.Unlock()
		return
	}
	r.capturing = false
	r.mu.Unlock()

	// Discard the buffered audio if we are still recording; a transcription
	// already in flight is left to finish and be dropped on arrival.
	r.pipeline.StopRecording()
}

// Consume implements ResultConsumer, tagging each result with the session that
// requested it. Results with no active session are dropped.
func (r *SessionRecognizer) Consume(result Result) {
	r.mu.Lock()
	id, capturing := r.active, r.capturing
	// One result per capture: further results need a new StartCapture.
	r.capturing = false
	r.mu.Unlock()

	if !capturing {
		r.log.Debug("Discarding result with no active session")
		return
	}
	if r.onResult != nil {
		r.onResult(id, result.Text)
	}
}

// OnResult implements ResultConsumer.
func (r *SessionRecognizer) OnResult(result Result) { r.Consume(result) }

// onCompleted runs after every pipeline pass. Publication precedes it, so a
// session that received its result is already inactive; one still waiting got
// nothing and is closed with an empty result.
//
// The pipeline clears its transcribing flag just before this hook runs, so a
// new capture can start in between; a completion that arrives while the
// pipeline is recording belongs to the pass before that capture, not to it.
func (r *SessionRecognizer) onCompleted() {
	r.mu.Lock()
	waiting := r.capturing
	r.mu.Unlock()
	if !waiting {
		return
	}
	if r.pipeline.Recording() {
		r.log.Debug("Ignoring completion of a pass that preceded the active capture")
		return
	}
	r.log.Debug("Pipeline pass produced no result; ending session")
	r.Consume(Result{})
}
