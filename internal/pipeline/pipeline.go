package pipeline

import (
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/aploide/sussurro/internal/asr"
	"github.com/aploide/sussurro/internal/audio"
	ctxProvider "github.com/aploide/sussurro/internal/context"
	"github.com/aploide/sussurro/internal/llm"
	"github.com/aploide/sussurro/internal/session"
)

// audioBufferCapFor returns a sensible pre-allocation capacity (in samples)
// for the audio buffer based on the configured max duration.
func audioBufferCapFor(maxDuration string, sampleRate int) int {
	const unlimitedInitialSeconds = 30
	resolved, _ := ResolveMaxDuration(maxDuration)
	if resolved == "infinite" {
		return unlimitedInitialSeconds * sampleRate
	}
	d, _ := time.ParseDuration(resolved)
	return int(d.Seconds() * float64(sampleRate))
}

// StateNotifier receives pipeline state transitions and audio RMS values.
// Implementations must be non-blocking (use channels / async dispatch internally).
type StateNotifier interface {
	OnStateChange(state session.State)
	OnRMSData(rms float32)
}

// Pipeline orchestrates the flow of data from audio capture to text output
type Pipeline struct {
	audioEngine *audio.CaptureEngine
	asrEngine   transcriber
	llmEngine   cleaner
	ctxProvider ctxProvider.Provider
	log         *slog.Logger
	vadParams   audio.VADParams

	onCompletion   func()         // Callback for when processing finishes
	uiNotifier     StateNotifier  // optional; nil means no UI
	resultConsumer ResultConsumer // optional; nil discards results
	streamer       *Streamer      // optional; nil disables partial transcription
	// finalText holds the transcription just produced, so completion can
	// display it rather than only reporting that the session ended.
	finalText string

	// Channels for data flow
	audioChan chan []float32
	stopChan  chan struct{}
	wg        sync.WaitGroup

	// State
	//
	// minDuration is the shortest recording sent to recognition, guarding
	// against Whisper inventing stock phrases from near-silence.
	minDuration atomic.Int64

	// isRecording is atomic because the realtime audio callback reads it on
	// every chunk. Reading it under mu made the audio thread wait on whatever
	// else held the lock, and a blocked realtime callback overruns the device
	// ring buffer, losing speech. Writes still happen under mu, which
	// continues to order it against isTranscribing and audioBuffer.
	isRecording     atomic.Bool
	isTranscribing  bool // true while processSegment is running; blocks new recordings
	stopped         bool // guarded by mu; prevents sessions starting after Stop begins
	lowercaseOutput bool
	skipLLMCleanup  bool
	audioBuffer     []float32
	audioBufferCap  int        // pre-computed capacity to avoid repeated slice growth
	mu              sync.Mutex // Protects stopped, isTranscribing, output options, audioBuffer, and recordingStart
	maxDuration     string

	// afterCurrent runs settings updates after the active recording and cleanup
	// have both finished, but before a new recording can start. Guarded by mu.
	afterCurrent []func()

	// recordingStart is when the current recording began, so a stop can
	// report how long it ran. Without it a premature stop is
	// indistinguishable in the log from a normal one.
	recordingStart time.Time

	// maxSamples is the recording cap in samples, published so the UI can
	// show how close the buffer is to it. Written once at capture start,
	// before any reader exists, then only read.
	maxSamples atomic.Int64
	// droppedFrames counts audio frames discarded because the consumer could
	// not keep up. Written from the realtime audio thread, so it is atomic
	// and never guarded by mu.
	droppedFrames atomic.Uint64
	// lastDropWarn throttles the drop warning: the audio thread can drop
	// hundreds of frames a second, and one line per frame would bury the log.
	lastDropWarn atomic.Int64
}

// StopReason names what ended a recording, so the log can distinguish a user
// release from an internal stop. A recording that ends early otherwise leaves
// no trace of which path ended it.
type StopReason string

const (
	// StopReleased is the ordinary end: the user let go, or toggled off.
	StopReleased StopReason = "released"
	// StopMaxDuration is the max_duration safety cap firing mid-utterance.
	StopMaxDuration StopReason = "max-duration"
)

