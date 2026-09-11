package ui

import "github.com/aploide/sussurro/internal/session"

// AppState aliases the platform-neutral session state for overlay APIs.
type AppState = session.State

const (
	StateIdle         = session.StateIdle         // 7 animated dots
	StateRecording    = session.StateRecording    // waveform bars
	StateTranscribing = session.StateTranscribing // shimmer text
	StateCleaningUp   = session.StateCleaningUp   // shimmer text, post-ASR
)
