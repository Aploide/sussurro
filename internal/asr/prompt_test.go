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
	e.context.SetInitialPrompt("text from a streaming pass")
	e.resetPromptLocked()
	e.mutex.Unlock()

	if got := ctx.last(); got != "" {
		t.Errorf("prompt left as %q after reset; it must be cleared so it "+
			"cannot leak into the final pass", got)
	}
}

// A vocabulary list must never become a standing prompt. Whisper can decode
// an initial prompt as a continuation when the input is ambient noise; in the
// observed failure, a configured `sshfs` became both the streaming partial and
// the delivered transcript even though nobody said it (sussurro-99o).
// Dictionary normalization therefore belongs after recognition, where it can
// only change text Whisper actually returned.
func TestPromptResetNeverRetainsVocabulary(t *testing.T) {
	ctx := &recordingContext{}
	e := &Engine{context: ctx}

	e.mutex.Lock()
	e.context.SetInitialPrompt("dolt, sshfs")
	e.resetPromptLocked()
	e.mutex.Unlock()

	if got := ctx.last(); got != "" {
		t.Errorf("prompt reset to %q, want empty", got)
	}
}
