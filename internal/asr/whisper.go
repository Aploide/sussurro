package asr

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aploide/sussurro/internal/config"
	"github.com/aploide/sussurro/internal/logger"
	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

// Segment is a span of recognised speech with the times it covers, measured
// from the start of the audio handed to the recogniser.
//
// The timestamps are what allow a streaming caller to tell which words belong
// to audio that has scrolled out of a window and can therefore be treated as
// settled. Joining segments into a plain string discards that, which is why
// windowing could not be implemented correctly against Transcribe alone
// (sussurro-xvj.60).
type Segment struct {
	Text  string
	Start time.Duration
	End   time.Duration
	// Words carries the segment's tokens with their own timings. Whisper
	// reports segments that can each run for tens of seconds, which is too
	// coarse to place a window boundary: a single long segment holding the
	// whole word budget leaves the window unable to shrink at all.
	Words []Word
}

// Word is a token with its own timing, used to place a window boundary
// between words rather than only between segments.
type Word struct {
	Text  string
	Start time.Duration
	End   time.Duration
}

// JoinSegments renders segments as a single transcription.
//
// Each segment's text is already trimmed, so a single space between them keeps
// words apart without introducing doubled spacing.
func JoinSegments(segments []Segment) string {
	parts := make([]string, 0, len(segments))
	for _, seg := range segments {
		if seg.Text != "" {
			parts = append(parts, seg.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// Engine handles the Whisper model and transcription
type Engine struct {
	model   whisper.Model
	context whisper.Context
	mutex   sync.Mutex
	debug   bool
}

// NewEngine initializes the Whisper model from a file path
func NewEngine(modelPath string, threads int, language string, debug bool) (*Engine, error) {
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("model file not found at %s: %w", modelPath, err)
	}

	if !debug {
		cleanup := logger.SuppressStderr()
		defer cleanup()
	}

	model, err := whisper.New(modelPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load whisper model: %w", err)
	}

	ctx, err := model.NewContext()
	if err != nil {
		return nil, fmt.Errorf("failed to create whisper context: %w", err)
	}

	// The threads setting was accepted and then never applied, so every
	// transcription ran on whisper's internal default (4) regardless of
	// configuration or how many cores the machine has.
	if threads > 0 {
		ctx.SetThreads(uint(threads))
	}

	// Per-token timings are what place a window boundary between words. Without
	// this whisper leaves every token's timestamp at its uninitialised -10ms,
	// which is not a corrupt value to be validated around but simply the answer
	// to a question never asked: the streamer then saw a window that never
	// advanced and re-settled the whole transcript on every pass
	// (sussurro-fkd).
	ctx.SetTokenTimestamps(true)

	if language != "" {
		if err := ctx.SetLanguage(language); err != nil {
			// Log warning but don't fail — e.g. english-only model ignores language
			slog.Warn("whisper: could not set language", "language", language, "error", err)
		}
	}

	return &Engine{
		model:   model,
		context: ctx,
		debug:   debug,
	}, nil
}

// EnableVAD configures whisper.cpp to recognise only audio that its Silero
// voice-activity model identifies as speech. This prevents trailing silence,
// ambient noise, and key-release transients from being decoded as stock phrases
// without filtering any particular words from the transcript.
func (e *Engine) EnableVAD(modelPath string, thresholds ...float32) error {
	info, err := os.Stat(modelPath)
	if err != nil {
		return fmt.Errorf("VAD model file not found at %s: %w (download it from %s)", modelPath, err, config.DefaultVADModelURL)
	}
	if info.Size() < config.MinimumVADModelSize {
		return fmt.Errorf("VAD model file at %s is incomplete (%d bytes); download it again from %s", modelPath, info.Size(), config.DefaultVADModelURL)
	}

	threshold := float32(0.01)
	if len(thresholds) > 0 && thresholds[0] > 0 && thresholds[0] <= 1 {
		threshold = thresholds[0]
	}

	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.context.SetVAD(true)
	e.context.SetVADModelPath(modelPath)
	// The default retained speech attenuated to 0.1% of its original amplitude
	// in verification while still rejecting silence and pink noise. It remains
	// configurable for noisier recording environments.
	e.context.SetVADThreshold(threshold)
	e.context.SetVADMinSilenceMs(500)
	e.context.SetVADSpeechPadMs(100)
	// whisper.cpp loads the VAD model lazily. A short silent pass forces that
	// initialization now, so a corrupt or incompatible model fails at startup
	// rather than after the user finishes dictating.
	if err := e.context.Process(make([]float32, 2*16000), nil, nil, nil); err != nil {
		return fmt.Errorf("failed to initialize VAD model at %s: %w", modelPath, err)
	}
	return nil
}

// TranscribeWithContext transcribes samples while conditioning the decoder on
// text that came before them.
//
// The context is passed as whisper's initial prompt, which biases decoding
// without being decoded itself. That is what lets a streaming pass transcribe
// only a recent window of audio and still produce text consistent with
// everything said earlier: the earlier words inform the decoder, but cost no
// inference and cannot be revised (sussurro-xvj.60).
//
// The prompt is advisory. whisper may still decode against it, and it truncates
// the prompt to n_text_ctx/2 (224 tokens), so a long preceding transcript is cut
// by the model.
func (e *Engine) TranscribeWithContext(samples []float32, preceding string) (string, error) {
	// The prompt is set on the shared whisper context, so it must not be
	// changed by another caller between being set and being used. The lock is
	// therefore held across the whole operation, exactly as Transcribe does.
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if prompt := strings.TrimSpace(preceding); prompt != "" {
		e.context.SetInitialPrompt(prompt)
		defer e.resetPromptLocked()
	}

	return e.transcribeLocked(samples)
}

// SegmentsWithContext transcribes samples and returns the recognised segments
// with their timestamps, conditioning the decoder on preceding text.
//
// This is the form a streaming caller needs: the timestamps say which words
// belong to which audio, so text whose audio has left the window can be settled
// exactly, with neither a gap nor a duplicated overlap.
func (e *Engine) SegmentsWithContext(samples []float32, preceding string) ([]Segment, error) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if prompt := strings.TrimSpace(preceding); prompt != "" {
		e.context.SetInitialPrompt(prompt)
		defer e.resetPromptLocked()
	}

	return e.segmentsLocked(samples)
}

// resetPromptLocked clears the per-call preceding-text prompt.
//
// The empty case is not a no-op and must not be skipped. Leaving a per-call
// prompt set leaks it into the next caller, and the next caller is the final
// pass: whisper continues an initial prompt rather than merely conditioning on
// it, so the whole accumulated transcript was re-emitted ahead of the real
// audio and the delivered text arrived with its sentences repeated
// (sussurro-fkd).
func (e *Engine) resetPromptLocked() {
	e.context.SetInitialPrompt("")
}

// Transcribe processes the audio samples and returns the text
func (e *Engine) Transcribe(samples []float32) (string, error) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	return e.transcribeLocked(samples)
}

// transcribeLocked is the body of Transcribe. Callers must hold e.mutex.
func (e *Engine) transcribeLocked(samples []float32) (string, error) {
	segments, err := e.segmentsLocked(samples)
	if err != nil {
		return "", err
	}
	return JoinSegments(segments), nil
}

// segmentsLocked runs recognition and returns its segments. Callers must hold
// e.mutex.
func (e *Engine) segmentsLocked(samples []float32) ([]Segment, error) {
	if len(samples) == 0 {
		return nil, nil
	}

	if !e.debug {
		cleanup := logger.SuppressStderr()
		defer cleanup()
	}

	if err := e.context.Process(samples, nil, nil, nil); err != nil {
		return nil, fmt.Errorf("transcription failed: %w", err)
	}

	var segments []Segment
	for {
		segment, err := e.context.NextSegment()
		if err != nil {
			break // End of segments
		}
		// Trimmed here so joining cannot merge words across a boundary
		// (e.g. "wentto" instead of "went to").
		text := strings.TrimSpace(segment.Text)
		if text == "" {
			continue
		}
		var words []Word
		for _, tok := range segment.Tokens {
			// Whisper mixes special tokens (timestamps, control markers) in
			// with real text; they carry no speech, so they must not count
			// toward a word budget or reach the transcript.
			if strings.TrimSpace(tok.Text) == "" || strings.HasPrefix(tok.Text, "[_") {
				continue
			}
			// Text is kept verbatim, spaces included. These are BPE pieces
			// rather than words, and their leading spaces are what separate
			// words: trimming them and rejoining would run words together.
			words = append(words, Word{
				Text:  tok.Text,
				Start: tok.Start,
				End:   tok.End,
			})
		}

		segments = append(segments, Segment{
			Text:  text,
			Start: segment.Start,
			End:   segment.End,
			Words: words,
		})
	}

	return segments, nil
}

// Close releases resources
// Note: context.Close() is not available in the bindings, we rely on GC or explicit C-level cleanup if exposed.
// However, Model has Close().
func (e *Engine) Close() {
	// e.context.Close() // Not available in current bindings
	if e.model != nil {
		e.model.Close()
	}
}
