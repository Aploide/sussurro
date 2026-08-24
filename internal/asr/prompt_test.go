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
	e.context.SetInitialPrompt(composePrompt(e.dictionary, "text from a streaming pass"))
	e.resetPromptLocked()
	e.mutex.Unlock()

	if got := ctx.last(); got != "" {
		t.Errorf("prompt left as %q after reset; it must be cleared so it "+
			"cannot leak into the final pass", got)
	}
}

// With a dictionary the standing prompt is the dictionary itself, not the
// preceding text and not nothing: the terms must survive for later calls.
func TestPromptFallsBackToTheDictionary(t *testing.T) {
	ctx := &recordingContext{}
	e := &Engine{context: ctx, dictionary: []string{"Sussurro", "whisper.cpp"}}

	e.mutex.Lock()
	e.context.SetInitialPrompt(composePrompt(e.dictionary, "text from a streaming pass"))
	e.resetPromptLocked()
	e.mutex.Unlock()

	if got, want := ctx.last(), "Sussurro, whisper.cpp"; got != want {
		t.Errorf("prompt reset to %q, want %q", got, want)
	}
}

// Dictionary terms lead so they survive whisper truncating the prompt to
// n_text_ctx/2; the preceding transcript is the part that may be cut.
func TestComposePromptPutsDictionaryFirst(t *testing.T) {
	got := composePrompt([]string{"Sussurro"}, "some preceding text")
	if want := "Sussurro. some preceding text"; got != want {
		t.Errorf("composePrompt = %q, want %q", got, want)
	}
}
