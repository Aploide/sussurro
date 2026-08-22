package pipeline

import (
	"strings"

	"github.com/aploide/sussurro/internal/asr"
	ctxProvider "github.com/aploide/sussurro/internal/context"
)

// Result is a completed recognition, published before any delivery occurs.
// Review mode holds results for inspection and editing; immediate mode passes
// them straight to a compatibility consumer.
type Result struct {
	// Raw is the verbatim ASR transcription.
	Raw string
	// Text is the text intended for delivery: the LLM-cleaned transcription,
	// or Raw when cleanup is skipped or fails.
	Text string
	// Context describes the window focused when the segment was captured.
	Context ctxProvider.ContextInfo
	// Cleaned reports whether LLM cleanup actually produced Text.
	Cleaned bool
}

// Empty reports whether the result carries no deliverable text.
func (r Result) Empty() bool {
	return strings.TrimSpace(r.Text) == ""
}

// ResultConsumer receives recognition results once, in order, from the
// pipeline's processing goroutine. Implementations must not block for long.
type ResultConsumer interface {
	OnResult(result Result)
}

// ResultConsumerFunc adapts a function to ResultConsumer.
type ResultConsumerFunc func(result Result)

// OnResult implements ResultConsumer.
func (fn ResultConsumerFunc) OnResult(result Result) { fn(result) }

// transcriber converts captured samples into text. Narrowing the ASR engine to
// this interface lets the result path be tested without model files.
type transcriber interface {
	Transcribe(samples []float32) (string, error)
}

// segmentingTranscriber is the optional extension for engines that can report
// where each span of recognised speech sits in the audio, and can condition
// decoding on text preceding it.
//
// Timestamps are what make a sliding window correct: they say which words
// belong to audio that has scrolled out of the window and can be settled, so
// the window advances with neither a gap nor a duplicated overlap. Engines
// without this fall back to transcribing the whole buffer (sussurro-xvj.60).
type segmentingTranscriber interface {
	SegmentsWithContext(samples []float32, preceding string) ([]asr.Segment, error)
}

// cleaner post-processes raw transcriptions.
type cleaner interface {
	CleanupText(rawText string) (string, error)
}
