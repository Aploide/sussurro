package pipeline

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aploide/sussurro/internal/asr"
	"github.com/aploide/sussurro/internal/audio"
	ctxProvider "github.com/aploide/sussurro/internal/context"
	"github.com/aploide/sussurro/internal/llm"
	"github.com/aploide/sussurro/internal/session"
)

// audioBufferCapFor returns a sensible pre-allocation capacity (in samples)
// for the audio buffer based on the configured max duration.
func audioBufferCapFor(maxDuration string, sampleRate int) int {
	const fallbackSecs = 30
	switch strings.ToLower(maxDuration) {
	case "infinite", "0", "":
		return fallbackSecs * sampleRate // reasonable starting size for infinite mode
	}
	d, err := time.ParseDuration(maxDuration)
	if err != nil {
		d = fallbackSecs * time.Second
	}
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

	// Channels for data flow
	audioChan chan []float32
	stopChan  chan struct{}
	wg        sync.WaitGroup

	// State
	isRecording     bool
	isTranscribing  bool // true while processSegment is running; blocks new recordings
	lowercaseOutput bool
	skipLLMCleanup  bool
	audioBuffer     []float32
	audioBufferCap  int        // pre-computed capacity to avoid repeated slice growth
	mu              sync.Mutex // Protects isRecording, isTranscribing, lowercaseOutput, skipLLMCleanup, and audioBuffer
	maxDuration     string
}

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

// SetOnCompletion sets a callback to be called when processing is done
func (p *Pipeline) SetOnCompletion(callback func()) {
	p.onCompletion = callback
}

// SetResultConsumer installs the consumer that receives completed recognition
// results. Must be called before Start(). A nil consumer discards results.
func (p *Pipeline) SetResultConsumer(consumer ResultConsumer) {
	p.resultConsumer = consumer
}

