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

	// revisionSentences is how many sentences stay revisable. Read without a
	// lock: it is set before Start and only ever read by the worker goroutine.
	revisionSentences int

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
	// settledText is the transcript of audio that has fallen out of the
	// revision window, and settledUntil the point in the recording it covers.
	// Settled text is never decoded again: it conditions the decoder as a
	// prompt, so it costs no inference and cannot change under the user.
	settledText  string
	settledUntil time.Duration
	// lastSegments are the previous pass's segments and lastStart the point
	// that pass began decoding at. Their timings are what the next window is
	// measured back through, since whisper reports no word timings of its own.
	lastSegments []asr.Segment
	lastStart    time.Duration
	// wake signals the worker that a pass is due. Capacity 1 is what
	// coalesces ticks: a pending wake absorbs any number of further ticks.
	wake chan struct{}
	stop chan struct{}
	wg   sync.WaitGroup

	// beforeCommit is a test barrier for the narrow stop-versus-publish race.
	// Production leaves it nil.
	beforeCommit func()
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
		transcribe:        transcribe,
		snapshot:          snapshot,
		onPartial:         onPartial,
		newTicker:         func() Ticker { return NewTicker(interval) },
		sampleRate:        sampleRate,
		log:               log,
		revisionSentences: defaultRevisionSentences,
	}
}

// defaultRevisionSentences is how many complete sentences stay revisable.
//
// Windowing begins only once this many sentences have been finished, so a
// dictation shorter than that is decoded whole on every pass: nothing settles,
// and no mistake can be frozen.
//
// Four is drawn from this repository's own dictation logs. The median sentence
// there runs twelve words, about five seconds, so four sentences is roughly
// twenty seconds of revisable audio — which is also the point up to which
// whole-recording passes still measure a flat 0.29-0.37s. Below that there is
// nothing to save by windowing; the growth windowing exists to bound only bites
// later, at 1.27s per pass for a fifty-five second dictation (sussurro-xvj.60).
// Three-sentence dictations are the commonest length in those logs and are left
// untouched entirely.
//
// Sizing by words instead put the boundary mid-phrase, and whisper handed audio
// that starts partway through a phrase produces a plausible continuation rather
// than the truth: "a short couple of years" for "a short couple of sentences",
// and a duplicated "Yep" at a window head that was not a natural start
// (sussurro-k6w).
const defaultRevisionSentences = 4

// maxWindowDuration caps the audio a single pass may decode.
//
// A sentence count does not bound cost on its own: two sentences of slow or
// rambling speech can span a great deal of audio, and cost tracks audio. Without
// this backstop a long unpunctuated stretch would reintroduce the
// sussurro-xvj.60 slowdown the window exists to prevent.
//
// It must comfortably exceed the time defaultRevisionSentences takes to speak,
// or the two settings contradict each other: the ceiling would forever be
// trying to shrink a window the sentence count is trying to hold open.
const maxWindowDuration = 60 * time.Second

// sentenceEnd reports whether a word ends a sentence, i.e. finishes with a
// full stop, question mark, or exclamation mark.
//
// Trailing quotes and brackets are stepped over so that a sentence ending
// inside quotation marks still counts.
func sentenceEnd(text string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(text), `"'”’)]`)
	if trimmed == "" {
		return false
	}
	switch trimmed[len(trimmed)-1] {
	case '.', '!', '?':
		return true
	}
	return false
}

// countWords reports how many whitespace-separated words a transcript holds.
func countWords(text string) int {
	return len(strings.Fields(text))
}

// flattenWords concatenates every segment's word timings in order.
//
// Sentences routinely straddle segment boundaries, so the run of words has to
// be seen as one sequence rather than per segment. Returns nil when the engine
// reported no word timings, which callers treat as "fall back to segments".
func flattenWords(segments []asr.Segment) []asr.Word {
	var words []asr.Word
	for _, seg := range segments {
		words = append(words, seg.Words...)
	}
	return words
}