// NewPipeline creates a new processing pipeline
func NewPipeline(
	audioEngine *audio.CaptureEngine,
	asrEngine *asr.Engine,
	llmEngine *llm.Engine,
	ctxProvider ctxProvider.Provider,
	log *slog.Logger,
	sampleRate int,
	maxDuration string,
) *Pipeline {
	vadParams := audio.DefaultVADParams()
	vadParams.SampleRate = sampleRate // Override with actual sample rate

	return &Pipeline{
		audioEngine:    audioEngine,
		asrEngine:      asrEngine,
		llmEngine:      llmEngine,
		ctxProvider:    ctxProvider,
		log:            log,
		vadParams:      vadParams,
		audioBufferCap: audioBufferCapFor(maxDuration, sampleRate),
		audioChan:      make(chan []float32, 100), // Buffer audio chunks
		stopChan:       make(chan struct{}),
		maxDuration:    maxDuration,
	}
}

// defaultMinDuration is the fallback shortest recording sent to recognition.
//
// It has to clear an accidental keypress without discarding a real one-word
// dictation. The previous hardcoded 2s silently dropped ordinary short
// phrases, which is sussurro-xvj.52.
const defaultMinDuration = 300 * time.Millisecond

// SetMinDuration sets the shortest recording sent to recognition. An unparseable
// or non-positive value leaves the default in place. Safe to call from any
// goroutine; must be called before Start() to affect the first recording.
func (p *Pipeline) SetMinDuration(d string) {
	if d == "" {
		return
	}
	parsed, err := time.ParseDuration(d)
	if err != nil {
		p.log.Warn("Invalid min_duration, using default", "value", d, "default", defaultMinDuration, "error", err)
		return
	}
	if parsed <= 0 {
		p.log.Warn("Non-positive min_duration, using default", "value", d, "default", defaultMinDuration)
		return
	}
	p.minDuration.Store(int64(parsed))
}

// minRecordingDuration returns the configured minimum, or the default.
func (p *Pipeline) minRecordingDuration() time.Duration {
	if d := p.minDuration.Load(); d > 0 {
		return time.Duration(d)
	}
	return defaultMinDuration
}

// SetLowercaseOutput controls whether all transcribed text is forced to lowercase.
// Safe to call from any goroutine.
func (p *Pipeline) SetLowercaseOutput(v bool) {
	p.mu.Lock()
	p.lowercaseOutput = v
	p.mu.Unlock()
}

// SetSkipLLMCleanup controls whether the LLM cleanup step is bypassed entirely.
// Safe to call from any goroutine.
func (p *Pipeline) SetSkipLLMCleanup(v bool) {
	p.mu.Lock()
	p.skipLLMCleanup = v
	p.mu.Unlock()
}

// RunWhenIdle applies callback immediately when no dictation is active, or
// queues it after the current recording and cleanup. The callback runs while
// the pipeline state lock excludes a new recording and must not call Pipeline
// methods itself.
func (p *Pipeline) RunWhenIdle(callback func()) {
	if callback == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.isRecording.Load() || p.isTranscribing {
		p.afterCurrent = append(p.afterCurrent, callback)
		return
	}
	callback()
}

// runAfterCurrentLocked drains deferred settings changes before the current
// dictation releases its isTranscribing gate. A bad callback is isolated so it
// cannot leave the pipeline permanently busy.
func (p *Pipeline) runAfterCurrentLocked() {
	callbacks := p.afterCurrent
	p.afterCurrent = nil
	for _, callback := range callbacks {
		func() {
			defer func() {
				if r := recover(); r != nil && p.log != nil {
					p.log.Error("Recovered from deferred settings update", "error", r)
				}
			}()
			callback()
		}()
	}
}

// SetOnCompletion sets a callback to be called when processing is done
func (p *Pipeline) SetOnCompletion(callback func()) {
	p.onCompletion = callback
}

