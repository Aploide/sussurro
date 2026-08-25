package llm

import (
	"os"
	"testing"
)

// TestCorrectionWithRealModel is opt-in because it loads a GGUF model. It
// verifies that the bundled model proposes the corrections accepted by the
// deterministic guard, rather than only testing the guard with a fake.
func TestCorrectionWithRealModel(t *testing.T) {
	modelPath := os.Getenv("SUSSURRO_LLM_TEST_MODEL")
	if modelPath == "" {
		t.Skip("set SUSSURRO_LLM_TEST_MODEL to run the real-model correction test")
	}

	engine, err := NewEngine(modelPath, 4, 32768, 0, false)
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
			got, err := engine.CleanupText(tt.raw)
			if err != nil {
				t.Fatalf("CleanupText() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("CleanupText() = %q, want %q", got, tt.want)
			}
		})
	}
}
