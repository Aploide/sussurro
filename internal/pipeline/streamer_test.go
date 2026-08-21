package pipeline

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// manualTicker lets a test decide exactly when a partial pass becomes due.
type manualTicker struct {
	ch      chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func newManualTicker() *manualTicker {
	return &manualTicker{ch: make(chan time.Time), stopped: make(chan struct{})}
}

func (m *manualTicker) C() <-chan time.Time { return m.ch }
func (m *manualTicker) Stop()               { m.once.Do(func() { close(m.stopped) }) }

// tick delivers one tick, failing the test if the streamer never collects it.
func (m *manualTicker) tick(t *testing.T) {
	t.Helper()
	select {
	case m.ch <- time.Now():
	case <-time.After(time.Second):
		t.Fatal("timed out delivering tick")
	}
}

// blockingTranscriber holds each call until the test releases it, so overlap
// and coalescing are observable rather than timing-dependent.
type blockingTranscriber struct {
	entered chan []float32
	release chan string
	err     error

	mu       sync.Mutex
	active   int
	maxActiv int
	calls    int
}

func newBlockingTranscriber() *blockingTranscriber {
	return &blockingTranscriber{
		entered: make(chan []float32, 16),
		release: make(chan string, 16),
	}
}

func (b *blockingTranscriber) Transcribe(samples []float32) (string, error) {
	b.mu.Lock()
	b.calls++
	b.active++
	if b.active > b.maxActiv {
		b.maxActiv = b.active
	}
	b.mu.Unlock()

	b.entered <- samples
	text := <-b.release

	b.mu.Lock()
	b.active--
	b.mu.Unlock()

	return text, b.err
}

// drain unblocks any inference still waiting for a release, so a failing test
// tears down instead of deadlocking in Streamer.Wait.
func (b *blockingTranscriber) drain() {
	for i := 0; i < cap(b.release); i++ {
		select {
		case b.release <- "":
		default:
			return
		}
	}
}

func (b *blockingTranscriber) stats() (calls, maxConcurrent int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls, b.maxActiv
}

// awaitEntry waits for the worker to enter an inference.
func (b *blockingTranscriber) awaitEntry(t *testing.T) {
	t.Helper()
	select {
	case <-b.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a partial inference to start")
	}
}

// partialRecorder collects published partial transcriptions.
type partialRecorder struct {
	mu      sync.Mutex
	texts   []string
	gens    []uint64
	updated chan struct{}
}

func newPartialRecorder() *partialRecorder {
	return &partialRecorder{updated: make(chan struct{}, 16)}
}

func (r *partialRecorder) record(generation uint64, text string) {
	r.mu.Lock()
	r.texts = append(r.texts, text)
	r.gens = append(r.gens, generation)
	r.mu.Unlock()
	select {
	case r.updated <- struct{}{}:
	default:
	}
}

func (r *partialRecorder) snapshot() ([]string, []uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.texts...), append([]uint64(nil), r.gens...)
}

func (r *partialRecorder) awaitUpdate(t *testing.T) {
	t.Helper()
	select {
	case <-r.updated:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a partial transcription")
	}
}

