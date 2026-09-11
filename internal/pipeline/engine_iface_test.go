package pipeline

import (
	"testing"

	"github.com/aploide/sussurro/internal/asr"
)

// The windowing only takes effect if the real engine satisfies the optional
// interface. A signature drift would silently fall back to transcribing the
// whole buffer, restoring the quadratic cost with no test failing.
func TestASREngineSupportsSegmentedTranscription(t *testing.T) {
	var _ segmentingTranscriber = (*asr.Engine)(nil)
	var _ transcriber = (*asr.Engine)(nil)
}
