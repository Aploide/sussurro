package ui

import (
	"testing"

	"github.com/aploide/sussurro/internal/session"
)

// The waveform is the only live confirmation that audio is being captured, and
// it shares its slot with the status word. A label produced while recording
// would displace it mid-dictation, which is sussurro-xvj.61.
func TestRecordingStatesCarryNoLabel(t *testing.T) {
	t.Run("compact", func(t *testing.T) {
		if got := compactStatus(session.StateRecording); got != "" {
			t.Errorf("compactStatus(recording) = %q, want empty", got)
		}
	})

	t.Run("review", func(t *testing.T) {
		// Both states capture audio, so both show the waveform instead.
		for _, state := range []session.ReviewState{
			session.ReviewRecording,
			session.ReviewEditing,
		} {
			if got := reviewStatus(state); got != "" {
				t.Errorf("reviewStatus(%v) = %q, want empty", state, got)
			}
		}
	})
}

// The states where nothing is being captured must still explain themselves:
// the overlay is static then, and silence would read as a hang.
func TestNonRecordingStatesKeepTheirLabel(t *testing.T) {
	if got := compactStatus(session.StateCleaningUp); got == "" {
		t.Error("cleaning up lost its label; a static overlay must say why")
	}
	if got := reviewStatus(session.ReviewFinalizing); got == "" {
		t.Error("finalizing lost its label; a static overlay must say why")
	}
}
