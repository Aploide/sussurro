package llm

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestCorrectionWithRealModel is opt-in because it loads a GGUF model. It
// verifies that the bundled model proposes the corrections accepted by the
// deterministic guard, rather than only testing the guard with a fake.
func TestCorrectionWithRealModel(t *testing.T) {
	modelPath := os.Getenv("SUSSURRO_LLM_TEST_MODEL")
	if modelPath == "" {
		t.Skip("set SUSSURRO_LLM_TEST_MODEL to run the real-model correction test")
	}
	if os.Getenv(helperOverrideEnv) == "" {
		helper, err := filepath.Abs(filepath.Join("..", "..", "bin", helperBinaryName()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(helper); err != nil {
			t.Fatalf("built LLM helper not found at %s; run make build-helper: %v", helper, err)
		}
		t.Setenv(helperOverrideEnv, helper)
	}

	threads := integrationEnvInt(t, "SUSSURRO_LLM_TEST_THREADS", 4)
	contextSize := integrationEnvInt(t, "SUSSURRO_LLM_TEST_CONTEXT", 4096)
	gpuLayers := integrationEnvInt(t, "SUSSURRO_LLM_TEST_GPU_LAYERS", 99)
	debug := os.Getenv("SUSSURRO_LLM_TEST_DEBUG") != ""
	t.Logf("model configuration: threads=%d context=%d gpu_layers=%d debug=%t", threads, contextSize, gpuLayers, debug)

	engine, err := NewEngine(modelPath, threads, contextSize, gpuLayers, debug)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	t.Cleanup(func() { engine.Close() })

	tests := []struct {
		raw, want string
	}{
		{"I compare the Polygon blockchain and the base blockchain.", "I compare the Polygon blockchain and the Base blockchain."},
		{"The music software can generate base notes.", "The music software can generate bass notes."},
		{"Run this under the large B3 turbo model.", "Run this under the large v3 turbo model."},
		{"this sentence has incorrect punctuation, And CAPITALIZATION", "This sentence has incorrect punctuation. And capitalization."},
		{"The Polygon and Base blockchains are working correctly.", "The Polygon and Base blockchains are working correctly."},
		{"The base of the statue is made of stone.", "The base of the statue is made of stone."},
		{"The bass player performs the lowest notes.", "The bass player performs the lowest notes."},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			started := time.Now()
			got, err := engine.CleanupText(tt.raw)
			duration := time.Since(started)
			t.Logf("cleanup duration: %s", duration)
			if err != nil {
				t.Fatalf("CleanupText() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("CleanupText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func integrationEnvInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("%s=%q is not an integer: %v", name, value, err)
	}
	return parsed
}
