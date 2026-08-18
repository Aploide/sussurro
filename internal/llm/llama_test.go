package llm

import (
	"testing"

	llama "github.com/AshkanYarmoradi/go-llama.cpp"
)

type fakePredictor struct {
	output  string
	err     error
	prompt  string
	options llama.PredictOptions
	calls   int
}

func (f *fakePredictor) Predict(prompt string, opts ...llama.PredictOption) (string, error) {
	f.calls++
	f.prompt = prompt
	f.options = llama.NewPredictOptions(opts...)
	if f.err != nil {
		return "", f.err
	}
	return f.output, nil
}

func (f *fakePredictor) Free() {}

func TestCleanupOnceUsesBoundedPredictionOptions(t *testing.T) {
	model := &fakePredictor{output: "The original sentence."}
	engine := &Engine{model: model, threads: 7, debug: true}

	got, ok, err := engine.cleanupOnce("The original sentence.")
	if err != nil {
		t.Fatalf("cleanupOnce returned an error: %v", err)
	}
	if !ok {
		t.Fatal("cleanupOnce rejected unchanged valid output")
	}
	if got != "The original sentence." {
		t.Fatalf("cleanupOnce returned %q, want unchanged valid output", got)
	}
	if model.calls != 1 {
		t.Fatalf("Predict called %d times, want 1", model.calls)
	}
	if model.options.Tokens != cleanupMaxTokens {
		t.Fatalf("prediction token limit = %d, want %d", model.options.Tokens, cleanupMaxTokens)
	}
	if model.options.Tokens <= 0 {
		t.Fatalf("prediction token limit = %d, want a positive bound", model.options.Tokens)
	}
	if model.options.Threads != 7 {
		t.Fatalf("prediction threads = %d, want 7", model.options.Threads)
	}
}

func TestCleanupOnceFallsBackToRawTextOnEmptyOutput(t *testing.T) {
	const raw = "Keep this original sentence."
	model := &fakePredictor{}
	engine := &Engine{model: model, threads: 4, debug: true}

	got, ok, err := engine.cleanupOnce(raw)
	if err != nil {
		t.Fatalf("cleanupOnce returned an error: %v", err)
	}
	if got != raw {
		t.Fatalf("cleanupOnce returned %q, want raw fallback %q", got, raw)
	}
	if ok {
		t.Fatal("cleanupOnce accepted empty model output")
	}
}