// newTestStreamer wires a streamer to a manual ticker and a fixed snapshot.
func newTestStreamer(t *testing.T, asr *blockingTranscriber, onPartial PartialFunc) (*Streamer, *manualTicker) {
	t.Helper()
	ticker := newManualTicker()
	snapshot := func() ([]float32, bool) { return make([]float32, 16000), true }
	s := NewStreamer(asr, snapshot, onPartial, time.Millisecond, testSampleRate, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.newTicker = func() Ticker { return ticker }
	t.Cleanup(func() {
		s.Stop()
		// A failed test can leave an inference blocked on release; unblock it
		// so Wait() cannot deadlock and mask the real failure.
		asr.drain()
		s.Wait()
	})
	return s, ticker
}

func TestStreamerPublishesPartialText(t *testing.T) {
	asr := newBlockingTranscriber()
	recorder := newPartialRecorder()
	s, ticker := newTestStreamer(t, asr, recorder.record)

	generation := s.Start()
	ticker.tick(t)
	asr.awaitEntry(t)
	asr.release <- "hello there"
	recorder.awaitUpdate(t)

	texts, gens := recorder.snapshot()
	if len(texts) != 1 || texts[0] != "hello there" {
		t.Fatalf("partials = %v, want one %q", texts, "hello there")
	}
	if gens[0] != generation {
		t.Errorf("generation = %d, want %d", gens[0], generation)
	}
}

func TestStreamerNeverOverlapsInference(t *testing.T) {
	asr := newBlockingTranscriber()
	recorder := newPartialRecorder()
	s, ticker := newTestStreamer(t, asr, recorder.record)

	s.Start()

	// Hold the first inference open while more ticks arrive.
	ticker.tick(t)
	asr.awaitEntry(t)
	for i := 0; i < 5; i++ {
		ticker.tick(t)
	}

	// Nothing else may start while the first pass is still running.
	select {
	case <-asr.entered:
		t.Fatal("a second inference started while the first was running")
	case <-time.After(100 * time.Millisecond):
	}

	asr.release <- "first"
	recorder.awaitUpdate(t)

	// The coalesced wake permits exactly one follow-up pass.
	asr.awaitEntry(t)
	asr.release <- "second"
	recorder.awaitUpdate(t)

	calls, maxConcurrent := asr.stats()
	if maxConcurrent != 1 {
		t.Errorf("max concurrent inferences = %d, want 1", maxConcurrent)
	}
	// Six ticks arriving during one inference must not each buy a pass.
	if calls > 3 {
		t.Errorf("inference calls = %d for 6 ticks, want them coalesced", calls)
	}
}

func TestStreamerCoalescesTicksWhileIdleQueueStaysBounded(t *testing.T) {
	asr := newBlockingTranscriber()
	recorder := newPartialRecorder()
	s, ticker := newTestStreamer(t, asr, recorder.record)

	s.Start()
	ticker.tick(t)
	asr.awaitEntry(t)

	// Twenty ticks during one slow inference must not queue twenty passes.
	const ticks = 20
	for i := 0; i < ticks; i++ {
		ticker.tick(t)
	}
	asr.release <- "first"
	recorder.awaitUpdate(t)

	// Drain the follow-up passes the coalesced wakes produced. The bound is
	// what matters: the single-slot wake channel plus at most one tick already
	// in flight inside tickLoop can leave no more than two queued passes.
	const maxQueued = 2
	followUps := 0
	for {
		select {
		case <-asr.entered:
			followUps++
			if followUps > maxQueued {
				t.Fatalf("%d ticks queued %d follow-up passes, want at most %d",
					ticks, followUps, maxQueued)
			}
			asr.release <- "later"
		case <-time.After(200 * time.Millisecond):
			if followUps == 0 {
				t.Fatal("no follow-up pass ran after the coalesced ticks")
			}
			calls, _ := asr.stats()
			if calls != followUps+1 {
				t.Errorf("inference calls = %d, want %d", calls, followUps+1)
			}
			return
		}
	}
}

func TestStreamerDiscardsResultFromStoppedSession(t *testing.T) {
	asr := newBlockingTranscriber()
	recorder := newPartialRecorder()
	s, ticker := newTestStreamer(t, asr, recorder.record)

	s.Start()
	ticker.tick(t)
	asr.awaitEntry(t)

	// Stop must not wait for the in-flight pass, so final transcription is
	// never delayed by a partial one.
	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop() blocked on an in-flight inference")
	}

	// The abandoned result must not be published.
	asr.release <- "text from a cancelled session"
	time.Sleep(100 * time.Millisecond)
	if texts, _ := recorder.snapshot(); len(texts) != 0 {
		t.Fatalf("published %v after Stop(), want nothing", texts)
	}
}

