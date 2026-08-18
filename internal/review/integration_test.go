// Package review holds cross-package tests for the review workflow, wiring the
// real delivery backends to the real session controller.
package review

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/aploide/sussurro/internal/delivery"
	"github.com/aploide/sussurro/internal/session"
)

// stubRecognizer satisfies the controller without touching audio hardware.
type stubRecognizer struct{}

func (stubRecognizer) StartCapture(session.SessionID)  {}
func (stubRecognizer) StopCapture(session.SessionID)   {}
func (stubRecognizer) CancelCapture(session.SessionID) {}

// stubEditor is unused by these tests but required by the controller.
type stubEditor struct{}

func (stubEditor) ApplyEdit(session.SessionID, string, string) {}

// recordingBackend captures what the delivery layer asked of the host.
type recordingBackend struct {
	typed   []string
	submits int
	typeErr error
}

func (b *recordingBackend) Name() string { return "recording" }

func (b *recordingBackend) Type(text string) error {
	b.typed = append(b.typed, text)
	return b.typeErr
}

func (b *recordingBackend) Submit() error {
	b.submits++
	return nil
}

// noWait skips the input-release settle so tests stay fast.
var noWait = delivery.ReleaseWaiterFunc(func() {})

// readyController drives a controller to Ready holding text, using a real
// Deliverer over the given backend.
func readyController(t *testing.T, backend delivery.Backend, text string) *session.Controller {
	t.Helper()
	controller := session.NewController(
		stubRecognizer{},
		stubEditor{},
		delivery.NewDeliverer(backend, noWait),
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	controller.Handle(session.InputPress)
	controller.Handle(session.InputRelease)
	controller.OnResult(controller.SessionID(), text)
	if got := controller.State(); got != session.ReviewReady {
		t.Fatalf("state = %s, want ready", got)
	}
	return controller
}

func TestReviewDeliversExactTextThroughRealDeliverer(t *testing.T) {
	backend := &recordingBackend{}
	controller := readyController(t, backend, "the reviewed text")

	if err := controller.Deliver(false); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	if len(backend.typed) != 1 || backend.typed[0] != "the reviewed text" {
		t.Errorf("typed %v, want exactly the reviewed text with no added space", backend.typed)
	}
	if backend.submits != 0 {
		t.Errorf("submits = %d, want no Enter for a plain deliver", backend.submits)
	}
	if got := controller.State(); got != session.ReviewIdle {
		t.Errorf("state = %s, want idle after delivery", got)
	}
}

func TestReviewSubmitSendsEnterAfterText(t *testing.T) {
	backend := &recordingBackend{}
	controller := readyController(t, backend, "text")

	if err := controller.Deliver(true); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if backend.submits != 1 {
		t.Errorf("submits = %d, want 1", backend.submits)
	}
}

func TestBackendFailureLeavesControllerReadyWithText(t *testing.T) {
	backend := &recordingBackend{typeErr: errors.New("no window focused")}
	controller := readyController(t, backend, "precious text")

	if err := controller.Deliver(false); err == nil {
		t.Fatal("Deliver() error = nil, want the backend failure")
	}
	if got := controller.State(); got != session.ReviewReady {
		t.Errorf("state = %s, want ready so the text is not lost", got)
	}
	if got := controller.Text(); got != "precious text" {
		t.Errorf("Text() = %q, want unchanged after a failed delivery", got)
	}
}

func TestIdleControllerNeverDeliversBareEnter(t *testing.T) {
	backend := &recordingBackend{}
	controller := session.NewController(
		stubRecognizer{},
		stubEditor{},
		delivery.NewDeliverer(backend, noWait),
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	// Delivery from Idle must reach neither the backend nor the host, and
	// must say so rather than reporting a delivery that never happened.
	if err := controller.Deliver(true); !errors.Is(err, session.ErrNothingToDeliver) {
		t.Fatalf("Deliver() error = %v, want ErrNothingToDeliver", err)
	}
	if len(backend.typed) != 0 || backend.submits != 0 {
		t.Errorf("backend touched from idle (typed=%v submits=%d), want nothing",
			backend.typed, backend.submits)
	}
}

func TestCancelledSessionDeliversNothing(t *testing.T) {
	backend := &recordingBackend{}
	controller := readyController(t, backend, "text")

	controller.Cancel()
	if err := controller.Deliver(false); !errors.Is(err, session.ErrNothingToDeliver) {
		t.Fatalf("Deliver() error = %v, want ErrNothingToDeliver", err)
	}
	if len(backend.typed) != 0 {
		t.Errorf("typed %v after cancel, want nothing", backend.typed)
	}
}

func TestAutoBackendOnHostWithoutWaylandTools(t *testing.T) {
	clipboard := &recordingBackend{}
	// The primary development host: X11, no wtype, no ydotool installed.
	backend, err := delivery.SelectBackend(delivery.BackendAuto, delivery.Capabilities{
		LookPath:  func(string) (string, error) { return "", errors.New("not found") },
		Clipboard: clipboard,
	})
	if err != nil {
		t.Fatalf("SelectBackend() error = %v", err)
	}

	controller := readyController(t, backend, "text")
	if err := controller.Deliver(false); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if len(clipboard.typed) != 1 {
		t.Errorf("clipboard typed %v, want the delivery to route through it", clipboard.typed)
	}
}
