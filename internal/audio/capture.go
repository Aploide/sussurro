package audio

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/gen2brain/malgo"
)

// byteBufPool recycles the temporary []byte copies that the malgo device
// callback makes before handing data to the processing pipeline.
// Audio chunks are fixed-size for the lifetime of a capture session, so
// the pool reaches steady-state almost immediately.
var byteBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, 4096)
		return &buf
	},
}

// CaptureEngine handles audio recording using malgo (miniaudio)
type CaptureEngine struct {
	ctx          *malgo.AllocatedContext
	device       *malgo.Device
	sampleRate   int
	channels     int
	bitDepth     int // Should be 16 for Whisper
	isRecording  bool
	mutex        sync.Mutex
	dataCallback func([]byte)
	rmsCB        atomic.Pointer[func(float32)] // RMS callback; atomic for lock-free hot-path reads
	dropCB       atomic.Pointer[func()]        // Called when a frame is discarded; atomic for the same reason
}

// SetRMSCallback installs a callback that receives the RMS level of each
// incoming audio chunk.  The callback is invoked from the audio thread —
// implementations must be non-blocking.
func (e *CaptureEngine) SetRMSCallback(cb func(float32)) {
	if cb == nil {
		e.rmsCB.Store(nil)
		return
	}
	e.rmsCB.Store(&cb)
}

// SetDropCallback registers a function called whenever a captured frame is
// discarded because the consumer could not keep up. Passing nil clears it.
// The callback runs on the realtime audio thread and must not block.
func (e *CaptureEngine) SetDropCallback(cb func()) {
	if cb == nil {
		e.dropCB.Store(nil)
		return
	}
	e.dropCB.Store(&cb)
}

// computeRMS returns the root-mean-square of a float32 sample slice.
func computeRMS(samples []float32) float32 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return float32(math.Sqrt(sum / float64(len(samples))))
}

// NewCaptureEngine creates a new engine instance
// Whisper typically expects 16kHz, 1 channel, 16-bit PCM
func NewCaptureEngine(sampleRate, channels int) (*CaptureEngine, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to init audio context: %w", err)
	}

	return &CaptureEngine{
		ctx:        ctx,
		sampleRate: sampleRate,
		channels:   channels,
		bitDepth:   16, // Fixed to 16-bit for now
	}, nil
}

// StartRecording starts capturing audio and sends data to the provided channel
func (e *CaptureEngine) StartRecording(dataChan chan<- []float32) error {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if e.isRecording {
		return nil
	}

	// Define the callback that writes to the channel
	onData := func(data []byte) {
		// Reinterpret the raw F32 bytes as a []float32 via unsafe — avoids a
		// per-sample decode loop and compiles down to a single memcpy.
		numSamples := len(data) / 4
		floats := make([]float32, numSamples)
		if numSamples > 0 {
			src := unsafe.Slice((*float32)(unsafe.Pointer(&data[0])), numSamples)
			copy(floats, src)
		}

		// Invoke RMS callback (non-blocking, lock-free atomic read)
		cbPtr := e.rmsCB.Load()
		if cbPtr != nil {
			rms := computeRMS(floats)
			(*cbPtr)(rms)
		}

		// Non-blocking send
		select {
		case dataChan <- floats:
		default:
			// Dropping is preferable to blocking the realtime audio thread,
			// but it silently loses speech, so report it rather than
			// discarding without trace.
			if dropPtr := e.dropCB.Load(); dropPtr != nil {
				(*dropPtr)()
			}
		}
	}

	// Start the internal device
	return e.startDevice(onData)
}

// startDevice initiates the low-level audio stream
func (e *CaptureEngine) startDevice(onData func([]byte)) error {
	e.dataCallback = onData

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatF32
	deviceConfig.Capture.Channels = uint32(e.channels)
	deviceConfig.SampleRate = uint32(e.sampleRate)
	deviceConfig.Alsa.NoMMap = 1 // Common fix for Linux ALSA

	var err error
	// Callback to handle incoming audio data
	e.device, err = malgo.InitDevice(e.ctx.Context, deviceConfig, malgo.DeviceCallbacks{
		Data: func(pOutputSample, pInputSamples []byte, framecount uint32) {
			if e.dataCallback == nil || len(pInputSamples) == 0 {
				return
			}

			// Grab a pooled byte buffer and resize it to fit this chunk.
			// onData (dataCallback) is synchronous so the buffer can be
			// returned to the pool as soon as the call returns.
			rawPtr := byteBufPool.Get().(*[]byte)
			raw := *rawPtr
			if cap(raw) < len(pInputSamples) {
				raw = make([]byte, len(pInputSamples))
			} else {
				raw = raw[:len(pInputSamples)]
			}
			copy(raw, pInputSamples)
			*rawPtr = raw

			e.dataCallback(raw)

			byteBufPool.Put(rawPtr)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to init device: %w", err)
	}

	err = e.device.Start()
	if err != nil {
		return fmt.Errorf("failed to start device: %w", err)
	}

	e.isRecording = true
	return nil
}

// Stop halts the stream
func (e *CaptureEngine) Stop() error {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if !e.isRecording {
		return nil
	}

	if e.device != nil {
		e.device.Uninit()
		e.device = nil
	}
	e.isRecording = false
	return nil
}

// Close releases resources
func (e *CaptureEngine) Close() {
	e.Stop()
	if e.ctx != nil {
		e.ctx.Free()
		e.ctx = nil
	}
}