// SetResultConsumer installs the consumer that receives completed recognition
// results. Must be called before Start(). A nil consumer discards results.
func (p *Pipeline) SetResultConsumer(consumer ResultConsumer) {
	p.resultConsumer = consumer
}

// SetStreamer installs a partial transcription worker, enabling streaming.
// Must be called before Start(). A nil streamer leaves streaming disabled,
// which is the default.
func (p *Pipeline) SetStreamer(streamer *Streamer) {
	p.streamer = streamer
}

// SnapshotRecording returns a copy of the audio captured so far, and whether a
// recording is currently in progress. The copy lets partial transcription run
// without holding the pipeline lock across a slow inference.
func (p *Pipeline) SnapshotRecording() ([]float32, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.isRecording.Load() {
		return nil, false
	}
	snapshot := make([]float32, len(p.audioBuffer))
	copy(snapshot, p.audioBuffer)
	return snapshot, true
}

// SetUINotifier installs a StateNotifier for UI state updates.
// Must be called before Start().
func (p *Pipeline) SetUINotifier(n StateNotifier) {
	p.uiNotifier = n
	// Forward RMS data from the audio engine to the notifier
	if n != nil {
		// Runs on the realtime audio thread: no lock, no allocation.
		p.audioEngine.SetRMSCallback(func(rms float32) {
			if p.isRecording.Load() {
				n.OnRMSData(rms)
			}
		})
	}
}

// notifyState sends a state change to the UI notifier (nil-safe).
func (p *Pipeline) notifyState(state session.State) {
	if p.uiNotifier != nil {
		p.uiNotifier.OnStateChange(state)
	}
}

// dropWarnInterval bounds how often dropped frames are reported.
const dropWarnInterval = time.Second

// slowStepThreshold is roughly one audio chunk period. Anything in the
// capture path that takes longer than this is stalling the consumer, which
// leads to dropped frames.
const slowStepThreshold = 50 * time.Millisecond

// warnIfSlow reports a step in the capture path that took long enough to
// risk dropping audio. Rate-limited by the same window as drop warnings.
func (p *Pipeline) warnIfSlow(step string, start time.Time) {
	elapsed := time.Since(start)
	if elapsed < slowStepThreshold {
		return
	}

	now := time.Now().UnixNano()
	last := p.lastDropWarn.Load()
	if now-last < int64(dropWarnInterval) {
		return
	}
	if !p.lastDropWarn.CompareAndSwap(last, now) {
		return
	}
	p.log.Warn("Audio capture path stalled; frames may be dropped",
		"step", step, "elapsed", elapsed.Round(time.Millisecond))
}

// onFrameDropped records a discarded audio frame. It runs on the realtime
// audio thread, so it does no allocation, takes no lock, and rate-limits
// itself rather than logging every frame.
func (p *Pipeline) onFrameDropped() {
	total := p.droppedFrames.Add(1)

	now := time.Now().UnixNano()
	last := p.lastDropWarn.Load()
	if now-last < int64(dropWarnInterval) {
		return
	}
	if !p.lastDropWarn.CompareAndSwap(last, now) {
		// Another callback is already reporting this window.
		return
	}
	p.log.Warn("Dropping captured audio; the consumer is not keeping up",
		"dropped_frames_total", total)
}

// Start begins the pipeline processing
func (p *Pipeline) Start() error {
	p.log.Debug("Starting pipeline")
	p.audioEngine.SetDropCallback(p.onFrameDropped)

	// Start Audio Capture Loop (runs continuously to keep device ready)
	p.wg.Add(1)
	go p.captureLoop()

	return nil
}

// stopStreaming ends the current partial transcription session, if any.
// Safe to call when streaming is disabled or already stopped.
func (p *Pipeline) stopStreaming() {
	if p.streamer != nil {
		p.streamer.Stop()
	}
}

// Stop gracefully shuts down the pipeline
func (p *Pipeline) Stop() {
	p.log.Debug("Stopping pipeline")
	// StopRecording and the duration-cap path register their final worker while
	// holding this lock. Taking it before closing the capture loop guarantees
	// Wait cannot observe zero before that registration (sussurro-3jn).
	p.mu.Lock()
	p.stopped = true
	p.isRecording.Store(false)
	p.stopStreaming()
	close(p.stopChan)
	p.mu.Unlock()
	p.wg.Wait()
	if p.streamer != nil {
		p.streamer.Wait()
	}
	p.log.Debug("Pipeline stopped")
}

