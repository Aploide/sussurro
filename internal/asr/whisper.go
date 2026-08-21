package asr

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/aploide/sussurro/internal/logger"
	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

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

// Transcribe processes the audio samples and returns the text
func (e *Engine) Transcribe(samples []float32) (string, error) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if len(samples) == 0 {
		return "", nil
	}

	if !e.debug {
		cleanup := logger.SuppressStderr()
		defer cleanup()
	}

	if err := e.context.Process(samples, nil, nil, nil); err != nil {
		return "", fmt.Errorf("transcription failed: %w", err)
	}

	// Iterate through segments to build the full text.
	// Each segment is trimmed before joining with a space to prevent words
	// from merging at segment boundaries (e.g. "wentto" instead of "went to").
	var parts []string
	for {
		segment, err := e.context.NextSegment()
		if err != nil {
			break // End of segments
		}
		if t := strings.TrimSpace(segment.Text); t != "" {
			parts = append(parts, t)
		}
	}

	return strings.TrimSpace(strings.Join(parts, " ")), nil
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
