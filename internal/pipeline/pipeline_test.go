package pipeline

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aploide/sussurro/internal/audio"
	ctxProvider "github.com/aploide/sussurro/internal/context"
	"github.com/aploide/sussurro/internal/session"
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

func TestSnapshotRecordingReportsNoRecordingWhenIdle(t *testing.T) {
	p := newTestPipeline(t, &stubTranscriber{}, &stubCleaner{}, stubContext{})

	if _, recording := p.SnapshotRecording(); recording {
		t.Error("SnapshotRecording() reported a recording while idle")
	}
}

func TestSnapshotRecordingCopiesBuffer(t *testing.T) {
	p := newTestPipeline(t, &stubTranscriber{}, &stubCleaner{}, stubContext{})
	p.isRecording = true
	p.audioBuffer = []float32{0.1, 0.2, 0.3}

	snapshot, recording := p.SnapshotRecording()
	if !recording {
		t.Fatal("SnapshotRecording() reported no recording while recording")
	}
	if len(snapshot) != 3 {
		t.Fatalf("snapshot length = %d, want 3", len(snapshot))
	}

	// Mutating the snapshot must not disturb the live buffer, and vice versa.
	snapshot[0] = 9
	if p.audioBuffer[0] != 0.1 {
		t.Error("snapshot aliases the live audio buffer")
	}
	p.audioBuffer = append(p.audioBuffer, 0.4)
	if len(snapshot) != 3 {
		t.Error("appending to the live buffer changed an earlier snapshot")
	}
}

// staticSnapshot returns a fixed-size buffer, standing in for captured audio.
func staticSnapshot(samples int) SnapshotFunc {
	return func() ([]float32, bool) { return make([]float32, samples), true }
}

