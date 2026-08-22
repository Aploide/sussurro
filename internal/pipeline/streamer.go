package pipeline

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// SnapshotFunc returns a copy of the audio captured so far in the current
// recording. It returns false when no recording is in progress.
type SnapshotFunc func() ([]float32, bool)

// PartialFunc receives a partial transcription belonging to the generation it
// is tagged with. Callers must ignore generations they no longer care about;
// the streamer already drops results from stopped sessions.
type PartialFunc func(generation uint64, text string)

// Ticker abstracts time.Ticker so tests can drive the streamer deterministically.
type Ticker interface {
	// C returns the channel signalling that a partial pass is due.
	C() <-chan time.Time
	// Stop releases the ticker's resources.
	Stop()
}

// realTicker adapts time.Ticker to Ticker.
type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }

// NewTicker returns a Ticker backed by the standard library.
func NewTicker(interval time.Duration) Ticker {
	return realTicker{t: time.NewTicker(interval)}
}

// Streamer produces bounded partial transcriptions of an in-progress
// recording. It runs at most one inference at a time and coalesces every tick
// that arrives while an inference is running, so slow CPU inference degrades
// the partial update rate instead of growing an unbounded work queue.
//
// Behavioral reference: flt-james/master internal/asr/stream.go (7c9c12e),
// which transcribes on the ticker goroutine and has no generation tagging, so
// a slow pass delays Stop and can publish text after the session ends.
type Streamer struct {
	transcribe transcriber
	snapshot   SnapshotFunc
	onPartial  PartialFunc
	newTicker  func() Ticker
	sampleRate int
	log        *slog.Logger

	// generation identifies the active recording session. Bumping it
	// invalidates every in-flight and queued partial result.
	generation atomic.Uint64

	mu      sync.Mutex
	running bool
	// lastText is the most recent published partial, and lastSamples the
	// number of audio samples it was computed from. Together they let the
	// final pass be skipped when a partial already covered the whole
	// recording.
	lastText    string
	lastSamples int
	lastGen     uint64
	// wake signals the worker that a pass is due. Capacity 1 is what
	// coalesces ticks: a pending wake absorbs any number of further ticks.
	wake chan struct{}
	stop chan struct{}
	wg   sync.WaitGroup
}

// NewStreamer builds a partial transcription worker. The transcriber is called
// only from the worker goroutine, so a single-threaded ASR engine is safe.
func NewStreamer(
	transcribe transcriber,
	snapshot SnapshotFunc,
	onPartial PartialFunc,
	interval time.Duration,
	sampleRate int,
	log *slog.Logger,
) *Streamer {
	if sampleRate <= 0 {
		sampleRate = 16000
	}
	return &Streamer{
		transcribe: transcribe,
		snapshot:   snapshot,
		onPartial:  onPartial,
		newTicker:  func() Ticker { return NewTicker(interval) },
		sampleRate: sampleRate,
		log:        log,
	}
}

// minPartialSamples is the least audio worth transcribing. Below this whisper
// tends to invent stock phrases rather than report silence.
func minPartialSamples(sampleRate int) int {
	const minAudio = 900 * time.Millisecond
	return int(minAudio.Seconds() * float64(sampleRate))
}

// Generation returns the currently active session generation.
func (s *Streamer) Generation() uint64 { return s.generation.Load() }

// Start begins a new partial transcription session and returns its generation.
// Results from earlier sessions are discarded from this point on. Starting an
// already-running streamer is a no-op that returns the current generation.
func (s *Streamer) Start() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return s.generation.Load()
	}

	generation := s.generation.Add(1)
	s.running = true
	s.lastText = ""
	s.lastSamples = 0
	s.wake = make(chan struct{}, 1)
	s.stop = make(chan struct{})

	s.wg.Add(2)
	go s.tickLoop(s.stop, s.wake)
	go s.worker(generation, s.stop, s.wake)

	return generation
}

// Stop ends the current session. It returns immediately without waiting for an
// in-flight inference, so final transcription is never delayed by a partial
// pass; the abandoned result is discarded by its generation check.
func (s *Streamer) Stop() {
	s.stopAndTake()
}

