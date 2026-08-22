package pipeline

import (
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aploide/sussurro/internal/asr"
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
	// settledText is the transcript of audio behind the window, and
	// settledUntil the point in the recording it covers. Settled text is never
	// transcribed again: it conditions the decoder as a prompt, so it costs no
	// inference and cannot change under the user.
	settledText  string
	settledUntil time.Duration
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

// partialWindow is how much recent audio each partial pass transcribes.
//
// Handing whisper the whole recording made each pass cost roughly 25-30ms per
// second of accumulated audio, so a fixed tick interval produced work quadratic
// in dictation length: 0.365s per pass early in a dictation against 1.270s
// late, 3.5x, and 45.8s of inference for 54.9s of speech (sussurro-xvj.60).
// Bounding the audio makes the per-pass cost constant instead.
//
// Fifteen seconds holds several sentences, so the decoder has substantial
// acoustic context rather than starting cold at a splice point, which is what
// made the tail-splicing in sussurro-xvj.22 invent words at the boundary. Text
// settled before the window is carried as a decoder prompt.
const partialWindow = 15 * time.Second

// settleMargin is how far behind the window's start a segment must end before
// its text is settled.
//
// Whisper's segment timestamps are approximate, and a segment ending near the
// boundary may be reported slightly differently on the next pass. Settling only
// what is clear of the boundary by this margin keeps a word from being frozen
// on one pass and re-recognised on the next.
const settleMargin = 2 * time.Second

// samplesDuration converts a sample count to the time it represents.
func samplesDuration(samples, sampleRate int) time.Duration {
	if sampleRate <= 0 {
		return 0
	}
	return time.Duration(samples) * time.Second / time.Duration(sampleRate)
}

// durationSamples converts a duration to a sample count.
func durationSamples(d time.Duration, sampleRate int) int {
	if sampleRate <= 0 || d <= 0 {
		return 0
	}
	return int(d.Seconds() * float64(sampleRate))
}

// windowStartFor returns where a partial pass should begin transcribing.
//
// The window begins exactly where the settled text ends, and is never moved
// ahead of it: audio between the two would be transcribed by nobody, which is
// how an earlier offset-only attempt silently dropped speech from long
// dictations. The window therefore grows beyond partialWindow whenever
// settling lags, and that pass simply costs more; the cost is bounded again as
// soon as the next segment settles.
func windowStartFor(settledUntil time.Duration) time.Duration {
	if settledUntil < 0 {
		return 0
	}
	return settledUntil
}

// settleSegments picks the segments whose audio lies behind the window and can
// therefore be frozen.
//
// windowStart is where this pass's audio began in the recording, so segment
// timestamps (which are relative to that audio) are shifted by it. A segment
// settles when it ends before cutoff less a margin, which keeps a word sitting
// on the boundary from being frozen on one pass and re-recognised on the next.
//
// Returns the settled text, the point up to which it now covers, and whether
// anything settled at all.
func settleSegments(
	segments []asr.Segment,
	windowStart, cutoff time.Duration,
	sampleRate int,
) (string, time.Duration, bool) {
	if cutoff <= 0 || len(segments) == 0 {
		return "", 0, false
	}

	limit := cutoff - settleMargin
	if limit <= 0 {
		return "", 0, false
	}

	var settled []asr.Segment
	until := windowStart
	for _, seg := range segments {
		end := windowStart + seg.End
		if end > limit {
			break // Segments are in order, so nothing later settles either.
		}
		settled = append(settled, seg)
		until = end
	}

	if len(settled) == 0 {
		return "", 0, false
	}
	return asr.JoinSegments(settled), until, true
}

// joinTranscript appends text to a settled prefix, keeping a single space
// between them and tolerating either being empty.
func joinTranscript(prefix, text string) string {
	prefix = strings.TrimSpace(prefix)
	text = strings.TrimSpace(text)
	switch {
	case prefix != "" && text != "":
		return prefix + " " + text
	case prefix != "":
		return prefix
	default:
		return text
	}
}

// recognise transcribes the window, using timestamps and decoder context when
// the engine supports them and falling back to a plain transcription when it
// does not.
func (s *Streamer) recognise(audio []float32, preceding string) ([]asr.Segment, error) {
	if seg, ok := s.transcribe.(segmentingTranscriber); ok {
		return seg.SegmentsWithContext(audio, preceding)
	}

	text, err := s.transcribe.Transcribe(audio)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	// Without timestamps nothing can settle, so the whole window is reported
	// as one span and the next pass simply re-transcribes it.
	return []asr.Segment{{Text: strings.TrimSpace(text)}}, nil
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

	s.mu.Lock()
	settledText, settledUntil := s.settledText, s.settledUntil
	s.mu.Unlock()

	// The window starts where the settled text ends, and never spans more than
	// partialWindow, so per-pass cost is bounded however long the dictation
	// runs. Everything before it is carried as a decoder prompt rather than
	// transcribed again.
	total := samplesDuration(len(samples), s.sampleRate)
	windowStart := windowStartFor(settledUntil)
	offset := durationSamples(windowStart, s.sampleRate)
	if offset < 0 || offset > len(samples) {
		offset = 0
		windowStart = 0
	}
	audio := samples[offset:]
	if len(audio) < minPartialSamples(s.sampleRate) {
		return
	}

	passStart := time.Now()
	segments, err := s.recognise(audio, settledText)
	passDuration := time.Since(passStart)
	if err != nil {
		s.log.Debug("Partial transcription failed", "error", err)
		return
	}

	windowText := StripNonSpeechMarkers(asr.JoinSegments(segments))
	full := joinTranscript(settledText, windowText)

	s.log.Info("Partial pass",
		"duration", passDuration.Round(time.Millisecond),
		"window", samplesDuration(len(audio), s.sampleRate).Round(time.Millisecond),
		"total", total.Round(time.Millisecond),
		"settled", settledUntil.Round(time.Millisecond))

	if full == "" {
		return
	}

	// Inference is slow, so the session may have ended meanwhile. Publishing
	// now would resurrect text the user already cancelled.
	if s.generation.Load() != generation {
		s.log.Debug("Discarding stale partial transcription", "generation", generation)
		return
	}

	s.mu.Lock()
	s.lastText = full
	s.lastSamples = len(samples)
	s.lastGen = generation
	// Settle the segments whose audio is clear of the window's start. Their
	// timestamps are relative to the window, so they are offset back onto the
	// recording's own timeline before comparing.
	if newText, newUntil, ok := settleSegments(
		segments, windowStart, total-partialWindow, s.sampleRate,
	); ok {
		s.settledText = joinTranscript(settledText, newText)
		s.settledUntil = newUntil
	}
	s.mu.Unlock()

	if s.onPartial != nil {
		s.onPartial(generation, full)
	}
}
