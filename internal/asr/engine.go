package asr

import (
	"fmt"
	"strings"
)

// Engine is the common ASR interface used by the pipeline.
type Engine interface {
	Transcribe(samples []float32) (string, error)
	Close()
	EngineName() string
}

// NewEngine initializes the requested ASR backend based on config.
func NewEngine(modelPath string, modelType string, threads int, language string, debug bool) (Engine, error) {
	switch strings.ToLower(strings.TrimSpace(modelType)) {
	case "", "whisper":
		return NewWhisperEngine(modelPath, threads, language, debug)
	case "parakeet", "parakeet-v3", "nemo-parakeet-tdt-0.6b-v3":
		return NewParakeetEngine(modelPath, debug)
	default:
		return nil, fmt.Errorf("unsupported ASR type: %s", modelType)
	}
}