// windowStartFor returns where a partial pass should begin decoding.
//
// The window reaches back over the last revisionSentences complete sentences,
// so recent speech keeps being reconsidered as later audio arrives and can
// still be corrected. It is clamped two ways: never before the settled point,
// whose text is gone from the audio being decoded, and never spanning more than
// maxWindowDuration, which bounds the cost of a pass.
//
// segments are those of the previous pass, with timings relative to
// windowStart, the point that pass began at.
func windowStartFor(
	settledUntil time.Duration,
	segments []asr.Segment,
	windowStart time.Duration,
	total time.Duration,
	revisionSentences int,
) time.Duration {
	if settledUntil < 0 {
		settledUntil = 0
	}
	if revisionSentences <= 0 {
		revisionSentences = defaultRevisionSentences
	}

	// Walk back to the start of the nth-from-last sentence.
	//
	// Cutting on a sentence boundary rather than a word count is what keeps
	// whisper from having to guess at the join. Handed audio that begins
	// mid-phrase it produces a plausible continuation rather than the truth —
	// "a short couple of years" for "a short couple of sentences", and a
	// duplicated "Yep" at a window head that was not a natural start — and
	// settling then made those permanent (sussurro-k6w).
	//
	// The boundary moves only when the count is actually reached, so a
	// dictation with fewer than revisionSentences sentences stays wholly
	// revisable. Assigning on every step instead put the boundary *after* the
	// oldest unit, which froze whisper's first guess at the opening of every
	// dictation at 420ms (sussurro-fkd).
	//
	// Falls back to segment granularity when an engine reports no word timings,
	// which decodes more audio than asked but never less.
	// Flattened across segments deliberately: a sentence frequently ends on the
	// last token of one segment and continues into the next, and walking the
	// segments separately cannot express "the word after that mark".
	start := settledUntil
	if words := flattenWords(segments); len(words) > 0 {
		ends := 0
		// A full stop on the very last word closes the sentence just spoken,
		// which must itself stay revisable; counting it would leave one fewer
		// complete sentence open than asked for.
		for j := len(words) - 2; j >= 0; j-- {
			if !sentenceEnd(words[j].Text) {
				continue
			}
			ends++
			if ends < revisionSentences {
				continue
			}
			// The full stop closes the sentence before it, so the window opens
			// at the following word.
			start = windowStart + words[j+1].Start
			break
		}
	} else {
		// No word timings: fall back to whole segments. The last segment is
		// skipped for the same reason as the last word above.
		ends := 0
		for i := len(segments) - 2; i >= 0; i-- {
			if !sentenceEnd(segments[i].Text) {
				continue
			}
			ends++
			if ends >= revisionSentences {
				start = windowStart + segments[i+1].Start
				break
			}
		}
	}

	// Never reach back behind the settled point: that audio already has frozen
	// text, so decoding it again would duplicate those words.
	if start < settledUntil {
		start = settledUntil
	}
	// Cap the span so a long pause cannot make a pass arbitrarily expensive.
	//
	// The cap may only pull the window forward as far as the settled point,
	// never past it. Only decoded audio can settle, so audio the ceiling skips
	// is transcribed by nobody and is lost outright: an earlier version
	// clamped unconditionally, reasoning that settling would catch up, and
	// whole sentences vanished from long dictations (sussurro-fkd). When
	// settling has fallen behind, the pass simply costs more — a slow pass is
	// a cost, dropped speech is a defect.
	if floor := total - maxWindowDuration; start < floor {
		// Never past the settled point: see above.
		if floor > settledUntil {
			floor = settledUntil
		}
		start = floor
	}
	if start < 0 {
		start = 0
	}
	return start
}

// lastSentenceStart returns the window-relative start of the nth-from-last
// sentence, or zero when the window holds fewer than n complete sentences and
// so is entirely within the revision budget.
//
// This is the settling side of the same boundary windowStartFor computes: text
// may only be frozen once it lies behind the sentence the next pass will begin
// at. Deriving both from sentence ends is what keeps settling from running
// ahead of the window and freezing text still being decoded.
func lastSentenceStart(segments []asr.Segment, n int) time.Duration {
	if n <= 0 {
		return 0
	}

	// The last word is skipped: a full stop on it closes the sentence just
	// spoken, which must itself stay revisable. Counting it would leave one
	// fewer complete sentence open than asked for, and settling would run
	// ahead of the window.
	if words := flattenWords(segments); len(words) > 0 {
		ends := 0
		for j := len(words) - 2; j >= 0; j-- {
			if !sentenceEnd(words[j].Text) {
				continue
			}
			ends++
			if ends >= n {
				return words[j+1].Start
			}
		}
		return 0
	}

	ends := 0
	for i := len(segments) - 2; i >= 0; i-- {
		if !sentenceEnd(segments[i].Text) {
			continue
		}
		ends++
		if ends >= n {
			return segments[i+1].Start
		}
	}
	return 0
}