func TestStreamerDiscardsSupersededSessionResult(t *testing.T) {
	asr := newBlockingTranscriber()
	recorder := newPartialRecorder()
	s, ticker := newTestStreamer(t, asr, recorder.record)

	first := s.Start()
	ticker.tick(t)
	asr.awaitEntry(t)

	s.Stop()
	second := s.Start()
	if second == first {
		t.Fatalf("second generation = %d, want a value distinct from %d", second, first)
	}

	// The stale pass from the first session finishes late; it must be dropped.
	asr.release <- "stale"
	time.Sleep(100 * time.Millisecond)
	if texts, _ := recorder.snapshot(); len(texts) != 0 {
		t.Fatalf("published %v from a superseded session, want nothing", texts)
	}

	// The new session still works.
	ticker.tick(t)
	asr.awaitEntry(t)
	asr.release <- "fresh"
	recorder.awaitUpdate(t)

	texts, gens := recorder.snapshot()
	if len(texts) != 1 || texts[0] != "fresh" {
		t.Fatalf("partials = %v, want one %q", texts, "fresh")
	}
	if gens[0] != second {
		t.Errorf("generation = %d, want %d", gens[0], second)
	}
}

func TestStreamerSkipsPassWhenNotRecording(t *testing.T) {
	asr := newBlockingTranscriber()
	recorder := newPartialRecorder()
	ticker := newManualTicker()
	s := NewStreamer(asr, func() ([]float32, bool) { return nil, false },
		recorder.record, time.Millisecond, testSampleRate, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.newTicker = func() Ticker { return ticker }
	t.Cleanup(func() { s.Stop(); s.Wait() })

	s.Start()
	ticker.tick(t)

	select {
	case <-asr.entered:
		t.Fatal("transcribed while no recording was in progress")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStreamerSuppressesEmptyAndFailedPasses(t *testing.T) {
	tests := []struct {
		name string
		text string
		err  error
	}{
		{name: "empty text", text: ""},
		{name: "inference error", text: "ignored", err: errors.New("whisper failed")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asr := newBlockingTranscriber()
			asr.err = tt.err
			recorder := newPartialRecorder()
			s, ticker := newTestStreamer(t, asr, recorder.record)

			s.Start()
			ticker.tick(t)
			asr.awaitEntry(t)
			asr.release <- tt.text
			time.Sleep(100 * time.Millisecond)

			if texts, _ := recorder.snapshot(); len(texts) != 0 {
				t.Fatalf("published %v, want nothing", texts)
			}
		})
	}
}

func TestStreamerStartIsIdempotent(t *testing.T) {
	asr := newBlockingTranscriber()
	s, _ := newTestStreamer(t, asr, nil)

	first := s.Start()
	if second := s.Start(); second != first {
		t.Errorf("second Start() = %d, want the running generation %d", second, first)
	}
	if got := s.Generation(); got != first {
		t.Errorf("Generation() = %d, want %d", got, first)
	}
}

func TestStreamerStopIsIdempotent(t *testing.T) {
	asr := newBlockingTranscriber()
	s, _ := newTestStreamer(t, asr, nil)

	s.Start()
	s.Stop()
	// A second Stop must not close the stop channel twice.
	s.Stop()
	s.Wait()
}

func TestStreamerRestartAfterStop(t *testing.T) {
	asr := newBlockingTranscriber()
	recorder := newPartialRecorder()
	s, ticker := newTestStreamer(t, asr, recorder.record)

	s.Start()
	s.Stop()
	s.Wait()

	s.Start()
	ticker.tick(t)
	asr.awaitEntry(t)
	asr.release <- "second session"
	recorder.awaitUpdate(t)

	if texts, _ := recorder.snapshot(); len(texts) != 1 || texts[0] != "second session" {
		t.Fatalf("partials = %v, want one %q", texts, "second session")
	}
}

func TestStopAndTakePartialReturnsTheLastResult(t *testing.T) {
	asr := newBlockingTranscriber()
	recorder := newPartialRecorder()
	s, ticker := newTestStreamer(t, asr, recorder.record)

	s.Start()
	ticker.tick(t)
	asr.awaitEntry(t)
	asr.release <- "the transcribed text"
	recorder.awaitUpdate(t)

	text, samples, ok := s.StopAndTakePartial()
	if !ok {
		t.Fatal("StopAndTakePartial() ok = false, want the last partial")
	}
	if text != "the transcribed text" {
		t.Errorf("text = %q, want the last partial", text)
	}
	// The stub snapshot returns one second of audio.
	if samples != 16000 {
		t.Errorf("samples = %d, want the count the partial covered", samples)
	}
}

func TestStopAndTakePartialWithNoPartial(t *testing.T) {
	asr := newBlockingTranscriber()
	s, _ := newTestStreamer(t, asr, nil)

	s.Start()
	if _, _, ok := s.StopAndTakePartial(); ok {
		t.Error("ok = true with no partial produced, want false")
	}
}

func TestStopAndTakePartialWhenNotRunning(t *testing.T) {
	asr := newBlockingTranscriber()
	s, _ := newTestStreamer(t, asr, nil)

	if _, _, ok := s.StopAndTakePartial(); ok {
		t.Error("ok = true for a streamer that never started, want false")
	}
}

func TestPartialDoesNotCarryIntoTheNextSession(t *testing.T) {
	asr := newBlockingTranscriber()
	recorder := newPartialRecorder()
	s, ticker := newTestStreamer(t, asr, recorder.record)

	s.Start()
	ticker.tick(t)
	asr.awaitEntry(t)
	asr.release <- "text from the first session"
	recorder.awaitUpdate(t)
	s.Stop()

	// A new recording must not inherit the previous one's text: delivering
	// it would put words the user never said into their document.
	s.Start()
	if text, _, ok := s.StopAndTakePartial(); ok {
		t.Errorf("second session returned %q from the first, want nothing", text)
	}
}

func TestDiscardedPartialIsNotTaken(t *testing.T) {
	asr := newBlockingTranscriber()
	recorder := newPartialRecorder()
	s, ticker := newTestStreamer(t, asr, recorder.record)

	s.Start()
	ticker.tick(t)
	asr.awaitEntry(t)

	// Stop while the pass is in flight: its result is discarded by
	// generation, so it must not surface as a reusable partial either.
	text, _, ok := s.StopAndTakePartial()
	if ok {
		t.Errorf("returned %q from an in-flight pass, want nothing", text)
	}
	asr.release <- "completed after the stop"
}

func TestStreamerIgnoresTooLittleAudio(t *testing.T) {
	asr := newBlockingTranscriber()
	recorder := newPartialRecorder()
	ticker := newManualTicker()

	// Less audio than a word takes to say: whisper answers such buffers with
	// stock phrases from its training data ("Thank you.", "you"), which is
	// worse than showing nothing.
	short := minPartialSamples(testSampleRate) - 1
	s := NewStreamer(asr, func() ([]float32, bool) { return make([]float32, short), true },
		recorder.record, time.Millisecond, testSampleRate,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.newTicker = func() Ticker { return ticker }
	t.Cleanup(func() { s.Stop(); asr.drain(); s.Wait() })

	s.Start()
	ticker.tick(t)

	select {
	case <-asr.entered:
		t.Fatal("transcribed a buffer too short to hold speech")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestStreamerTranscribesOnceThereIsEnoughAudio(t *testing.T) {
	asr := newBlockingTranscriber()
	recorder := newPartialRecorder()
	ticker := newManualTicker()

	enough := minPartialSamples(testSampleRate)
	s := NewStreamer(asr, func() ([]float32, bool) { return make([]float32, enough), true },
		recorder.record, time.Millisecond, testSampleRate,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.newTicker = func() Ticker { return ticker }
	t.Cleanup(func() { s.Stop(); asr.drain(); s.Wait() })

	s.Start()
	ticker.tick(t)
	asr.awaitEntry(t)
	asr.release <- "real speech"
	recorder.awaitUpdate(t)

	if texts, _ := recorder.snapshot(); len(texts) != 1 {
		t.Errorf("published %v, want the transcription", texts)
	}
}
