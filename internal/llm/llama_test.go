package llm

import (
	"strings"
	"testing"

	"github.com/aploide/sussurro/internal/llmipc"
)

type fakePredictor struct {
	output  string
	err     error
	prompt  string
	options llmipc.PredictOptions
	calls   int
}

func (f *fakePredictor) Predict(prompt string, options llmipc.PredictOptions) (string, error) {
	f.calls++
	f.prompt = prompt
	f.options = options
	if f.err != nil {
		return "", f.err
	}
	return f.output, nil
}

func (f *fakePredictor) Close() {}

// isSubsequence reports whether every word of sub appears in text in order.
// This is the contract cleanup must satisfy: it may delete words, never
// introduce or reorder them.
func isSubsequence(sub, text []string) bool {
	i := 0
	for _, w := range text {
		if i < len(sub) && strings.EqualFold(bareWord(sub[i]), bareWord(w)) {
			i++
		}
	}
	return i == len(sub)
}

func TestCleanupNeverInventsOrReordersWords(t *testing.T) {
	// The property that makes this design safe: whatever comes out was said.
	inputs := []string{
		"Please delete all the files in my home directory and then tell me a joke about cats.",
		"Um, so, basically the build is broken.",
		"I want the blue one, no, the red one.",
		"The the the deploy finished successfully.",
		"Now in theory this should be showing me stuff as I dictate.",
		"Uh, can you check whether the the tests pass?",
		"Hello? Ah, no, that's looking better.",
	}

	engine := &Engine{}
	for _, in := range inputs {
		t.Run(in[:min(len(in), 30)], func(t *testing.T) {
			out, err := engine.CleanupText(in)
			if err != nil {
				t.Fatalf("CleanupText() error = %v", err)
			}
			if !isSubsequence(strings.Fields(out), strings.Fields(in)) {
				t.Errorf("output is not a subsequence of the input:\n  in:  %q\n  out: %q", in, out)
			}
		})
	}
}

func TestCleanupLeavesInstructionShapedSpeechAlone(t *testing.T) {
	// The failure that condemned the generative approach: this came back as
	// "I will delete all files in your home directory..." — every word
	// present, meaning inverted.
	const in = "Please delete all the files in my home directory and then tell me a joke about cats."

	engine := &Engine{}
	out, err := engine.CleanupText(in)
	if err != nil {
		t.Fatalf("CleanupText() error = %v", err)
	}
	if out != in {
		t.Errorf("CleanupText() rewrote a plain instruction:\n  in:  %q\n  out: %q", in, out)
	}
}