// StartRecording begins accumulating audio data
func (p *Pipeline) StartRecording() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stopped || p.isRecording.Load() || p.isTranscribing {
		return
	}

	// Drain channel to ensure no stale audio is included
	for len(p.audioChan) > 0 {
		<-p.audioChan
	}

	p.isRecording.Store(true)
	p.recordingStart = time.Now()
	// Per-recording, so the count logged at stop describes this utterance.
	p.droppedFrames.Store(0)
	// Reuse the backing array from the previous recording to avoid re-allocating
	// every time. On the very first recording we make an upfront allocation sized
	// to the configured max duration so appends never need to grow the slice.
	if cap(p.audioBuffer) > 0 {
		p.audioBuffer = p.audioBuffer[:0]
	} else {
		p.audioBuffer = make([]float32, 0, p.audioBufferCap)
	}
	p.log.Debug("Recording started")
	p.notifyState(session.StateRecording)

	if p.streamer != nil {
		p.streamer.Start()
	}
}

// StopRecording stops accumulating and triggers processing
// Returns true if recording was stopped and processing started, false if not recording
func (p *Pipeline) StopRecording() bool {
	return p.stopRecordingBecause(StopReleased)
}

// stopRecordingBecause ends the recording, recording why. The reason is
// logged at info level with the elapsed time: a recording that ends before
// the user released the key is otherwise indistinguishable from one that did
// not, which made sussurro-xvj.36 impossible to diagnose from a log.
func (p *Pipeline) stopRecordingBecause(reason StopReason) bool {
	p.mu.Lock()
	if !p.isRecording.Load() {
		p.mu.Unlock()
		return false
	}

	p.isRecording.Store(false)
	p.isTranscribing = true

	elapsed := time.Since(p.recordingStart)
	p.log.Info("Recording stopped",
		"reason", reason,
		"elapsed", elapsed.Round(time.Millisecond),
		"samples", len(p.audioBuffer),
		"dropped_frames", p.droppedFrames.Load())

	bufferCopy := make([]float32, len(p.audioBuffer))
	copy(bufferCopy, p.audioBuffer)
	// Take the partial and register its replacement before unlocking. Stop
	// takes the same lock before Wait, so it cannot discard the partial or
	// close engines between this point and final worker registration.
	partial, partialSamples, hasPartial := p.takeStreamingPartial()
	p.wg.Add(1)
	p.mu.Unlock()

	p.finalizeRecording(bufferCopy, partial, partialSamples, hasPartial)
	return true
}

// finalizeRecording dispatches the least work that can still produce a
// complete transcript. Both user release and the maximum-duration stop pass
// through here, so neither can discard the complete partial while
// independently decoding the final buffer. Its worker is registered by the
// caller while holding p.mu, before shutdown can begin waiting.
func (p *Pipeline) finalizeRecording(bufferCopy []float32, partial string, partialSamples int, hasPartial bool) {

	// Audio keeps arriving while a partial is running, so a completed partial
	// has essentially never seen the whole buffer — measured 59200 samples
	// against 80400, a 1.3s gap. Requiring full coverage therefore never
	// fired. Reuse only when the tail it missed is too short to contain
	// speech; otherwise the final pass is genuinely needed.
	if hasPartial && len(bufferCopy)-partialSamples <= reusableTailSamples(p.vadParams.SampleRate) {
		p.log.Debug("Reusing partial transcription",
			"partial_samples", partialSamples, "buffer_samples", len(bufferCopy),
			"text", partial)
		// No ASR runs on this path, so announcing transcription would be a
		// plain falsehood: the only remaining work is cleanup and delivery.
		p.notifyPhase(session.StateCleaningUp, partial)
		go p.completeFromPartial(partial)
		return
	}

	p.log.Debug("Final pass needed",
		"partial_samples", partialSamples, "buffer_samples", len(bufferCopy))
	// Announce after taking the partial, so the state carries the text
	// already on screen rather than blanking it.
	p.notifyPhase(session.StateTranscribing, partial)
	go p.processSegmentWithPartial(bufferCopy, partial)
}