func TestStopReusesPartialCoveringTheWholeRecording(t *testing.T) {
	asr := &stubTranscriber{text: "the streamed text here"}
	llm := &stubCleaner{text: "The streamed text here."}
	consumer := &recordingConsumer{}

	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(consumer)

	streamer := NewStreamer(asr, staticSnapshot(48000), nil, time.Millisecond, testSampleRate,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.SetStreamer(streamer)

	// A partial that already saw every sample the recording holds.
	streamer.Start()
	streamer.runPass(streamer.Generation())

	p.isRecording = true
	p.audioBuffer = make([]float32, 48000)
	if !p.StopRecording() {
		t.Fatal("StopRecording() = false, want the recording stopped")
	}
	p.wg.Wait()

	if len(consumer.results) != 1 {
		t.Fatalf("got %d results, want 1", len(consumer.results))
	}
	// One ASR call: the partial. The final pass must have been skipped.
	if asr.calls != 1 {
		t.Errorf("Transcribe called %d times, want 1 (partial reused)", asr.calls)
	}
	if got := consumer.results[0].Raw; got != "the streamed text here" {
		t.Errorf("Raw = %q, want the streamed text", got)
	}
}

func TestStopRetranscribesWhenAudioArrivedAfterThePartial(t *testing.T) {
	asr := &stubTranscriber{text: "the streamed text here"}
	llm := &stubCleaner{text: "The streamed text here."}
	consumer := &recordingConsumer{}

	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(consumer)

	// The partial saw two seconds; the recording ended up holding four.
	streamer := NewStreamer(asr, staticSnapshot(32000), nil, time.Millisecond, testSampleRate,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.SetStreamer(streamer)
	streamer.Start()
	streamer.runPass(streamer.Generation())

	p.isRecording = true
	p.audioBuffer = make([]float32, 64000)
	if !p.StopRecording() {
		t.Fatal("StopRecording() = false, want the recording stopped")
	}
	p.wg.Wait()

	// Reusing here would silently drop the last two seconds of speech.
	if asr.calls != 2 {
		t.Errorf("Transcribe called %d times, want 2 (partial plus final pass)", asr.calls)
	}
}

func TestStopTranscribesNormallyWithoutAStreamer(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{text: "The quick brown fox."}
	consumer := &recordingConsumer{}

	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(consumer)

	p.isRecording = true
	p.audioBuffer = make([]float32, 48000)
	if !p.StopRecording() {
		t.Fatal("StopRecording() = false, want the recording stopped")
	}
	p.wg.Wait()

	if len(consumer.results) != 1 {
		t.Fatalf("got %d results, want 1", len(consumer.results))
	}
	if asr.calls != 1 {
		t.Errorf("Transcribe called %d times, want 1", asr.calls)
	}
}

// finishNotifier records the state notifications a pipeline emits, including
// the optional text-carrying ones.
type finishNotifier struct {
	states   []session.State
	phases   []session.State
	finished []string
	partials []string
}

func (n *finishNotifier) OnStateChange(state session.State) { n.states = append(n.states, state) }
func (n *finishNotifier) OnRMSData(float32)                 {}
func (n *finishNotifier) OnPhase(state session.State, partial string) {
	n.phases = append(n.phases, state)
	n.partials = append(n.partials, partial)
}
func (n *finishNotifier) OnFinished(text string) { n.finished = append(n.finished, text) }

func TestCompletionCarriesTheFinalText(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{text: "The quick brown fox."}
	notifier := &finishNotifier{}

	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(&recordingConsumer{})
	p.uiNotifier = notifier

	run(p, samplesFor(3))

	// Without the text, the overlay learns only that the session ended and
	// hides without ever showing what was produced.
	if len(notifier.finished) != 1 {
		t.Fatalf("OnFinished called %d times, want 1", len(notifier.finished))
	}
	if notifier.finished[0] != "The quick brown fox." {
		t.Errorf("OnFinished text = %q, want the delivered text", notifier.finished[0])
	}
}

func TestCompletionWithNoResultCarriesNoText(t *testing.T) {
	// A recording that produced nothing must not display the previous one's
	// text on the way out.
	asr := &stubTranscriber{text: "too short"}
	llm := &stubCleaner{text: "unused"}
	notifier := &finishNotifier{}

	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(&recordingConsumer{})
	p.uiNotifier = notifier

	run(p, samplesFor(3))

	if len(notifier.finished) != 0 {
		t.Errorf("OnFinished called with %q for a rejected result, want no text",
			notifier.finished)
	}
}

// TestReusePathNeverReportsTranscribing covers the case that prompted
// sussurro-xvj.34: when the streaming partial is reused, no ASR runs at all,
// so telling the user "Transcribing" describes work that is not happening.
func TestReusePathNeverReportsTranscribing(t *testing.T) {
	asr := &stubTranscriber{text: "unused by the reuse path"}
	llm := &stubCleaner{text: "The quick brown fox."}
	notifier := &finishNotifier{}

	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(&recordingConsumer{})
	p.uiNotifier = notifier

	p.wg.Add(1)
	p.completeFromPartial("the quick brown fox")
	p.notifyPhase(session.StateCleaningUp, "the quick brown fox")

	for _, state := range notifier.phases {
		if state == session.StateTranscribing {
			t.Error("reuse path reported StateTranscribing; no ASR runs on this path")
		}
	}
}

// TestFinalPassMovesFromTranscribingToCleaningUp checks the phases are
// reported in order, so the label stops saying "Transcribing" once
// recognition has finished and only cleanup remains.
func TestFinalPassMovesFromTranscribingToCleaningUp(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{text: "The quick brown fox."}
	notifier := &finishNotifier{}

	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(&recordingConsumer{})
	p.uiNotifier = notifier

	run(p, samplesFor(3))

	if len(notifier.phases) != 1 {
		t.Fatalf("phases = %v, want exactly the post-ASR transition", notifier.phases)
	}
	if notifier.phases[0] != session.StateCleaningUp {
		t.Errorf("phase after ASR = %s, want cleaning up", notifier.phases[0])
	}
}