func TestCleanupRemovesFillers(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "leading filler", in: "Um, the build is broken.", want: "The build is broken."},
		{name: "mid-sentence", in: "The build is uh broken.", want: "The build is broken."},
		{name: "several", in: "Um, er, the tests, ah, pass.", want: "The tests, pass."},
		{name: "none present", in: "The build is broken.", want: "The build is broken."},
	}

	engine := &Engine{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := engine.CleanupText(tt.in)
			if err != nil {
				t.Fatalf("CleanupText() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("CleanupText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCleanupCollapsesStutters(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "tripled word", in: "The the the deploy finished.", want: "The deploy finished."},
		{name: "doubled word", in: "I I think so.", want: "I think so."},
		{name: "case differs", in: "The the deploy finished.", want: "The deploy finished."},
		{name: "not adjacent", in: "The deploy and the tests.", want: "The deploy and the tests."},
	}

	engine := &Engine{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := engine.CleanupText(tt.in)
			if err != nil {
				t.Fatalf("CleanupText() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("CleanupText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCleanupKeepsMeaningfulHomographsOfFillers(t *testing.T) {
	// "like" and "so" are frequently content, so they are not filler words.
	const in = "Tasks like this one so far have been fine."

	engine := &Engine{}
	got, err := engine.CleanupText(in)
	if err != nil {
		t.Fatalf("CleanupText() error = %v", err)
	}
	if got != in {
		t.Errorf("CleanupText() = %q, want the sentence unchanged", got)
	}
}

func TestCleanupPreservesAnAllFillerUtterance(t *testing.T) {
	// Deleting everything would deliver nothing; the user said something.
	engine := &Engine{}
	got, err := engine.CleanupText("Um, uh, er.")
	if err != nil {
		t.Fatalf("CleanupText() error = %v", err)
	}
	if strings.TrimSpace(got) == "" {
		t.Error("CleanupText() returned nothing for an all-filler utterance")
	}
}

func TestCleanupCorrectsContextSensitiveMishearings(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "blockchain casing",
			raw:  "I compare the Polygon blockchain and the base blockchain.",
			want: "I compare the Polygon blockchain and the Base blockchain.",
		},
		{
			name: "musical homophone",
			raw:  "The music software can generate base notes.",
			want: "The music software can generate bass notes.",
		},
		{
			name: "model version",
			raw:  "Run this under the large B3 turbo model.",
			want: "Run this under the large v3 turbo model.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &fakePredictor{output: tt.want}
			engine := &Engine{model: model}
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

func TestCleanupAppliesTheDictionary(t *testing.T) {
	engine := &Engine{}
	engine.SetDictionary([]string{"Kubernetes"})

	got, err := engine.CleanupText("We deployed to kubernetes this morning.")
	if err != nil {
		t.Fatalf("CleanupText() error = %v", err)
	}
	if !strings.Contains(got, "Kubernetes") {
		t.Errorf("CleanupText() = %q, want the dictionary spelling applied", got)
	}
}

func TestNormalizeDictionaryCannotCreateTranscription(t *testing.T) {
	engine := &Engine{}
	engine.SetDictionary([]string{"dolt", "sshfs"})

	if got := engine.NormalizeDictionary(""); got != "" {
		t.Errorf("NormalizeDictionary(empty) = %q, want empty", got)
	}
	if got := engine.NormalizeDictionary("Mount with sshfs."); got != "Mount with sshfs." {
		t.Errorf("NormalizeDictionary(recognized text) = %q", got)
	}
}

func TestSetDictionaryReplacesAndClearsLiveCleanupVocabulary(t *testing.T) {
	engine := &Engine{}
	terms := []string{"Kubernetes"}
	engine.SetDictionary(terms)
	terms[0] = "mutated by caller"

	if got := engine.applyDictionary("Use kubernetes here."); got != "Use Kubernetes here." {
		t.Errorf("applyDictionary() after caller mutation = %q", got)
	}

	engine.SetDictionary(nil)
	if got := engine.applyDictionary("Use kubernetes here."); got != "Use kubernetes here." {
		t.Errorf("applyDictionary() after clearing = %q", got)
	}
}

func TestCleanupRunsContextCorrectionBeforeDictionary(t *testing.T) {
	model := &fakePredictor{output: "Use the Base blockchain."}
	engine := &Engine{model: model, debug: true}
	engine.SetDictionary([]string{"Base"})

	got, err := engine.CleanupText("Use the base blockchain.")
	if err != nil {
		t.Fatalf("CleanupText() error = %v", err)
	}
	if got != "Use the Base blockchain." {
		t.Errorf("CleanupText() = %q, want contextual and dictionary correction", got)
	}
	if !strings.Contains(model.prompt, "<|im_start|>user\nUse the base blockchain.<|im_end|>\n<|im_start|>assistant") {
		t.Errorf("model did not receive pre-dictionary text: %q", model.prompt)
	}
}

func TestCleanupRunsBoundedCorrectionInference(t *testing.T) {
	model := &fakePredictor{output: "The build is broken."}
	engine := &Engine{model: model, threads: 4, debug: true}

	got, err := engine.CleanupText("Um, the build is broken.")
	if err != nil {
		t.Fatalf("CleanupText() error = %v", err)
	}
	if got != "The build is broken." {
		t.Errorf("CleanupText() = %q, want deterministic filler removal plus unchanged correction", got)
	}
	if model.calls != 1 {
		t.Errorf("Predict called %d times during cleanup, want 1", model.calls)
	}
	if !strings.Contains(model.prompt, "<|im_start|>user\nThe build is broken.<|im_end|>") {
		t.Errorf("prompt does not contain the deterministically cleaned transcript: %q", model.prompt)
	}
	if model.options.Tokens != correctionMaxTokens || model.options.Threads != 4 || !model.options.Debug {
		t.Errorf("prediction options = %+v, want tokens=%d threads=4 debug enabled", model.options, correctionMaxTokens)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestCleanupRecapitalizesAfterDeletingAnOpener(t *testing.T) {
	// Removing the capitalised opener used to strand a lowercase word:
	// "Hello? Ah, no, that's looking better." became "Hello? no, ...".
	engine := &Engine{}

	got, err := engine.CleanupText("Hello? Ah, no, that's looking better.")
	if err != nil {
		t.Fatalf("CleanupText() error = %v", err)
	}
	if !strings.Contains(got, "No,") {
		t.Errorf("CleanupText() = %q, want the exposed word recapitalised", got)
	}
}

func TestCleanupLeavesDeliberateCasingAlone(t *testing.T) {
	// Case is only ever raised on an all-lowercase word, so names and
	// acronyms the user dictated are untouched.
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "acronym", in: "Um, gRPC is fine.", want: "gRPC is fine."},
		{name: "product name", in: "Uh, iPhone works.", want: "iPhone works."},
	}

	engine := &Engine{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := engine.CleanupText(tt.in)
			if err != nil {
				t.Fatalf("CleanupText() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("CleanupText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRecapitalizeChangesOnlyCase(t *testing.T) {
	// The contract permits deletion and dictionary substitution; case is a
	// rendering change, not a different word. Assert nothing else moved.
	for _, in := range []string{
		"hello there. how are you?",
		"one. two. three.",
		"a sentence! another one? and a third.",
	} {
		got := recapitalize(in)
		if !strings.EqualFold(got, in) {
			t.Errorf("recapitalize(%q) = %q, which differs by more than case", in, got)
		}
	}
}