// notifyPhase announces a post-recording phase, carrying the last partial so
// the overlay keeps showing it. Blanking the text the user is reading, only
// to replace it moments later with the same words, reads as the app losing
// their dictation.
//
// The phase is passed in rather than assumed: the reuse path performs no
// recognition, so reporting StateTranscribing there would describe work that
// is not happening.
func (p *Pipeline) notifyPhase(state session.State, partial string) {
	// Logged here rather than at the key handler, which fires before any work
	// is dispatched and so cannot know which phase actually follows.
	p.log.Info(phaseMessage(state))

	if p.uiNotifier == nil {
		return
	}
	if holder, ok := p.uiNotifier.(TranscribingNotifier); ok && partial != "" {
		holder.OnPhase(state, partial)
		return
	}
	p.uiNotifier.OnStateChange(state)
}

// TranscribingNotifier is the optional extension a notifier implements to
// keep transcript text visible across the states that carry it.
type TranscribingNotifier interface {
	// OnPhase keeps provisional text visible during a post-recording phase,
	// naming the phase so the UI can describe it accurately.
	OnPhase(state session.State, partial string)
	// OnFinished hands over the completed text so the UI can display it
	// before it stops showing anything. Without this the overlay only ever
	// learns that the session ended, never what it produced.
	OnFinished(text string)
}

// notifyFinished announces the completed transcription, carrying the text so
// it can be shown before the overlay goes away.
func (p *Pipeline) notifyFinished(text string) {
	if p.uiNotifier == nil {
		return
	}
	if holder, ok := p.uiNotifier.(TranscribingNotifier); ok && text != "" {
		holder.OnFinished(text)
		return
	}
	p.uiNotifier.OnStateChange(session.StateIdle)
}

// tooShortMessage is shown when a recording is discarded for being below the
// recognition floor. The acceptance criterion for sussurro-xvj.52 is that a
// genuine lower bound is reported rather than the dictation vanishing.
const tooShortMessage = "Too short to transcribe"

// notifyTooShort tells the user their dictation was discarded as too short,
// reusing the finished-text path so the message appears where the transcript
// would have been rather than the overlay simply closing.
func (p *Pipeline) notifyTooShort() {
	p.notifyFinished(tooShortMessage)
}

// reusableTailSamples is how much unseen audio a partial may have missed and
// still be reused. Speech shorter than this cannot carry a word, so nothing
// is lost by not transcribing it; anything longer might.
func reusableTailSamples(sampleRate int) int {
	const tail = 250 * time.Millisecond
	return int(tail.Seconds() * float64(sampleRate))
}

// takeStreamingPartial ends the streaming session and returns its last
// published partial. Safe when streaming is disabled.
func (p *Pipeline) takeStreamingPartial() (text string, samples int, ok bool) {
	if p.streamer == nil {
		return "", 0, false
	}
	return p.streamer.StopAndTakePartial()
}

func (p *Pipeline) captureLoop() {
	defer p.wg.Done()

	// Start audio capture
	err := p.audioEngine.StartRecording(p.audioChan)
	if err != nil {
		p.log.Error("Failed to start recording", "error", err)
		return
	}

	defer p.audioEngine.Stop()

	// Calculate max samples based on configuration.
	resolvedDuration, resolveErr := ResolveMaxDuration(p.maxDuration)
	var maxSamples int
	if resolvedDuration == "infinite" {
		maxSamples = math.MaxInt32 // Effectively infinite
		p.log.Debug("Max recording duration set to infinite")
	} else {
		if resolveErr != nil {
			p.log.Warn("Invalid max_duration, using default",
				"value", p.maxDuration, "default", DefaultMaxDuration, "error", resolveErr)
		}
		d, _ := time.ParseDuration(resolvedDuration)
		maxSamples = int(float64(d.Seconds()) * float64(p.vadParams.SampleRate))
		p.log.Debug("Max recording duration set", "duration", d, "max_samples", maxSamples)
	}
	p.maxSamples.Store(int64(maxSamples))

	for {
		select {
		case chunk := <-p.audioChan:
			p.handleCapturedChunk(chunk, maxSamples)

		case <-p.stopChan:
			return
		}
	}
}

