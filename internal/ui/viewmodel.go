package ui

import (
	"strings"

	"github.com/aploide/sussurro/internal/session"
)

// ViewMode selects how much of the transcript the overlay shows. Immediate
// mode never leaves compact; review mode expands once there is text to read.
type ViewMode uint8

const (
	// ViewCompact is the capsule overlay: state and audio level only.
	ViewCompact ViewMode = iota
	// ViewExpanded additionally shows transcript text for review.
	ViewExpanded
	viewModeCount
)

// Valid reports whether mode is a defined view mode.
func (mode ViewMode) Valid() bool { return mode < viewModeCount }

func (mode ViewMode) String() string {
	switch mode {
	case ViewCompact:
		return "compact"
	case ViewExpanded:
		return "expanded"
	default:
		return "invalid"
	}
}

// ViewModel is an immutable description of what the overlay should show. The
// Manager builds one per update and hands it to the platform overlay, so no
// platform code reaches back into workflow state.
type ViewModel struct {
	// State is the immediate-mode lifecycle state, kept so existing overlays
	// and the tray icon behave exactly as before.
	State AppState
	// Review is the review-mode state. Meaningful only when Reviewing is true.
	Review session.ReviewState
	// Reviewing reports whether the review workflow is driving the overlay.
	Reviewing bool
	// Transcript is the partial or final text to display. Empty in compact mode.
	Transcript string
	// Partial reports whether Transcript is still being revised.
	Partial bool
	// Status is a short line describing what the user can do next, or the
	// error that just occurred.
	Status string
	// Mode selects compact or expanded presentation.
	Mode ViewMode
}

// Visible reports whether the overlay should be on screen for this model.
//
// An idle Sussurro shows nothing: the capsule is feedback about work in
// progress, not a permanent fixture. Review mode is the one exception — text
// held in Ready is waiting on the user, so hiding it would strand a session
// with no indication it exists.
func (model ViewModel) Visible() bool {
	if model.Reviewing {
		switch model.Review {
		case session.ReviewIdle:
			return false
		default:
			return true
		}
	}
	return model.State != session.StateIdle
}

// CompactModel builds the immediate-mode view for a lifecycle state. This is
// what upstream already showed, expressed as a model.
func CompactModel(state AppState) ViewModel {
	return ViewModel{State: state, Mode: ViewCompact, Status: compactStatus(state)}
}

// compactStatus describes an immediate-mode state.
func compactStatus(state AppState) string {
	switch state {
	case session.StateRecording:
		return "Listening"
	case session.StateTranscribing:
		return "Transcribing"
	default:
		return ""
	}
}

// ReviewModel builds the review-mode view. The overlay expands as soon as
// there is text worth reading and stays compact before that, so an empty card
// never appears over the user's window.
func ReviewModel(state session.ReviewState, transcript string, partial bool) ViewModel {
	model := ViewModel{
		State:      lifecycleFor(state),
		Review:     state,
		Reviewing:  true,
		Transcript: transcript,
		Partial:    partial,
		Status:     reviewStatus(state),
		Mode:       ViewCompact,
	}
	if strings.TrimSpace(transcript) != "" {
		model.Mode = ViewExpanded
	}
	return model
}

// ErrorModel builds a review view reporting a failure while keeping the text
// on screen, so a failed delivery never looks like lost dictation.
func ErrorModel(state session.ReviewState, transcript, message string) ViewModel {
	model := ReviewModel(state, transcript, false)
	model.Status = message
	return model
}

// lifecycleFor maps a review state onto the immediate-mode lifecycle, so the
// existing overlays and tray icon keep working unchanged under review mode.
func lifecycleFor(state session.ReviewState) AppState {
	switch state {
	case session.ReviewRecording, session.ReviewEditing:
		return session.StateRecording
	case session.ReviewFinalizing, session.ReviewApplyingEdit:
		return session.StateTranscribing
	default:
		// Ready and Delivering hold finished text; the capsule reads as idle
		// while the expanded card carries the actual review affordances.
		return session.StateIdle
	}
}

// reviewStatus describes what the user can do in a review state.
func reviewStatus(state session.ReviewState) string {
	switch state {
	case session.ReviewRecording:
		return "Listening"
	case session.ReviewFinalizing:
		return "Transcribing"
	case session.ReviewReady:
		return "Tap to deliver, hold to edit, Esc to cancel"
	case session.ReviewEditing:
		return "Listening for an edit"
	case session.ReviewApplyingEdit:
		return "Applying edit"
	case session.ReviewDelivering:
		return "Delivering"
	default:
		return ""
	}
}
