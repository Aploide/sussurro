//go:build linux

package input

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/aploide/sussurro/internal/session"
)

// Reader turns evdev key events into recording gestures. Device I/O is kept
// here; every gesture rule lives in the pure Detector, which is where the
// behaviour is tested.
type Reader struct {
	source   io.ReadCloser
	detector *Detector
	log      *slog.Logger

	// dispatch receives recording gestures.
	dispatch session.InputDispatcher
	// onCancel receives cancel gestures. Nil in immediate mode, which has no
	// session to cancel.
	onCancel func()

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	// done closes when the read loop exits, whether from Stop or the device
	// reaching EOF.
	done chan struct{}
}

// drained returns a channel closed once the read loop has exited. Used by
// tests driving a finite event stream.
func (r *Reader) drained() <-chan struct{} { return r.done }

// NewReader builds a reader over an already-open device.
func NewReader(source io.ReadCloser, detector *Detector, dispatch session.InputDispatcher, onCancel func(), log *slog.Logger) *Reader {
	return &Reader{
		source:   source,
		detector: detector,
		dispatch: dispatch,
		onCancel: onCancel,
		log:      log,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// OpenDevice opens an input device, converting a failure into actionable
// advice about the input group.
func OpenDevice(path string) (*os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, PermissionAdvice(path, err)
	}
	return file, nil
}

// Start begins reading events in the background.
func (r *Reader) Start() {
	r.wg.Add(1)
	go r.readLoop()
}

// Stop ends reading and waits for the loop to exit. Safe to call more than once.
func (r *Reader) Stop() {
	r.stopOnce.Do(func() {
		close(r.stop)
		// Closing the device unblocks the pending read.
		r.source.Close()
	})
	r.wg.Wait()
}

// readLoop reads events until the device closes or Stop is called.
func (r *Reader) readLoop() {
	defer r.wg.Done()
	defer close(r.done)

	buf := make([]byte, inputEventSize)
	for {
		select {
		case <-r.stop:
			return
		default:
		}

		if _, err := io.ReadFull(r.source, buf); err != nil {
			select {
			case <-r.stop:
				return
			default:
			}
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				r.log.Error("evdev read failed", "error", err)
			}
			return
		}

		event, ok := decodeEvent(buf)
		if !ok || event.Type != evKey {
			continue
		}

		r.handle(KeyCode(event.Code), KeyState(event.Value))
	}
}

// handle applies one decoded key event.
func (r *Reader) handle(code KeyCode, state KeyState) {
	gesture := r.detector.Handle(code, state)
	if gesture == GestureNone {
		return
	}

	r.log.Debug("evdev gesture", "gesture", gesture, "key", code)

	if gesture == GestureCancel {
		if r.onCancel != nil {
			r.onCancel()
		}
		return
	}
	if event, isInput := gesture.InputEvent(); isInput && r.dispatch != nil {
		r.dispatch.Dispatch(event)
	}
}
