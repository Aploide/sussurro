package pipeline

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/aploide/sussurro/internal/audio"
	ctxProvider "github.com/aploide/sussurro/internal/context"
)

const testSampleRate = 16000

type stubTranscriber struct {
	text  string
	err   error
	calls int
}

func (s *stubTranscriber) Transcribe(samples []float32) (string, error) {
	s.calls++
	return s.text, s.err
}

type stubCleaner struct {
	text  string
	err   error
	calls int
}

func (s *stubCleaner) CleanupText(rawText string) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.text, nil
}

type stubContext struct {
	info *ctxProvider.ContextInfo
	err  error
}

func (s stubContext) GetContext() (*ctxProvider.ContextInfo, error) { return s.info, s.err }
func (s stubContext) Close() error                                  { return nil }

type recordingConsumer struct {
	results []Result
}

func (c *recordingConsumer) OnResult(result Result) { c.results = append(c.results, result) }

// newTestPipeline builds a pipeline exercising only the result path: no audio
// engine is started, so processSegment can run against stub engines.
func newTestPipeline(t *testing.T, asr transcriber, llm cleaner, provider ctxProvider.Provider) *Pipeline {
	t.Helper()
	vadParams := audio.DefaultVADParams()
	vadParams.SampleRate = testSampleRate
	return &Pipeline{
		asrEngine:   asr,
		llmEngine:   llm,
		ctxProvider: provider,
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		vadParams:   vadParams,
	}
}

// samplesFor returns a silent buffer of the given duration in seconds.
func samplesFor(seconds float64) []float32 {
	return make([]float32, int(seconds*testSampleRate))
}

// run drives processSegment synchronously; it owns the WaitGroup increment that
// StopRecording would normally perform.
func run(p *Pipeline, samples []float32) {
	p.wg.Add(1)
	p.processSegment(samples)
}

func TestProcessSegmentPublishesCleanedResult(t *testing.T) {
	asr := &stubTranscriber{text: "um the quick brown fox"}
	llm := &stubCleaner{text: "The quick brown fox."}
	info := &ctxProvider.ContextInfo{AppName: "editor", WindowTitle: "notes"}
	consumer := &recordingConsumer{}

	p := newTestPipeline(t, asr, llm, stubContext{info: info})
	p.SetResultConsumer(consumer)
	run(p, samplesFor(3))

	if len(consumer.results) != 1 {
		t.Fatalf("got %d results, want exactly 1", len(consumer.results))
	}
	got := consumer.results[0]
	if got.Raw != asr.text {
		t.Errorf("Raw = %q, want %q", got.Raw, asr.text)
	}
	if got.Text != llm.text {
		t.Errorf("Text = %q, want %q", got.Text, llm.text)
	}
	if !got.Cleaned {
		t.Error("Cleaned = false, want true")
	}
	if got.Context.AppName != "editor" || got.Context.WindowTitle != "notes" {
		t.Errorf("Context = %+v, want editor/notes", got.Context)
	}
}

func TestProcessSegmentRawModeSkipsCleanup(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{text: "should not be used"}
	consumer := &recordingConsumer{}

	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(consumer)
	p.SetSkipLLMCleanup(true)
	run(p, samplesFor(3))

	if llm.calls != 0 {
		t.Fatalf("CleanupText called %d times in raw mode, want 0", llm.calls)
	}
	if len(consumer.results) != 1 {
		t.Fatalf("got %d results, want 1", len(consumer.results))
	}
	got := consumer.results[0]
	if got.Text != asr.text {
		t.Errorf("Text = %q, want raw %q", got.Text, asr.text)
	}
	if got.Cleaned {
		t.Error("Cleaned = true, want false in raw mode")
	}
}

func TestProcessSegmentCleanupFailureFallsBackToRaw(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{err: errors.New("model unavailable")}
	consumer := &recordingConsumer{}

	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(consumer)
	run(p, samplesFor(3))

	if len(consumer.results) != 1 {
		t.Fatalf("got %d results, want 1", len(consumer.results))
	}
	if got := consumer.results[0]; got.Text != asr.text || got.Cleaned {
		t.Errorf("got Text=%q Cleaned=%v, want raw fallback uncleaned", got.Text, got.Cleaned)
	}
}

func TestProcessSegmentLowercaseAppliesToDeliveredText(t *testing.T) {
	asr := &stubTranscriber{text: "The Quick Brown Fox"}
	llm := &stubCleaner{text: "The Quick Brown Fox."}
	consumer := &recordingConsumer{}

	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(consumer)
	p.SetLowercaseOutput(true)
	run(p, samplesFor(3))

	if len(consumer.results) != 1 {
		t.Fatalf("got %d results, want 1", len(consumer.results))
	}
	if got := consumer.results[0].Text; got != strings.ToLower(llm.text) {
		t.Errorf("Text = %q, want lowercased", got)
	}
}

func TestProcessSegmentSuppressesNonResults(t *testing.T) {
	tests := []struct {
		name    string
		asr     *stubTranscriber
		samples []float32
	}{
		{name: "empty buffer", asr: &stubTranscriber{text: "the quick brown fox"}, samples: nil},
		{name: "too short", asr: &stubTranscriber{text: "the quick brown fox"}, samples: samplesFor(1)},
		{name: "asr error", asr: &stubTranscriber{err: errors.New("asr failed")}, samples: samplesFor(3)},
		{name: "too few words", asr: &stubTranscriber{text: "hello there"}, samples: samplesFor(3)},
		{name: "no speech", asr: &stubTranscriber{text: "   "}, samples: samplesFor(3)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := &stubCleaner{text: "cleaned"}
			consumer := &recordingConsumer{}
			p := newTestPipeline(t, tt.asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
			p.SetResultConsumer(consumer)
			run(p, tt.samples)

			if len(consumer.results) != 0 {
				t.Fatalf("got %d results, want none", len(consumer.results))
			}
		})
	}
}

func TestProcessSegmentSurvivesMissingContext(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{text: "The quick brown fox."}
	consumer := &recordingConsumer{}

	// A failing provider returns no ContextInfo; the result path must not panic.
	p := newTestPipeline(t, asr, llm, stubContext{err: errors.New("no display")})
	p.SetResultConsumer(consumer)
	run(p, samplesFor(3))

	if len(consumer.results) != 1 {
		t.Fatalf("got %d results, want 1", len(consumer.results))
	}
	if got := consumer.results[0].Context; got.AppName != "" || got.WindowTitle != "" {
		t.Errorf("Context = %+v, want zero value", got)
	}
}

func TestProcessSegmentWithoutConsumerDoesNotPanic(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{text: "The quick brown fox."}

	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
	run(p, samplesFor(3))
}

func TestProcessSegmentClearsTranscribingAndSignalsCompletion(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{text: "The quick brown fox."}
	completions := 0

	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetOnCompletion(func() { completions++ })
	p.isTranscribing = true
	run(p, samplesFor(3))

	if p.isTranscribing {
		t.Error("isTranscribing = true after processing, want false")
	}
	if completions != 1 {
		t.Errorf("onCompletion called %d times, want 1", completions)
	}
}

func TestResultEmpty(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "text", text: "hello", want: false},
		{name: "blank", text: "  \n\t ", want: true},
		{name: "unset", text: "", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Result{Text: tt.text}).Empty(); got != tt.want {
				t.Errorf("Empty() = %v, want %v", got, tt.want)
			}
		})
	}
}