// settleSegments freezes the segments that have fallen out of the revision
// window and can no longer change.
//
// windowStart is where this pass's audio began, so segment timings (relative to
// that audio) are shifted onto the recording's own timeline. A segment settles
// once it ends before nextStart less a margin: it is outside the audio the next
// pass will decode, so no later pass can revise it anyway.
//
// Settling is deliberately driven by the window rather than by elapsed time.
// Tying the two together is what guarantees no audio is orphaned: text is
// frozen exactly when its audio stops being decoded, never before.
//
// Returns the settled text, the point it now covers up to, and whether
// anything settled.
func settleSegments(
	segments []asr.Segment,
	windowStart, nextStart time.Duration,
	revisionSentences int,
) (string, time.Duration, bool) {
	if len(segments) == 0 {
		return "", 0, false
	}

	// Nothing can settle until the window has actually moved: if the next pass
	// decodes the same audio, none of this text has left it.
	//
	// Checked rather than assumed, because the caller's timings come from
	// whisper and cannot be trusted to advance. With token timestamps disabled
	// every token reports -10ms, which drove nextStart to zero; every word then
	// tested as settled, so each pass froze the whole transcript and appended
	// it to the previous one, filling the overlay with the same sentence over
	// and over (sussurro-fkd). Token timestamps are now enabled, and this
	// guard keeps the failure contained if they are ever unavailable again.
	if nextStart <= windowStart {
		return "", 0, false
	}

	// Text settles exactly when it ends at or before the next window's start:
	// it is then outside the audio the next pass decodes, so no later pass
	// could revise it anyway.
	//
	// The comparison is deliberately exact. Subtracting a safety margin holds
	// back the very text that just left the window, which froze nothing at
	// all; adding one settles text still inside the window, the freezing-too-
	// early defect this exists to avoid.
	limit := nextStart

	// Keep the last revisionSentences words of the window revisable, whatever the
	// timings say.
	//
	// nextStart alone is not enough: the walk that produces it only sees words
	// still in the window, and the window already excludes settled text. Once
	// settling jumped ahead, the next window held fewer words than the budget,
	// so nothing pulled it back and it stayed starved. Whisper was then decoding
	// under two seconds of audio behind a confident prompt, which is how a
	// correctly heard "Surely" came back as "Really" and was frozen
	// (sussurro-fkd).
	if keep := lastSentenceStart(segments, revisionSentences); keep > 0 {
		if abs := windowStart + keep; abs < limit {
			limit = abs
		}
	}

	// Settling is word-granular for the same reason the window is: whisper
	// segments run tens of seconds, so a segment straddling the boundary could
	// never partly settle and the settled point would stall (sussurro-fkd).
	var parts []string
	until := windowStart
	for _, seg := range segments {
		if len(seg.Words) == 0 {
			if end := windowStart + seg.End; end <= limit {
				if len(parts) > 0 {
					parts = append(parts, " ")
				}
				parts = append(parts, seg.Text)
				until = end
				continue
			}
			break
		}
		done := false
		for _, w := range seg.Words {
			end := windowStart + w.End
			if end > limit {
				done = true
				break
			}
			parts = append(parts, w.Text)
			until = end
		}
		if done {
			break // In time order, so nothing later settles either.
		}
	}

	if len(parts) == 0 {
		return "", 0, false
	}
	// Joined without a separator: token text carries its own leading spaces,
	// whereas whole-segment text does not, so segments are spaced as they are
	// appended instead.
	return strings.TrimSpace(strings.Join(parts, "")), until, true
}

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

