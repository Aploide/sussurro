package asr

import (
	"testing"

	whisper "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

// nopContext satisfies whisper.Context without a model. The interface is
// embedded rather than stubbed method by method: anything these tests do not
// set explicitly panics if called, which is louder than a silent zero value.
type nopContext struct{ whisper.Context }

// recordingContext captures the prompts set on it. Only SetInitialPrompt is
// exercised here; the rest of whisper.Context is unused by these tests, so the
// embedded interface supplies it and would panic loudly if that changed.
type recordingContext struct {
	nopContext
	prompts []string
}

func (c *recordingContext) SetInitialPrompt(p string) {
	c.prompts = append(c.prompts, p)
}

func (c *recordingContext) last() string {
	if len(c.prompts) == 0 {
		return "<never set>"
	}
	return c.prompts[len(c.prompts)-1]
}

// A per-call prompt must never outlive the call.
//
// The restore used to be skipped when no dictionary was configured, so the
// streaming transcript stayed set on the shared context and the final pass
// decoded with it. Whisper continues an initial prompt rather than merely
// conditioning on it, so the accumulated transcript was re-emitted ahead of the
// real audio and the delivered text arrived with its sentences repeated
// (sussurro-fkd).
func TestPromptIsClearedWhenNoDictionaryIsConfigured(t *testing.T) {
	ctx := &recordingContext{}
	e := &Engine{context: ctx}

	e.mutex.Lock()
	e.setPromptLocked("text from a streaming pass")
	e.resetPromptLocked()
	e.mutex.Unlock()

	if got := ctx.last(); got != "" {
		t.Errorf("prompt left as %q after reset; it must be cleared so it "+
			"cannot leak into the final pass", got)
	}
}

// With a dictionary, the standing prompt is restored after the preceding text
// used by one streaming window. This deliberately optimizes recognition on a
// correctly selected speech input; an unrelated non-speech source can echo the
// vocabulary because Whisper treats prompts as prior transcript.
func TestPromptFallsBackToTheDictionary(t *testing.T) {
	ctx := &recordingContext{}
	e := &Engine{context: ctx, dictionary: []string{"Sussurro", "whisper.cpp"}}

	e.mutex.Lock()
	e.setPromptLocked(composePrompt(e.dictionary, "text from a streaming pass"))
	e.resetPromptLocked()
	e.mutex.Unlock()

	if got, want := ctx.last(), "Sussurro, whisper.cpp."; got != want {
		t.Errorf("prompt reset to %q, want %q", got, want)
	}
}

func TestSetDictionaryCanReplaceAndClearTheLivePrompt(t *testing.T) {
	ctx := &recordingContext{}
	e := &Engine{context: ctx}
	terms := []string{"Sussurro"}

	e.SetDictionary(terms)
	terms[0] = "mutated by caller"
	if got := composePrompt(e.dictionary, ""); got != "Sussurro." {
		t.Errorf("dictionary aliases caller's slice: %q", got)
	}

	e.SetDictionary(nil)
	if got := ctx.last(); got != "" {
		t.Errorf("prompt after clearing dictionary = %q, want empty", got)
	}
	if len(e.dictionary) != 0 {
		t.Errorf("dictionary after clearing = %#v, want empty", e.dictionary)
	}
}

// whisper.cpp keeps the tail of an over-long prompt, so the dictionary has to
// come after the preceding transcript or it is the first thing truncated once
// the settled text outgrows the prompt budget.
func TestComposePromptPutsDictionaryLast(t *testing.T) {
	got := composePrompt([]string{"Sussurro"}, "some preceding text")
	if want := "some preceding text Sussurro."; got != want {
		t.Errorf("composePrompt = %q, want %q", got, want)
	}
	if got := composePrompt([]string{"Sussurro"}, "  "); got != "Sussurro." {
		t.Errorf("composePrompt with no preceding text = %q, want the dictionary alone", got)
	}
	if got := composePrompt(nil, "some preceding text"); got != "some preceding text" {
		t.Errorf("composePrompt with no dictionary = %q, want the preceding text alone", got)
	}
}

// The binding copies each prompt into a C string it never frees, so a
// streaming session that re-set the same prompt every pass leaked one string
// per pass. An unchanged prompt must not reach the context at all.
func TestUnchangedPromptIsNotReset(t *testing.T) {
	ctx := &recordingContext{}
	e := &Engine{context: ctx, dictionary: []string{"Sussurro"}}

	e.mutex.Lock()
	e.setPromptLocked(composePrompt(e.dictionary, "the same window"))
	e.setPromptLocked(composePrompt(e.dictionary, "the same window"))
	e.resetPromptLocked()
	e.resetPromptLocked()
	e.mutex.Unlock()

	want := []string{"the same window Sussurro.", "Sussurro."}
	if len(ctx.prompts) != len(want) {
		t.Fatalf("SetInitialPrompt called with %q, want exactly %q", ctx.prompts, want)
	}
	for i := range want {
		if ctx.prompts[i] != want[i] {
			t.Errorf("prompt %d = %q, want %q", i, ctx.prompts[i], want[i])
		}
	}

	// A fresh context has no prompt: clearing an already-empty dictionary
	// must not set an empty string either.
	fresh := &recordingContext{}
	(&Engine{context: fresh}).SetDictionary(nil)
	if len(fresh.prompts) != 0 {
		t.Errorf("SetInitialPrompt called with %q on a fresh context, want no call", fresh.prompts)
	}
}

// whisper treats the prompt as prior transcript, and an unterminated term list
// made it drop every full stop in multi-sentence dictation (sussurro-916).
func TestDictionaryPromptEndsASentence(t *testing.T) {
	cases := map[string]struct {
		terms []string
		want  string
	}{
		"list gains a full stop": {[]string{"dolt", "tailscale"}, "dolt, tailscale."},
		"existing mark is kept":  {[]string{"dolt", "Yahoo!"}, "dolt, Yahoo!"},
		"empty stays empty":      {nil, ""},
	}
	for name, tc := range cases {
		if got := dictionaryPrompt(tc.terms); got != tc.want {
			t.Errorf("%s: dictionaryPrompt(%q) = %q, want %q", name, tc.terms, got, tc.want)
		}
	}
}