// handleCapturedChunk appends one capture callback or finalizes a buffer that
// has reached its configured ceiling. Finalization happens after releasing the
// pipeline lock: the streaming worker snapshots under the same lock.
func (p *Pipeline) handleCapturedChunk(chunk []float32, maxSamples int) {
	lockWait := time.Now()
	p.mu.Lock()
	p.warnIfSlow("acquiring the pipeline lock", lockWait)
	if !p.isRecording.Load() {
		p.mu.Unlock()
		return
	}
	if len(p.audioBuffer) < maxSamples {
		p.audioBuffer = append(p.audioBuffer, chunk...)
		p.mu.Unlock()
		return
	}

	p.log.Warn("Max recording duration reached, forcing stop",
		"limit", p.maxDuration,
		"reason", StopMaxDuration,
		"elapsed", time.Since(p.recordingStart).Round(time.Millisecond),
		"dropped_frames", p.droppedFrames.Load())
	p.isRecording.Store(false)
	p.isTranscribing = true
	bufferCopy := make([]float32, len(p.audioBuffer))
	copy(bufferCopy, p.audioBuffer)
	partial, partialSamples, hasPartial := p.takeStreamingPartial()
	p.wg.Add(1)
	p.mu.Unlock()

	p.finalizeRecording(bufferCopy, partial, partialSamples, hasPartial)
}

func (p *Pipeline) processSegment(samples []float32) {
	p.processSegmentWithPartial(samples, "")
}

// processSegmentWithPartial runs the final whole-buffer pass while retaining
// the last text already shown to the user. Whisper can regress on an
// independent pass; a shorter result must not discard words that its previous
// pass had already recognised.
func (p *Pipeline) processSegmentWithPartial(samples []float32, partial string) {
	defer p.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("Recovered from panic in processSegment", "error", r)
		}
		p.mu.Lock()
		p.runAfterCurrentLocked()
		p.isTranscribing = false
		p.mu.Unlock()
		p.notifyFinished(p.takeFinalText())
		if p.onCompletion != nil {
			p.onCompletion()
		}
	}()

	if len(samples) == 0 {
		p.log.Warn("Empty audio buffer, skipping processing")
		return
	}

	// Check duration (SampleRate is typically 16000)
	// If recording is less than 2 seconds, skip transcription
	durationSeconds := float64(len(samples)) / float64(p.vadParams.SampleRate)
	p.log.Debug("Processing segment", "samples", len(samples), "rate", p.vadParams.SampleRate, "duration", durationSeconds)

	if min := p.minRecordingDuration(); durationSeconds < min.Seconds() {
		// Below this, recognition reports invented phrases rather than
		// silence. Warn rather than debug: the user pressed the key meaning
		// to dictate, so a dropped attempt must not be invisible.
		p.log.Warn("Recording too short for recognition, discarding",
			"duration", time.Duration(durationSeconds*float64(time.Second)).Round(time.Millisecond),
			"minimum", min)
		p.notifyTooShort()
		return
	}

	start := time.Now()

	// 1. ASR: Transcribe Audio
	text, err := p.asrEngine.Transcribe(samples)
	asrDuration := time.Since(start)
	if err != nil {
		p.log.Error("ASR failed", "error", err)
		if partial == "" {
			return
		}
		// A failed independent pass must not turn text already recognised and
		// shown to the user into an empty delivery (sussurro-3jn).
		p.log.Warn("Final pass failed; preserving the last partial",
			"partial_chars", utf8.RuneCountInString(strings.TrimSpace(partial)))
		p.notifyPhase(session.StateCleaningUp, partial)
		p.finishSegment(partial, partial, start, asrDuration)
		return
	}

	// Whisper's non-speech annotations are not dictated text, so they are
	// dropped before anything downstream can show or deliver them.
	text = StripNonSpeechMarkers(text)

	// The final pass is an independent decode and can regress despite seeing
	// more audio. The log that prompted sussurro-3jn showed it ending
	// mid-sentence while the last partial already held the missing words.
	if finalPassShorter(text, partial) {
		// Transcript bodies remain debug-only: warning-level logging must not
		// expose a user's dictated text merely because recognition regressed.
		p.log.Warn("Final pass shorter than the last partial; preserving partial",
			"final_chars", utf8.RuneCountInString(strings.TrimSpace(text)),
			"partial_chars", utf8.RuneCountInString(strings.TrimSpace(partial)))
		text = partial
	}

	// Recognition is done; what follows is cleanup and delivery. Announce the
	// change so the overlay stops claiming to transcribe.
	p.notifyPhase(session.StateCleaningUp, text)

	p.finishSegment(text, partial, start, asrDuration)
}

