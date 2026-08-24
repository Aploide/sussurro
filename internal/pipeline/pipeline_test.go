package pipeline

import (
	"errors"
	"io"
	"log/slog"
	"math"
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

type sequenceTranscriber struct {
	texts []string
	errs  []error
	calls int
}

func (s *sequenceTranscriber) Transcribe([]float32) (string, error) {
	call := s.calls
	s.calls++
	var err error
	if call < len(s.errs) {
		err = s.errs[call]
	}
	return s.texts[call], err
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

type passthroughCleaner struct {
	calls int
}

func (c *passthroughCleaner) CleanupText(text string) (string, error) {
	c.calls++
	return text, nil
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
		// Below the recognition floor, where whisper invents stock phrases.
		{name: "too short", asr: &stubTranscriber{text: "the quick brown fox"}, samples: samplesFor(0.1)},
		{name: "asr error", asr: &stubTranscriber{err: errors.New("asr failed")}, samples: samplesFor(3)},
		{name: "no speech", asr: &stubTranscriber{text: "   "}, samples: samplesFor(3)},
		// Nothing but a non-speech marker is no transcription at all.
		{name: "marker only", asr: &stubTranscriber{text: "[BLANK_AUDIO]"}, samples: samplesFor(3)},
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
	p.isRecording.Store(true)
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

	p.isRecording.Store(true)
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

	p.isRecording.Store(true)
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

const completePartial = "the complete sentence includes all of these ending words"

// primePartial runs one deterministic streaming pass without letting a real
// ticker launch extra recognitions in the background.
func primePartial(t *testing.T, p *Pipeline, asr transcriber) *Streamer {
	t.Helper()
	ticker := newManualTicker()
	streamer := NewStreamer(asr, staticSnapshot(32000), nil, time.Millisecond, testSampleRate,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	streamer.newTicker = func() Ticker { return ticker }
	p.SetStreamer(streamer)
	streamer.Start()
	streamer.runPass(streamer.Generation())
	t.Cleanup(func() {
		streamer.Stop()
		streamer.Wait()
	})
	return streamer
}

// TestFinalPassCannotTruncateTheLastPartial reproduces sussurro-3jn through
// the real stop path. Whisper's final whole-buffer pass ended mid-sentence
// even though the last streaming pass already held the complete text; blindly
// preferring the final pass then discarded words the user had seen.
func TestFinalPassCannotTruncateTheLastPartial(t *testing.T) {
	asr := &sequenceTranscriber{texts: []string{completePartial, "the complete sentence includes"}}
	cleaner := &stubCleaner{text: "the complete sentence includes"}
	consumer := &recordingConsumer{}
	p := newTestPipeline(t, asr, cleaner, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(consumer)
	streamer := primePartial(t, p, asr)

	// The partial saw two seconds but recording continued to four, forcing the
	// final ASR path rather than the partial-reuse shortcut.
	p.isRecording.Store(true)
	p.audioBuffer = make([]float32, 64000)
	if !p.StopRecording() {
		t.Fatal("StopRecording() = false, want the recording stopped")
	}
	p.wg.Wait()
	streamer.Wait()

	if asr.calls != 2 {
		t.Fatalf("Transcribe called %d times, want partial plus final", asr.calls)
	}
	if cleaner.calls != 1 {
		t.Fatalf("CleanupText called %d times, want the normal cleanup path", cleaner.calls)
	}
	if len(consumer.results) != 1 {
		t.Fatalf("got %d results, want 1", len(consumer.results))
	}
	if got := consumer.results[0]; got.Raw != completePartial || got.Text != completePartial || got.Cleaned {
		t.Errorf("delivered Raw=%q Text=%q Cleaned=%v, want the complete last partial %q", got.Raw, got.Text, got.Cleaned, completePartial)
	}
}

func TestFinalPassFailurePreservesTheLastPartial(t *testing.T) {
	asr := &sequenceTranscriber{
		texts: []string{completePartial, ""},
		errs:  []error{nil, errors.New("decoder failed")},
	}
	consumer := &recordingConsumer{}
	p := newTestPipeline(t, asr, &passthroughCleaner{}, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(consumer)
	streamer := primePartial(t, p, asr)

	p.isRecording.Store(true)
	p.audioBuffer = make([]float32, 64000)
	if !p.StopRecording() {
		t.Fatal("StopRecording() = false, want the recording stopped")
	}
	p.wg.Wait()
	streamer.Wait()

	if len(consumer.results) != 1 || consumer.results[0].Raw != completePartial {
		t.Fatalf("results = %+v, want the complete last partial", consumer.results)
	}
}

func TestMaxDurationCannotTruncateTheLastPartial(t *testing.T) {
	asr := &sequenceTranscriber{texts: []string{completePartial, "the complete sentence includes"}}
	consumer := &recordingConsumer{}
	p := newTestPipeline(t, asr, &passthroughCleaner{}, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(consumer)
	streamer := primePartial(t, p, asr)

	p.isRecording.Store(true)
	p.audioBuffer = make([]float32, 64000)
	p.handleCapturedChunk([]float32{1}, 64000)
	p.wg.Wait()
	streamer.Wait()

	if p.isRecording.Load() {
		t.Fatal("recording still active after reaching the maximum duration")
	}
	if asr.calls != 2 {
		t.Fatalf("Transcribe called %d times, want partial plus final", asr.calls)
	}
	if len(consumer.results) != 1 || consumer.results[0].Raw != completePartial {
		t.Fatalf("results = %+v, want the complete last partial", consumer.results)
	}
}

func TestFinalPassShorter(t *testing.T) {
	tests := []struct {
		name    string
		final   string
		partial string
		want    bool
	}{
		{name: "aligned suffix missing", final: "Hello world", partial: "hello, world! More words.", want: true},
		{name: "suffix missing after correction", final: "why did it add a yep", partial: "why did it insert a yep at the beginning which wasn't there before", want: true},
		{name: "empty final", final: "", partial: "speech was already recognised", want: true},
		{name: "shorter correction", final: "I need this sentence", partial: "I I need this sentence", want: true},
		{name: "longer final", final: "the final added words", partial: "the final", want: false},
		{name: "same length correction", final: "choose final wording", partial: "retain prior wording", want: false},
		{name: "no partial", final: "some result", partial: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := finalPassShorter(tt.final, tt.partial); got != tt.want {
				t.Errorf("finalPassShorter(%q, %q) = %v, want %v", tt.final, tt.partial, got, tt.want)
			}
		})
	}
}

func TestStartRecordingIsIgnoredAfterStop(t *testing.T) {
	p := newTestPipeline(t, &stubTranscriber{}, &stubCleaner{}, stubContext{})
	p.audioChan = make(chan []float32)
	p.stopChan = make(chan struct{})

	p.Stop()
	p.StartRecording()

	if p.isRecording.Load() {
		t.Fatal("StartRecording restarted a stopped pipeline")
	}
}

func TestStopTranscribesNormallyWithoutAStreamer(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{text: "The quick brown fox."}
	consumer := &recordingConsumer{}

	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(consumer)

	p.isRecording.Store(true)
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

// The defect in sussurro-xvj.52: a short phrase was discarded by a four-word
// floor and never reached the clipboard.
func TestShortDictationIsDelivered(t *testing.T) {
	for _, text := range []string{"something very short", "yes", "no thanks"} {
		t.Run(text, func(t *testing.T) {
			llm := &stubCleaner{text: text}
			consumer := &recordingConsumer{}
			p := newTestPipeline(t, &stubTranscriber{text: text}, llm,
				stubContext{info: &ctxProvider.ContextInfo{}})
			p.SetResultConsumer(consumer)

			run(p, samplesFor(1))

			if len(consumer.results) != 1 {
				t.Fatalf("got %d results, want the dictation delivered", len(consumer.results))
			}
			if got := consumer.results[0].Text; got != text {
				t.Errorf("Text = %q, want %q", got, text)
			}
		})
	}
}

// A recording below the floor must say so rather than vanish.
func TestTooShortRecordingIsReported(t *testing.T) {
	notifier := &finishNotifier{}
	p := newTestPipeline(t, &stubTranscriber{text: "the quick brown fox"},
		&stubCleaner{text: "cleaned"}, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(&recordingConsumer{})
	p.uiNotifier = notifier

	run(p, samplesFor(0.1))

	if len(notifier.finished) != 1 || notifier.finished[0] != tooShortMessage {
		t.Errorf("OnFinished = %q, want the too-short message reported", notifier.finished)
	}
}

func TestCompletionWithNoResultCarriesNoText(t *testing.T) {
	// A recording that produced nothing must not display the previous one's
	// text on the way out. The rejection here is empty recogniser output; a
	// short-but-real transcription is no longer rejected (sussurro-xvj.52).
	asr := &stubTranscriber{text: "   "}
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

// TestPhaseMessageNeverCallsWorkTranscribing guards the log side of
// sussurro-xvj.34 independently from the overlay wording.
func TestPhaseMessageNeverCallsWorkTranscribing(t *testing.T) {
	for _, test := range []struct {
		state session.State
		want  string
	}{
		{session.StateTranscribing, "Finalizing..."},
		{session.StateCleaningUp, "Cleaning up..."},
	} {
		got := phaseMessage(test.state)
		if got != test.want {
			t.Errorf("phaseMessage(%s) = %q, want %q", test.state, got, test.want)
		}
		if strings.Contains(strings.ToLower(got), "transcrib") {
			t.Errorf("phaseMessage(%s) exposed forbidden wording %q", test.state, got)
		}
	}
}

// TestStopReasonDistinguishesReleaseFromCap covers sussurro-xvj.37: a
// recording that ends early must be separable in the log from one the user
// ended, otherwise the cause cannot be identified after the fact.
func TestStopReasonDistinguishesReleaseFromCap(t *testing.T) {
	if StopReleased == StopMaxDuration {
		t.Fatal("stop reasons must be distinguishable")
	}
	for _, reason := range []StopReason{StopReleased, StopMaxDuration} {
		if string(reason) == "" {
			t.Errorf("stop reason %v has no name", reason)
		}
	}
}

// TestDroppedFramesAreCounted checks the counter the stop log reports. A
// silent drop loses speech with no trace, which is what made the fault
// impossible to diagnose.
func TestDroppedFramesAreCounted(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{text: "The quick brown fox."}
	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})

	if got := p.droppedFrames.Load(); got != 0 {
		t.Fatalf("droppedFrames = %d before any drop, want 0", got)
	}

	p.onFrameDropped()
	p.onFrameDropped()

	if got := p.droppedFrames.Load(); got != 2 {
		t.Errorf("droppedFrames = %d, want 2", got)
	}
}

// TestDropCounterResetsPerRecording keeps the count attributable to one
// utterance rather than accumulating across a session.
func TestDropCounterResetsPerRecording(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{text: "The quick brown fox."}
	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})

	p.onFrameDropped()
	p.StartRecording()

	if got := p.droppedFrames.Load(); got != 0 {
		t.Errorf("droppedFrames = %d after StartRecording, want the count reset", got)
	}
}

// TestStopRecordingReportsElapsed checks a recording start time is captured,
// so the stop log can say how long it actually ran.
func TestStopRecordingReportsElapsed(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{text: "The quick brown fox."}
	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(&recordingConsumer{})

	p.StartRecording()
	if p.recordingStart.IsZero() {
		t.Fatal("recordingStart not set by StartRecording")
	}

	if !p.stopRecordingBecause(StopReleased) {
		t.Fatal("stopRecordingBecause returned false for an active recording")
	}
	// A stop with nothing recording must report that, not invent a session.
	if p.stopRecordingBecause(StopReleased) {
		t.Error("stopRecordingBecause returned true when not recording")
	}
}

// TestBufferFillReportsProgress covers sussurro-xvj.42: the cap truncates
// speech mid sentence, so the UI needs to see it coming.
func TestBufferFillReportsProgress(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{text: "The quick brown fox."}
	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})

	p.maxSamples.Store(1000)
	p.audioBuffer = make([]float32, 250)

	fill, bounded := p.BufferFill()
	if !bounded {
		t.Fatal("BufferFill() reported no limit, want a bounded buffer")
	}
	if fill != 0.25 {
		t.Errorf("fill = %v, want 0.25", fill)
	}
}

// TestBufferFillIsUnboundedWhenInfinite checks an unlimited recording reports
// no meaningful fill, so the UI shows no indicator rather than one pinned at
// zero forever.
func TestBufferFillIsUnboundedWhenInfinite(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{text: "The quick brown fox."}
	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})

	p.maxSamples.Store(math.MaxInt32)
	p.audioBuffer = make([]float32, 4096)

	if _, bounded := p.BufferFill(); bounded {
		t.Error("BufferFill() reported a limit for an infinite recording")
	}
}

// TestBufferFillClampsAtFull keeps the fraction usable as a bar width even if
// the buffer briefly exceeds the cap before the stop takes effect.
func TestBufferFillClampsAtFull(t *testing.T) {
	asr := &stubTranscriber{text: "the quick brown fox"}
	llm := &stubCleaner{text: "The quick brown fox."}
	p := newTestPipeline(t, asr, llm, stubContext{info: &ctxProvider.ContextInfo{}})

	p.maxSamples.Store(100)
	p.audioBuffer = make([]float32, 250)

	fill, bounded := p.BufferFill()
	if !bounded {
		t.Fatal("BufferFill() reported no limit")
	}
	if fill != 1 {
		t.Errorf("fill = %v, want it clamped to 1", fill)
	}
}
