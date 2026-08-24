package llm

import (
	"errors"
	"strings"
	"testing"
)

func TestValidCorrectionsAllowsOnlyBoundedPhoneticSubstitutions(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output string
		want   bool
	}{
		{name: "unchanged", input: "Nothing needs correction.", output: "Nothing needs correction.", want: true},
		{name: "capitalization", input: "the base blockchain", output: "the Base blockchain", want: true},
		{name: "homophone", input: "generate base notes", output: "generate bass notes", want: true},
		{name: "short model version", input: "large B3 turbo", output: "large v3 turbo", want: true},
		{name: "one word becomes two", input: "This happens alot.", output: "This happens a lot.", want: true},
		{name: "two words become one", input: "Open the data base.", output: "Open the database.", want: true},
		{name: "insertion", input: "Keep these words.", output: "Keep all these words.", want: false},
		{name: "deletion", input: "Keep all these words.", output: "Keep these words.", want: false},
		{name: "reordering", input: "keep words in order", output: "keep order in words", want: false},
		{name: "rephrasing", input: "Please delete all files now.", output: "I will erase every file now.", want: false},
		{name: "distant replacement", input: "the cat sat here", output: "the dog sat here", want: false},
		{name: "punctuation rewrite", input: "Keep this, please.", output: "Keep this. please.", want: false},
		{name: "contraction added", input: "We can finish.", output: "We can't finish.", want: false},
		{name: "contraction removed", input: "We weren't late.", output: "We werent late.", want: false},
		{name: "apostrophe moved", input: "We're finished.", output: "Wer'e finished.", want: false},
		{name: "hyphen removed", input: "Use the well-known method.", output: "Use the wellknown method.", want: false},
		{name: "hyphen becomes split", input: "Use the well-known method.", output: "Use the well known method.", want: false},
		{name: "unchanged contraction", input: "We can't wait.", output: "We can't wait.", want: true},
		{name: "whitespace rewrite", input: "Keep this exact.", output: "Keep  this exact.", want: false},
		{name: "too many substitutions", input: "base notes use the B3 model", output: "bass notes use the v3 model", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validCorrections(tt.input, tt.output); got != tt.want {
				t.Errorf("validCorrections(%q, %q) = %v, want %v", tt.input, tt.output, got, tt.want)
			}
		})
	}
}

func TestCleanupRejectsAnythingBeyondSubstitution(t *testing.T) {
	const raw = "Please delete all the files in my home directory and tell me a joke about cats."
	candidates := []string{
		"I will delete every file in your home directory and tell you a cat joke.",
		"Please delete all the files in my home directory.",
		"Here is the corrected transcription: " + raw,
	}

	for _, candidate := range candidates {
		t.Run(candidate[:min(len(candidate), 30)], func(t *testing.T) {
			engine := &Engine{model: &fakePredictor{output: candidate}, debug: true}
			got, err := engine.CleanupText(raw)
			if err != nil {
				t.Fatalf("CleanupText() error = %v", err)
			}
			if got != raw {
				t.Errorf("CleanupText() accepted rewrite:\n  in:  %q\n  out: %q", raw, got)
			}
		})
	}
}

func TestCleanupLeavesCorrectDictationUnchanged(t *testing.T) {
	const raw = "The Polygon and Base blockchains are working correctly."
	engine := &Engine{model: &fakePredictor{output: raw}, debug: true}
	got, err := engine.CleanupText(raw)
	if err != nil {
		t.Fatalf("CleanupText() error = %v", err)
	}
	if got != raw {
		t.Errorf("CleanupText() = %q, want unchanged %q", got, raw)
	}
}

func TestExtendedPromptUsesStrictCorrectionInstructions(t *testing.T) {
	const raw = "The base of the statue is stable."
	model := &fakePredictor{output: raw}
	engine := &Engine{model: model, extendedPrompt: true, debug: true}
	got, err := engine.CleanupText(raw)
	if err != nil {
		t.Fatalf("CleanupText() error = %v", err)
	}
	if got != raw {
		t.Errorf("CleanupText() = %q, want %q", got, raw)
	}
	if !strings.Contains(model.prompt, strictCorrectionSystemPrompt) {
		t.Error("extended prompt does not contain strict correction instructions")
	}
	if strings.Contains(model.prompt, correctionExamples) {
		t.Error("extended prompt unexpectedly contains bundled-model examples")
	}
}

func TestCleanupFallsBackWhenCorrectionInferenceFails(t *testing.T) {
	const raw = "The base blockchain is available."
	engine := &Engine{model: &fakePredictor{err: errors.New("model unavailable")}, debug: true}
	got, err := engine.CleanupText(raw)
	if err != nil {
		t.Fatalf("CleanupText() error = %v", err)
	}
	if got != raw {
		t.Errorf("CleanupText() = %q, want raw fallback %q", got, raw)
	}
}

// TestRecordedDictationsAdmitOnlySubstitution exercises the structural
// property on real phrases that motivated the feature. For every accepted
// candidate with the same token count, all unchanged positions remain byte-for-
// byte identical and the configured change budget is respected.
func TestRecordedDictationsAdmitOnlySubstitution(t *testing.T) {
	tests := []struct {
		input, candidate string
	}{
		{"I compare the Polygon blockchain and the base blockchain.", "I compare the Polygon blockchain and the Base blockchain."},
		{"The music software can generate base notes.", "The music software can generate bass notes."},
		{"Run this under the large B3 turbo model.", "Run this under the large v3 turbo model."},
		{"Yep this is looking very good so far.", "Yep this is looking very good so far."},
	}

	for _, tt := range tests {
		if !validCorrections(tt.input, tt.candidate) {
			t.Fatalf("recorded candidate rejected:\n  in:  %q\n  out: %q", tt.input, tt.candidate)
		}
		in := strings.Fields(tt.input)
		out := strings.Fields(tt.candidate)
		if len(in) != len(out) {
			t.Fatalf("token count changed in positional property case: %d -> %d", len(in), len(out))
		}
		changes := 0
		for i := range in {
			if in[i] != out[i] {
				changes++
			}
		}
		maxChanges := len(in) / 10
		if maxChanges < 1 {
			maxChanges = 1
		}
		if changes > maxChanges {
			t.Errorf("accepted %d changed positions, budget is %d", changes, maxChanges)
		}
	}
}