// SetUINotifier installs a StateNotifier for UI state updates.
// Must be called before Start().
func (p *Pipeline) SetUINotifier(n StateNotifier) {
	p.uiNotifier = n
	// Forward RMS data from the audio engine to the notifier
	if n != nil {
		p.audioEngine.SetRMSCallback(func(rms float32) {
			p.mu.Lock()
			recording := p.isRecording
			p.mu.Unlock()
			if recording {
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

// Start begins the pipeline processing
func (p *Pipeline) Start() error {
	p.log.Debug("Starting pipeline")

	// Start Audio Capture Loop (runs continuously to keep device ready)
	p.wg.Add(1)
	go p.captureLoop()

	return nil
}

// Stop gracefully shuts down the pipeline
func (p *Pipeline) Stop() {
	p.log.Debug("Stopping pipeline")
	close(p.stopChan)
	p.wg.Wait()
	p.log.Debug("Pipeline stopped")
}

// StartRecording begins accumulating audio data
func (p *Pipeline) StartRecording() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isRecording || p.isTranscribing {
		return
	}

	// Drain channel to ensure no stale audio is included
	for len(p.audioChan) > 0 {
		<-p.audioChan
	}

	p.isRecording = true
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
}

// StopRecording stops accumulating and triggers processing
// Returns true if recording was stopped and processing started, false if not recording
func (p *Pipeline) StopRecording() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.isRecording {
		return false
	}

	p.isRecording = false
	p.isTranscribing = true
	p.log.Debug("Recording stopped", "buffer_size", len(p.audioBuffer))
	p.notifyState(session.StateTranscribing)

	// Process the captured audio in a separate goroutine to not block
	// Make a copy of the buffer
	bufferCopy := make([]float32, len(p.audioBuffer))
	copy(bufferCopy, p.audioBuffer)

	p.wg.Add(1)
	go p.processSegment(bufferCopy)
	return true
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

	// Calculate max samples based on configuration
	var maxSamples int
	if strings.ToLower(p.maxDuration) == "infinite" || p.maxDuration == "0" {
		maxSamples = 1<<31 - 1 // Effectively infinite
		p.log.Debug("Max recording duration set to infinite")
	} else {
		// Default to 30s if not specified or invalid
		durationStr := p.maxDuration
		if durationStr == "" {
			durationStr = "30s"
		}

		d, err := time.ParseDuration(durationStr)
		if err != nil {
			p.log.Warn("Invalid max_duration format, defaulting to 30s", "value", p.maxDuration, "error", err)
			d = 30 * time.Second
		}
		maxSamples = int(float64(d.Seconds()) * float64(p.vadParams.SampleRate))
		p.log.Debug("Max recording duration set", "duration", d, "max_samples", maxSamples)
	}

	for {
		select {
		case chunk := <-p.audioChan:
			p.mu.Lock()
			if p.isRecording {
				// Safety check: Auto-stop if recording gets too long (prevents OOM/Stuck state)
				if len(p.audioBuffer) >= maxSamples {
					p.log.Warn("Max recording duration reached, forcing stop", "limit", p.maxDuration)
					p.isRecording = false
					p.isTranscribing = true
					p.notifyState(session.StateTranscribing)

					// Copy and process immediately
					bufferCopy := make([]float32, len(p.audioBuffer))
					copy(bufferCopy, p.audioBuffer)

					// Launch processing in background
					p.wg.Add(1)
					go p.processSegment(bufferCopy)
				} else {
					p.audioBuffer = append(p.audioBuffer, chunk...)
				}
			}
			p.mu.Unlock()

		case <-p.stopChan:
			return
		}
	}
}

func (p *Pipeline) processSegment(samples []float32) {
	defer p.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("Recovered from panic in processSegment", "error", r)
		}
		p.mu.Lock()
		p.isTranscribing = false
		p.mu.Unlock()
		p.notifyState(session.StateIdle)
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

	if durationSeconds < 2.0 {
		p.log.Debug("Recording too short (< 2s), skipping transcription", "duration", durationSeconds)
		return
	}

	start := time.Now()

	// 1. ASR: Transcribe Audio
	text, err := p.asrEngine.Transcribe(samples)
	if err != nil {
		p.log.Error("ASR failed", "error", err)
		return
	}

	// Check word count
	// If detected less than 4 words, avoid transcribing completely (treat as false positive)
	// We do this after transcription as we need the text to count words
	words := strings.Fields(text)
	if len(words) < 4 {
		p.log.Debug("Transcription too short (< 4 words), ignoring", "text", text, "word_count", len(words))
		return
	}

	if strings.TrimSpace(text) == "" {
		p.log.Debug("No speech detected")
		return
	}

	p.log.Debug("ASR Output", "text", text, "duration", time.Since(start))

	// 2. Context: Get Current Window Info
	var ctxInfo ctxProvider.ContextInfo
	info, err := p.ctxProvider.GetContext()
	if err != nil {
		p.log.Warn("Failed to get context", "error", err)
		// Proceed without context
	}
	if info != nil {
		ctxInfo = *info
	}

	p.mu.Lock()
	skipLLMCleanup := p.skipLLMCleanup
	p.mu.Unlock()

	cleanedText := text
	cleaned := false
	if !skipLLMCleanup {
		// 3. LLM: Cleanup and Contextualize
		// TODO: Pass context info to LLM if supported
		cleanedText, err = p.llmEngine.CleanupText(text)
		if err != nil {
			p.log.Error("LLM cleanup failed", "error", err)
			// Fallback to raw text
			cleanedText = text
		} else {
			cleaned = true
		}
	} else {
		p.log.Debug("Skipping LLM cleanup (raw output enabled)")
	}

	p.mu.Lock()
	if p.lowercaseOutput {
		cleanedText = strings.ToLower(cleanedText)
	}
	p.mu.Unlock()

	p.log.Info("Final Output",
		"raw", text,
		"cleaned", cleanedText,
		"app", ctxInfo.AppName,
		"window", ctxInfo.WindowTitle,
		"total_duration", time.Since(start),
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
func (p *Pipeline) publish(result Result) {
	consumer := p.resultConsumer
	if consumer == nil {
		p.log.Debug("No result consumer installed, discarding result")
		return
	}
	consumer.OnResult(result)
}
