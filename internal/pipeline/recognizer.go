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
func NewSessionRecognizer(pipe *Pipeline, onResult func(id session.SessionID, text string), log *slog.Logger) *SessionRecognizer {
	return &SessionRecognizer{pipeline: pipe, onResult: onResult, log: log}
}

// StartCapture begins recording for the given session.
func (r *SessionRecognizer) StartCapture(id session.SessionID) {
	r.mu.Lock()
	r.active = id
	r.capturing = true
	r.mu.Unlock()

	r.pipeline.StartRecording()
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