// stopAndTake ends the session and returns the last partial it had produced,
// with the number of samples that partial covered. The result is captured
// before the generation is bumped, which is the only moment it is still
// valid: bumping invalidates every in-flight and completed partial.
func (s *Streamer) stopAndTake() (text string, samples int, ok bool) {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return "", 0, false
	}
	s.running = false

	if s.lastText != "" && s.lastGen == s.generation.Load() {
		text, samples, ok = s.lastText, s.lastSamples, true
	}

	// Bumping the generation invalidates any result still being computed.
	s.generation.Add(1)
	close(s.stop)
	s.mu.Unlock()
	return text, samples, ok
}

// StopAndTakePartial ends the session and hands back the last partial it
// published, with the number of samples that partial covered.
//
// Audio keeps arriving while a pass runs, so this has essentially never seen
// the whole buffer — measured at 59200 samples against 80400. Callers must
// treat it as provisional text to display, not as a finished transcription,
// unless the tail it missed is too short to hold speech.
func (s *Streamer) StopAndTakePartial() (text string, samples int, ok bool) {
	return s.stopAndTake()
}

// Wait blocks until the goroutines of every stopped session have exited. It is
// intended for tests and shutdown, not for the recording hot path.
func (s *Streamer) Wait() { s.wg.Wait() }

// tickLoop converts ticks into at most one pending wake-up.
func (s *Streamer) tickLoop(stop <-chan struct{}, wake chan<- struct{}) {
	defer s.wg.Done()

	ticker := s.newTicker()
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C():
			// A full channel already holds a pending pass, so dropping this
			// tick is exactly the intended coalescing.
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}
}

// worker runs partial inferences one at a time for a single generation.
func (s *Streamer) worker(generation uint64, stop <-chan struct{}, wake <-chan struct{}) {
	defer s.wg.Done()

	for {
		select {
		case <-stop:
			return
		case <-wake:
			// Re-check: the session may have ended while this wake was queued.
			select {
			case <-stop:
				return
			default:
			}
			s.runPass(generation)
		}
	}
}

// runPass performs one partial transcription and publishes it if the session
// is still current.
func (s *Streamer) runPass(generation uint64) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("Recovered from panic in partial transcription", "error", r)
		}
	}()

	samples, ok := s.snapshot()
	if !ok || len(samples) == 0 {
		return
	}

	// Whisper hallucinates on near-silence, emitting stock phrases like
	// "Thank you." or "you" from its training data. Showing those before the
	// user has said anything is worse than showing nothing, so wait until
	// there is enough audio to hold speech.
	if len(samples) < minPartialSamples(s.sampleRate) {
		return
	}

	// Each partial re-transcribes the whole buffer from the start, so this
	// duration grows with the dictation while the interval between passes
	// stays fixed. Logged at info because it is the only visible measure of
	// that cost: asr_duration reports the final pass alone (sussurro-xvj.60).
	passStart := time.Now()
	text, err := s.transcribe.Transcribe(samples)
	passDuration := time.Since(passStart)
	if err != nil {
		s.log.Debug("Partial transcription failed", "error", err)
		return
	}
	s.log.Info("Partial pass",
		"duration", passDuration.Round(time.Millisecond),
		"audio", (time.Duration(len(samples)) * time.Second /
			time.Duration(s.sampleRate)).Round(time.Millisecond),
		"samples", len(samples))
	// Strip before publishing: a partial that is nothing but a marker has no
	// text in it, and showing one in the overlay is the reported defect.
	text = StripNonSpeechMarkers(text)
	if text == "" {
		return
	}

	// Inference is slow, so the session may have ended meanwhile. Publishing
	// now would resurrect text the user already cancelled.
	if s.generation.Load() != generation {
		s.log.Debug("Discarding stale partial transcription", "generation", generation)
		return
	}
	s.mu.Lock()
	s.lastText = text
	s.lastSamples = len(samples)
	s.lastGen = generation
	s.mu.Unlock()

	if s.onPartial != nil {
		s.onPartial(generation, text)
	}
}