// SetRevisionSentences sets how many sentences stay revisable. A non-positive
// count leaves the default in place. Must be called before Start().
func (s *Streamer) SetRevisionSentences(n int) {
	if n <= 0 {
		s.log.Warn("Non-positive revision_window_sentences, using default",
			"value", n, "default", defaultRevisionSentences)
		return
	}
	s.revisionSentences = n
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
	// Settled state belongs to one recording. Leaving it set would prepend the
	// previous dictation's text to this one (sussurro-fkd).
	s.settledText = ""
	s.settledUntil = 0
	s.lastSegments = nil
	s.lastStart = 0
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
	prevSegments, prevStart := s.lastSegments, s.lastStart
	s.mu.Unlock()

	// The window reaches back over the last revisionSentences words, so recent
	// speech keeps being reconsidered and can still be corrected by later
	// audio. Text behind it is settled and rides along as a decoder prompt
	// instead of being transcribed again, which is what keeps per-pass cost
	// flat however long the dictation runs.
	total := samplesDuration(len(samples), s.sampleRate)
	windowStart := windowStartFor(
		settledUntil, prevSegments, prevStart, total, s.revisionSentences,
	)
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

	// Logged before joining and marker-stripping so a defect in the text can be
	// attributed to whisper or to our assembly of it. The joined form alone
	// cannot distinguish the two (sussurro-sqn).
	if s.log.Enabled(nil, slog.LevelDebug) {
		for i, seg := range segments {
			tokens := make([]string, len(seg.Words))
			for j, w := range seg.Words {
				tokens[j] = w.Text
			}
			s.log.Debug("Raw segment",
				"i", i,
				"start", seg.Start.Round(time.Millisecond),
				"end", seg.End.Round(time.Millisecond),
				"text", seg.Text,
				"tokens", strings.Join(tokens, "|"))
		}
	}

	s.log.Debug("Partial pass",
		"duration", passDuration.Round(time.Millisecond),
		"window", samplesDuration(len(audio), s.sampleRate).Round(time.Millisecond),
		"total", total.Round(time.Millisecond),
		"settled", settledUntil.Round(time.Millisecond),
		"settled_text", settledText,
		"window_text", windowText,
		"overlay_text", full)

	if full == "" {
		return
	}

	// Inference is slow, so the session may have ended meanwhile. Publishing
	// now would resurrect text the user already cancelled.
	if s.generation.Load() != generation {
		s.log.Debug("Discarding stale partial transcription", "generation", generation)
		return
	}

	// Where the next pass will begin decoding. Settling is measured against
	// this rather than against elapsed time, which is what guarantees no audio
	// is orphaned: a segment is frozen exactly when it stops being decoded.
	nextStart := windowStartFor(
		settledUntil, segments, windowStart, total, s.revisionSentences,
	)

	if s.beforeCommit != nil {
		s.beforeCommit()
	}

	// Commit and generation validation must be atomic with stopAndTake. A
	// check before taking the lock left a gap where Stop could capture an older
	// partial and bump the generation, after which this pass still overwrote
	// state and published newer text to the overlay (sussurro-3jn).
	s.mu.Lock()
	if !s.running || s.generation.Load() != generation {
		s.mu.Unlock()
		s.log.Debug("Discarding partial stopped before commit", "generation", generation)
		return
	}
	// Keep the commit and its publication in one critical section. Otherwise
	// StopAndTake can return the committed text and invalidate the generation,
	// only for this callback to restore a Recording overlay afterwards.
	// The deferred unlock also releases the mutex if a callback panics.
	defer s.mu.Unlock()
	s.lastText = full
	s.lastSamples = len(samples)
	s.lastGen = generation
	s.lastSegments = segments
	s.lastStart = windowStart
	if newText, newUntil, ok := settleSegments(segments, windowStart, nextStart, s.revisionSentences); ok {
		s.settledText = joinTranscript(settledText, newText)
		s.settledUntil = newUntil
		s.log.Debug("Settled",
			"newly_settled", newText,
			"until", newUntil.Round(time.Millisecond),
			"next_window_start", nextStart.Round(time.Millisecond),
			"settled_total", s.settledText)
	}

	if s.onPartial != nil {
		s.onPartial(generation, full)
	}
}
