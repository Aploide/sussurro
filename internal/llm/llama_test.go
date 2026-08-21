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

func TestValidateOutputRejectsSummarizedDictation(t *testing.T) {
	// Real dictation from a user report: the model kept the first sentence and
	// silently dropped the rest, losing 74% of the words. The old 400-char
	// floor let this through because the input was only 291 characters.
	const raw = "Now in theory this should be showing me stuff as I type, as I dictate rather. " +
		"Now in theory this should be showing me stuff like that. " +
		"Now in theory this should be showing me stuff like that. " +
		"Now in theory this should be showing me stuff like that. " +
		"So I'm going to show you stuff like that."
	const summarized = "Now in theory this should be showing me stuff as I type, as I dictate rather."

	if validateOutput(raw, summarized, nil) {
		t.Errorf("validateOutput accepted a %d%% reduction, want it rejected as summarization",
			100-100*len(summarized)/len(raw))
	}
}

func TestValidateOutputAllowsOrdinaryFillerRemoval(t *testing.T) {
	// Cleanup legitimately strips fillers and false starts; that must not be
	// mistaken for summarization.
	const raw = "So um I was thinking that we should uh probably move the meeting to " +
		"Tuesday afternoon because you know Monday is already quite full."
	const cleaned = "I was thinking we should probably move the meeting to Tuesday afternoon " +
		"because Monday is already quite full."

	if !validateOutput(raw, cleaned, nil) {
		t.Errorf("validateOutput rejected ordinary filler removal (%d -> %d chars)",
			len(raw), len(cleaned))
	}
}

func TestValidateOutputIgnoresShortUtterances(t *testing.T) {
	// Below the floor, filler removal can legitimately account for a large
	// fraction of a very short utterance.
	if !validateOutput("Um, so, yeah, okay then.", "Okay then.", nil) {
		t.Error("validateOutput rejected a short utterance, want it accepted")
	}
}