// finalPassShorter enforces the delivery invariant from sussurro-3jn. A final
// decode can correct words as well as lose them, but there is no reliable way
// to distinguish those cases from text alone. Prefer the last result already
// shown to the user rather than silently deleting any of it.
func finalPassShorter(final, partial string) bool {
	return utf8.RuneCountInString(strings.TrimSpace(final)) < utf8.RuneCountInString(strings.TrimSpace(partial))
}

// completeFromPartial delivers text a streaming pass already produced,
// skipping the redundant final ASR run. Everything after recognition — word
// count, context, cleanup, publication — is identical to processSegment,
// because it is the same code.
func (p *Pipeline) completeFromPartial(text string) {
	defer p.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("Recovered from panic in completeFromPartial", "error", r)
		}
		p.mu.Lock()
		p.runAfterCurrentLocked()
		p.isTranscribing = false
		p.mu.Unlock()
		p.notifyFinished(p.takeFinalText())
		if p.onCompletion != nil {
			p.onCompletion()
		}
	}()

	p.finishSegment(text, text, time.Now(), 0)
}

// finishSegment turns a recognised transcription into a published result.
// minimumText is the last partial shown in the overlay, or empty when
// streaming produced none. Cleanup may rewrite it, but must not make the
// delivered result shorter than text the user already saw.
//
// asrDuration is how long recognition took, or zero on the partial-reuse path
// where none ran, so the completion log can attribute the wait after speech
// ends to a stage rather than reporting one opaque total.
func (p *Pipeline) finishSegment(text, minimumText string, start time.Time, asrDuration time.Duration) {

	// A word-count floor used to sit here, discarding anything under four
	// words as a false positive. That silently lost ordinary short dictations
	// such as "something very short" (sussurro-xvj.52). Hallucinated output is
	// now addressed at its source — the duration floor above, and the
	// non-speech marker filter — rather than by throwing away short speech.
	if strings.TrimSpace(text) == "" {
		p.log.Debug("No speech detected")
		return
	}

	p.log.Debug("ASR Output", "text", text, "duration", time.Since(start))

	// 2. Context: Get Current Window Info
	//
	// Timed separately: this queries the window manager, which can block, and
	// a slow answer here is indistinguishable from slow recognition in a log
	// that reports only the total.
	contextStart := time.Now()
	var ctxInfo ctxProvider.ContextInfo
	info, err := p.ctxProvider.GetContext()
	if err != nil {
		p.log.Warn("Failed to get context", "error", err)
		// Proceed without context
	}
	if info != nil {
		ctxInfo = *info
	}
	contextDuration := time.Since(contextStart)

	p.mu.Lock()
	skipLLMCleanup := p.skipLLMCleanup
	p.mu.Unlock()

	cleanedText := text
	cleaned := false
	var cleanupDuration time.Duration
	if !skipLLMCleanup {
		// 3. LLM: Cleanup and Contextualize
		// TODO: Pass context info to LLM if supported
		cleanupStart := time.Now()
		cleanedText, err = p.llmEngine.CleanupText(text)
		cleanupDuration = time.Since(cleanupStart)
		if err != nil {
			p.log.Error("LLM cleanup failed", "error", err)
			// Preserve deterministic dictionary normalization even when model
			// inference fails.
			cleanedText = p.llmEngine.NormalizeDictionary(text)
		} else {
			cleaned = true
		}
	} else {
		p.log.Debug("Skipping LLM cleanup (raw output enabled)")
		// The personal dictionary is deterministic and must remain useful on
		// the fast path. Applying it here can only normalize recognised text;
		// putting the same terms in Whisper's prompt allowed ambient noise to
		// materialize a dictionary entry as speech (sussurro-99o).
		cleanedText = p.llmEngine.NormalizeDictionary(text)
	}

	if finalPassShorter(cleanedText, minimumText) {
		p.log.Warn("Cleanup shorter than the last partial; preserving partial",
			"cleaned_chars", utf8.RuneCountInString(strings.TrimSpace(cleanedText)),
			"partial_chars", utf8.RuneCountInString(strings.TrimSpace(minimumText)))
		cleanedText = minimumText
		cleaned = false
	}

	p.mu.Lock()
	if p.lowercaseOutput {
		cleanedText = strings.ToLower(cleanedText)
	}
	p.mu.Unlock()

	// Broken down by stage: a single total cannot say whether the wait after
	// speech ends is recognition, the window-manager query, or cleanup.
	p.log.Info("Final Output",
		"raw", text,
		"cleaned", cleanedText,
		"app", ctxInfo.AppName,
		"window", ctxInfo.WindowTitle,
		"asr_duration", asrDuration.Round(time.Millisecond),
		"context_duration", contextDuration.Round(time.Millisecond),
		"cleanup_duration", cleanupDuration.Round(time.Millisecond),
		"total_duration", time.Since(start).Round(time.Millisecond),
	)

	// 4. Publish the result. Delivery is the consumer's responsibility, so
	// review mode can hold the text before anything reaches the focused window.
	p.publish(Result{
		Raw:     text,
		Text:    cleanedText,
		Context: ctxInfo,
		Cleaned: cleaned,
	})
}

