package llm

import "testing"

func TestCleanupTextPreservesAbbreviationCase(t *testing.T) {
	engine := &Engine{}
	tests := []string{
		"It happened at 11 p.m. Which means it was late.",
		"It happened at 7 a.m. Then we left.",
		"Use examples, e.g. this one.",
		"That is, i.e. exactly this.",
		"The U.S. office closed.",
		"Use etc. when the rest is omitted.",
		"The title Dr. precedes a name.",
		"She is a Ph.D. student.",
		"An M.Sc. program opened.",
		"Use (e.g.) this form.",
		"The É.U. office closed.",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			got, err := engine.CleanupText(input)
			if err != nil {
				t.Fatal(err)
			}
			if got != input {
				t.Errorf("CleanupText() = %q, want unchanged %q", got, input)
			}
		})
	}
}

func TestCleanupTextCapitalizesWordExposedByDeletedOpener(t *testing.T) {
	engine := &Engine{}
	tests := map[string]string{
		"One sentence! Ah, another sentence.":              "One sentence! Another sentence.",
		"Hello? Ah, no no, continue.":                      "Hello? No, continue.",
		"What? Ah, what happened?":                         "What? What happened?",
		"I was, Um, trying to leave.":                      "I was, trying to leave.",
		"The U.S. Um, office closed.":                      "The U.S. office closed.",
		"He asked “Really?” ah, élève answered.":           "He asked “Really?” Élève answered.",
		"um, lowercase still starts the whole transcript.": "Lowercase still starts the whole transcript.",
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			got, err := engine.CleanupText(input)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("CleanupText() = %q, want %q", got, want)
			}
		})
	}
}

func TestCleanupTextCapitalizesGenuineSentenceStart(t *testing.T) {
	engine := &Engine{}
	got, err := engine.CleanupText("Was that the U.S.? ah, next we left.")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Was that the U.S.? Next we left."; got != want {
		t.Errorf("CleanupText() = %q, want %q", got, want)
	}
}

func TestCleanupTextKeepsRepeatedSentenceOpener(t *testing.T) {
	engine := &Engine{}
	for _, input := range []string{
		"What? What happened?",
		"Stop! Stop now.",
		"Go. Go now.",
	} {
		t.Run(input, func(t *testing.T) {
			got, err := engine.CleanupText(input)
			if err != nil {
				t.Fatal(err)
			}
			if got != input {
				t.Errorf("CleanupText() = %q, want unchanged %q", got, input)
			}
		})
	}
}
