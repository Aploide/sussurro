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

func TestValidateOutputRejectsShortContentDeletion(t *testing.T) {
	// Real dictation: the model kept the first word and deleted the rest.
	// 37 characters, so every character-ratio floor missed it.
	const raw = "Hello? Ah, no, that's looking better."
	const gutted = "Hello."

	if validateOutput(raw, gutted, nil) {
		t.Error("validateOutput accepted a one-word reduction of a whole sentence")
	}
}

func TestValidateOutputAllowsFillerRemovalFromShortSpeech(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		cleaned string
	}{
		{
			name:    "leading fillers",
			raw:     "Um, so, basically the build is broken.",
			cleaned: "The build is broken.",
		},
		{
			name:    "false start repaired",
			raw:     "I want the blue one, no, the red one.",
			cleaned: "I want the red one.",
		},
		{
			name:    "stutter removed",
			raw:     "The the the deploy finished successfully.",
			cleaned: "The deploy finished successfully.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !validateOutput(tt.raw, tt.cleaned, nil) {
				t.Errorf("validateOutput rejected legitimate cleanup:\n  raw:     %q\n  cleaned: %q",
					tt.raw, tt.cleaned)
			}
		})
	}
}

func TestContentWordsIgnoresFillersAndPunctuation(t *testing.T) {
	got := contentWords("Um, so that's really quite broken!")

	// "um", "so", "that", "s", "really" are fillers; "quite" and "broken"
	// carry the meaning.
	want := map[string]bool{"quite": true, "broken": true}
	if len(got) != len(want) {
		t.Fatalf("contentWords() = %v, want %d words", got, len(want))
	}
	for _, w := range got {
		if !want[w] {
			t.Errorf("contentWords() returned %q, want only %v", w, want)
		}
	}
}

func TestPreservesContentWordsSkipsVeryShortInput(t *testing.T) {
	// Too few content words for a ratio to mean anything; the character
	// checks cover these.
	if !preservesContentWords("Yes okay", "Yes.") {
		t.Error("preservesContentWords rejected a very short utterance")
	}
}

func TestPreservesContentWordsCountsRepeats(t *testing.T) {
	// Deleting one of two identical content words is not wholesale loss.
	if !preservesContentWords("deploy the deploy script now", "deploy script now") {
		t.Error("preservesContentWords rejected a repeated-word removal")
	}
}

func TestValidateOutputStillRejectsLongSummarization(t *testing.T) {
	// The original 291-character case must stay rejected.
	const raw = "Now in theory this should be showing me stuff as I type, as I dictate rather. " +
		"Now in theory this should be showing me stuff like that. " +
		"Now in theory this should be showing me stuff like that. " +
		"Now in theory this should be showing me stuff like that. " +
		"So I'm going to show you stuff like that."
	const summarized = "Now in theory this should be showing me stuff as I type, as I dictate rather."

	if validateOutput(raw, summarized, nil) {
		t.Error("validateOutput accepted the long summarization case")
	}
}