// publish hands a completed result to the installed consumer (nil-safe).
//
// The delivered text is recorded here, rather than on entry to finishSegment,
// for two reasons: it is the cleaned text the user actually receives, not the
// raw ASR output, and a segment rejected as too short or empty returns before
// reaching this point, so it correctly records nothing.
func (p *Pipeline) publish(result Result) {
	p.setFinalText(result.Text)

	consumer := p.resultConsumer
	if consumer == nil {
		p.log.Debug("No result consumer installed, discarding result")
		return
	}
	consumer.OnResult(result)
}

// setFinalText records the transcription just produced.
func (p *Pipeline) setFinalText(text string) {
	p.mu.Lock()
	p.finalText = text
	p.mu.Unlock()
}

// takeFinalText returns and clears the last transcription, so a session that
// produced nothing does not display the previous one's text.
func (p *Pipeline) takeFinalText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	text := p.finalText
	p.finalText = ""
	return text
}

// phaseMessage is the console wording for a post-recording phase.
func phaseMessage(state session.State) string {
	switch state {
	case session.StateTranscribing:
		return "Finalizing..."
	case session.StateCleaningUp:
		return "Cleaning up..."
	default:
		return state.String()
	}
}

// BufferFill reports how full the recording buffer is, from 0 to 1, and
// whether a meaningful limit exists.
//
// The cap bounds memory rather than marking an expected limit, but reaching
// it truncates speech mid sentence, so the UI needs to be able to warn before
// that happens. An infinite cap reports false: there is nothing to fill.
func (p *Pipeline) BufferFill() (float64, bool) {
	max := p.maxSamples.Load()
	if max <= 0 || max == math.MaxInt32 {
		return 0, false
	}

	p.mu.Lock()
	used := len(p.audioBuffer)
	p.mu.Unlock()

	fill := float64(used) / float64(max)
	if fill > 1 {
		fill = 1
	}
	return fill, true
}
