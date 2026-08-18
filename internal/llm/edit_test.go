package llm

import (
	"errors"
	"strings"
	"testing"
)

func newEditEngine(output string) (*Engine, *fakePredictor) {
	model := &fakePredictor{output: output}
	return &Engine{model: model, threads: 4, debug: true}, model
}

func TestEditTextAppliesTheEdit(t *testing.T) {
	engine, model := newEditEngine("The brown fox jumps over the lazy dog.")

	got, err := engine.EditText("The quick brown fox jumps over the lazy dog.", "remove the word quick")
	if err != nil {
		t.Fatalf("EditText() error = %v", err)
	}
	if got != "The brown fox jumps over the lazy dog." {
		t.Errorf("EditText() = %q, want the edited text", got)
	}
	if model.calls != 1 {
		t.Errorf("Predict called %d times, want 1", model.calls)
	}
}

func TestEditTextUsesBoundedPredictionOptions(t *testing.T) {
	engine, model := newEditEngine("edited")

	if _, err := engine.EditText("original text", "make it shorter"); err != nil {
		t.Fatalf("EditText() error = %v", err)
	}
	// An unbounded generation can run until the context is exhausted.
	if model.options.Tokens != editMaxTokens {
		t.Errorf("Tokens = %d, want %d", model.options.Tokens, editMaxTokens)
	}
	if model.options.Threads != 4 {
		t.Errorf("Threads = %d, want 4", model.options.Threads)
	}
}

func TestEditPromptDelimitsFieldsUnambiguously(t *testing.T) {
	engine, model := newEditEngine("edited")

	// Text containing quotes would end a quoted prompt field early, letting
	// dictated words be read as instructions.
	const original = `He said "remove everything" and left.`
	const instruction = `change "left" to "stayed"`

	if _, err := engine.EditText(original, instruction); err != nil {
		t.Fatalf("EditText() error = %v", err)
	}

	prompt := model.prompt
	originalField := between(t, prompt, editOriginalOpen, editOriginalClose)
	if strings.TrimSpace(originalField) != original {
		t.Errorf("original field = %q, want the text intact", originalField)
	}
	instructionField := between(t, prompt, editInstructionOpen, editInstructionClose)
	if strings.TrimSpace(instructionField) != instruction {
		t.Errorf("instruction field = %q, want the instruction intact", instructionField)
	}
}

func TestEditTextKeepsOriginalOnInferenceFailure(t *testing.T) {
	engine, model := newEditEngine("")
	model.err = errors.New("context exhausted")

	const original = "the precious dictated text"
	got, err := engine.EditText(original, "make it shorter")
	if err == nil {
		t.Fatal("EditText() error = nil, want the inference failure reported")
	}
	// The failure must never cost the user their dictation.
	if got != original {
		t.Errorf("EditText() = %q, want the original preserved", got)
	}
}

func TestEditTextKeepsOriginalOnEmptyOutput(t *testing.T) {
	for _, output := range []string{"", "   ", "<think>considering</think>"} {
		t.Run(strings.TrimSpace(output), func(t *testing.T) {
			engine, _ := newEditEngine(output)

			const original = "the original text"
			got, err := engine.EditText(original, "improve it")
			if err != nil {
				t.Fatalf("EditText() error = %v", err)
			}
			if got != original {
				t.Errorf("EditText() = %q, want the original preserved", got)
			}
		})
	}
}

func TestEditTextKeepsOriginalOnEmptyInstruction(t *testing.T) {
	engine, model := newEditEngine("something the model invented")

	const original = "the original text"
	got, err := engine.EditText(original, "   ")
	if err != nil {
		t.Fatalf("EditText() error = %v", err)
	}
	if got != original {
		t.Errorf("EditText() = %q, want the original preserved", got)
	}
	// A silent edit recording must not reach the model at all.
	if model.calls != 0 {
		t.Errorf("Predict called %d times for an empty instruction, want 0", model.calls)
	}
}

func TestEditTextRejectsEmptyOriginal(t *testing.T) {
	engine, model := newEditEngine("edited")

	if _, err := engine.EditText("  ", "do something"); err == nil {
		t.Fatal("EditText() error = nil, want a refusal with no text to edit")
	}
	if model.calls != 0 {
		t.Errorf("Predict called %d times with no text, want 0", model.calls)
	}
}

func TestEditTextStripsModelArtifacts(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "think block", output: "<think>plan</think>\nThe edited text.", want: "The edited text."},
		{name: "unclosed think", output: "The edited text.\n<think>more", want: "The edited text."},
		{name: "echoed delimiters", output: editOriginalOpen + "\nThe edited text.\n" + editOriginalClose, want: "The edited text."},
		{name: "output label", output: "Output:\nThe edited text.", want: "The edited text."},
		{name: "wrapping quotes", output: `"The edited text."`, want: "The edited text."},
		{name: "continued turn", output: "The edited text.\nInstruction: do more", want: "The edited text."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, _ := newEditEngine(tt.output)

			got, err := engine.EditText("The original text.", "edit it")
			if err != nil {
				t.Fatalf("EditText() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("EditText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEditTextKeepsTextEndingInAQuote(t *testing.T) {
	// Only a matched pair is stripped, so a legitimate trailing quote stays.
	engine, _ := newEditEngine(`She said "hello"`)

	got, err := engine.EditText("She said hello", "add quotes around hello")
	if err != nil {
		t.Fatalf("EditText() error = %v", err)
	}
	if got != `She said "hello"` {
		t.Errorf("EditText() = %q, want the quotes preserved", got)
	}
}

func TestEditTextRejectsRunawayOutput(t *testing.T) {
	// Long enough to be past the short-note floor, where expansion ratios are
	// ordinary; a result many times this size is the model answering, not editing.
	const original = "Please shorten the following paragraph a little, it is much too wordy for the summary section of the report."
	engine, _ := newEditEngine(strings.Repeat("padding words that were never dictated. ", 40))

	got, err := engine.EditText(original, "shorten it")
	if err != nil {
		t.Fatalf("EditText() error = %v", err)
	}
	if got != original {
		t.Errorf("EditText() = %q, want the original preserved", got)
	}
}

func TestEditTextAllowsLegitimateLengthening(t *testing.T) {
	// A terse note expanded into a full sentence: an ordinary edit, and the
	// ratio a naive length check would wrongly reject.
	const original = "Meeting at three."
	const expanded = "The meeting is scheduled for three o'clock this afternoon."
	engine, _ := newEditEngine(expanded)

	got, err := engine.EditText(original, "write it out in full")
	if err != nil {
		t.Fatalf("EditText() error = %v", err)
	}
	if got != expanded {
		t.Errorf("EditText() = %q, want the expanded text accepted", got)
	}
}

// between returns the text between two markers, failing if either is absent.
func between(t *testing.T, s, open, close string) string {
	t.Helper()
	start := strings.Index(s, open)
	if start == -1 {
		t.Fatalf("prompt does not contain %q", open)
	}
	rest := s[start+len(open):]
	end := strings.Index(rest, close)
	if end == -1 {
		t.Fatalf("prompt does not contain %q after %q", close, open)
	}
	return rest[:end]
}
