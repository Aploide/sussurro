package trigger

import (
	"bufio"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aploide/sussurro/internal/session"
)

// fakeDispatcher records gestures and reports a scripted stop result.
type fakeDispatcher struct {
	mu      sync.Mutex
	events  []session.InputEvent
	stopped bool
}

func (d *fakeDispatcher) Dispatch(event session.InputEvent) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, event)
	return d.stopped
}

func (d *fakeDispatcher) recorded() []session.InputEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]session.InputEvent(nil), d.events...)
}

// fakeHandler records review actions.
type fakeHandler struct {
	mu       sync.Mutex
	cancels  int
	delivers []bool
	err      error
}

func (h *fakeHandler) Cancel() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cancels++
}

func (h *fakeHandler) Deliver(submit bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.delivers = append(h.delivers, submit)
	return h.err
}

// nothingReady reports the controller's no-op result, as when deliver arrives
// with no reviewed text waiting.
func nothingReady() error { return session.ErrNothingToDeliver }

func (h *fakeHandler) stats() (cancels int, delivers []bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cancels, append([]bool(nil), h.delivers...)
}

// newTestServer builds a server with no socket, for protocol tests.
func newTestServer(dispatch session.InputDispatcher, handler Handler) *Server {
	return &Server{
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		done:     make(chan struct{}),
		dispatch: dispatch,
		handler:  handler,
		notify:   func(string, string) {},
	}
}

func TestParseCommandAcceptsEveryCommand(t *testing.T) {
	for _, command := range commands {
		t.Run(string(command), func(t *testing.T) {
			got, err := ParseCommand(string(command))
			if err != nil {
				t.Fatalf("ParseCommand(%q) error = %v", command, err)
			}
			if got != command {
				t.Errorf("ParseCommand(%q) = %q, want %q", command, got, command)
			}
		})
	}
}

func TestParseCommandToleratesShellFormatting(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Command
	}{
		{name: "trailing newline", raw: "toggle\n", want: CommandToggle},
		{name: "carriage return", raw: "deliver\r\n", want: CommandDeliver},
		{name: "surrounding spaces", raw: "  cancel  ", want: CommandCancel},
		{name: "uppercase", raw: "SUBMIT", want: CommandSubmit},
		{name: "mixed case", raw: "Press", want: CommandPress},
		{name: "extra lines", raw: "release\nignored", want: CommandRelease},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCommand(tt.raw)
			if err != nil {
				t.Fatalf("ParseCommand(%q) error = %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("ParseCommand(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseCommandTreatsEmptyInputAsToggle(t *testing.T) {
	// The original protocol carried no command at all; those bindings must
	// keep working.
	for _, raw := range []string{"", "   ", "\n"} {
		got, err := ParseCommand(raw)
		if err != nil {
			t.Fatalf("ParseCommand(%q) error = %v", raw, err)
		}
		if got != CommandToggle {
			t.Errorf("ParseCommand(%q) = %q, want toggle", raw, got)
		}
	}
}

func TestParseCommandRejectsUnknownInput(t *testing.T) {
	for _, raw := range []string{"explode", "deliverr", "press release", "0x00"} {
		t.Run(raw, func(t *testing.T) {
			_, err := ParseCommand(raw)
			if err == nil {
				t.Fatalf("ParseCommand(%q) error = nil, want a rejection", raw)
			}
			// The error must tell the user what is accepted.
			if !strings.Contains(err.Error(), "toggle") {
				t.Errorf("error %q does not list the accepted commands", err)
			}
		})
	}
}

func TestToggleRemainsBackwardCompatible(t *testing.T) {
	dispatch := &fakeDispatcher{}
	server := newTestServer(dispatch, nil)

	// The shipped script sends "toggle" with a trailing newline.
	if reply := server.Execute("toggle\n"); reply != "RECORDING" {
		t.Errorf("reply = %q, want RECORDING", reply)
	}

	dispatch.mu.Lock()
	dispatch.stopped = true
	dispatch.mu.Unlock()

	if reply := server.Execute("toggle\n"); reply != "STOPPED" {
		t.Errorf("reply = %q, want STOPPED", reply)
	}

	events := dispatch.recorded()
	if len(events) != 2 || events[0] != session.InputToggle || events[1] != session.InputToggle {
		t.Errorf("events = %v, want two toggles", events)
	}
}

func TestGestureCommandsMapToInputEvents(t *testing.T) {
	tests := []struct {
		command Command
		want    session.InputEvent
	}{
		{command: CommandToggle, want: session.InputToggle},
		{command: CommandPress, want: session.InputPress},
		{command: CommandRelease, want: session.InputRelease},
	}

	for _, tt := range tests {
		t.Run(string(tt.command), func(t *testing.T) {
			dispatch := &fakeDispatcher{}
			server := newTestServer(dispatch, nil)

			server.Execute(string(tt.command))

			events := dispatch.recorded()
			if len(events) != 1 || events[0] != tt.want {
				t.Errorf("events = %v, want [%v]", events, tt.want)
			}
		})
	}
}

func TestNonGestureCommandsAreNotGestures(t *testing.T) {
	for _, command := range []Command{CommandCancel, CommandDeliver, CommandSubmit} {
		if _, isGesture := command.InputEvent(); isGesture {
			t.Errorf("%s reported as a gesture, want a workflow action", command)
		}
	}
}

func TestReviewCommandsReachTheHandler(t *testing.T) {
	dispatch := &fakeDispatcher{}
	handler := &fakeHandler{}
	server := newTestServer(dispatch, handler)

	if reply := server.Execute("cancel"); reply != "CANCELLED" {
		t.Errorf("cancel reply = %q, want CANCELLED", reply)
	}
	if reply := server.Execute("deliver"); reply != "DELIVERED" {
		t.Errorf("deliver reply = %q, want DELIVERED", reply)
	}
	if reply := server.Execute("submit"); reply != "DELIVERED" {
		t.Errorf("submit reply = %q, want DELIVERED", reply)
	}

	cancels, delivers := handler.stats()
	if cancels != 1 {
		t.Errorf("cancels = %d, want 1", cancels)
	}
	if len(delivers) != 2 || delivers[0] || !delivers[1] {
		t.Errorf("delivers = %v, want [false true]", delivers)
	}
	// Workflow actions must not be dispatched as recording gestures.
	if events := dispatch.recorded(); len(events) != 0 {
		t.Errorf("dispatched %v for workflow commands, want none", events)
	}
}

func TestReviewCommandsRefusedInImmediateMode(t *testing.T) {
	for _, command := range []string{"cancel", "deliver", "submit"} {
		t.Run(command, func(t *testing.T) {
			dispatch := &fakeDispatcher{}
			// No handler: immediate mode has no review workflow to drive.
			server := newTestServer(dispatch, nil)

			reply := server.Execute(command)
			if !strings.HasPrefix(reply, "ERROR") {
				t.Errorf("reply = %q, want an error", reply)
			}
			if !strings.Contains(reply, "review mode") {
				t.Errorf("reply = %q, want it to explain review mode is required", reply)
			}
			if events := dispatch.recorded(); len(events) != 0 {
				t.Errorf("dispatched %v, want no state change", events)
			}
		})
	}
}

func TestUnknownCommandChangesNothing(t *testing.T) {
	dispatch := &fakeDispatcher{}
	handler := &fakeHandler{}
	server := newTestServer(dispatch, handler)

	reply := server.Execute("self-destruct")
	if !strings.HasPrefix(reply, "ERROR") {
		t.Errorf("reply = %q, want an error", reply)
	}
	if events := dispatch.recorded(); len(events) != 0 {
		t.Errorf("dispatched %v for an unknown command, want none", events)
	}
	if cancels, delivers := handler.stats(); cancels != 0 || len(delivers) != 0 {
		t.Errorf("handler touched (%d cancels, %v delivers), want nothing", cancels, delivers)
	}
}

func TestDeliveryFailureReportedToClient(t *testing.T) {
	handler := &fakeHandler{err: errors.New("no window focused")}
	server := newTestServer(&fakeDispatcher{}, handler)

	reply := server.Execute("deliver")
	if !strings.HasPrefix(reply, "ERROR") {
		t.Errorf("reply = %q, want an error", reply)
	}
	if !strings.Contains(reply, "no window focused") {
		t.Errorf("reply = %q, want the backend failure surfaced", reply)
	}
}

func TestReleaseWithNothingRecordingIsIdle(t *testing.T) {
	dispatch := &fakeDispatcher{}
	server := newTestServer(dispatch, nil)

	// A stray release must not read as the start of a recording.
	if reply := server.Execute("release"); reply != "IDLE" {
		t.Errorf("reply = %q, want IDLE", reply)
	}
}

func TestStartRequiresADispatcher(t *testing.T) {
	server := newTestServer(nil, nil)
	server.socket = filepath.Join(t.TempDir(), "sussurro.sock")

	if err := server.Start(nil); err == nil {
		t.Fatal("Start(nil) error = nil, want a refusal")
	}
}

func TestServerRespondsOverTheSocket(t *testing.T) {
	dispatch := &fakeDispatcher{}
	server := newTestServer(dispatch, &fakeHandler{})
	server.socket = filepath.Join(t.TempDir(), "sussurro.sock")

	if err := server.Start(dispatch); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(server.Stop)

	reply := sendCommand(t, server.socket, "press\n")
	if reply != "RECORDING" {
		t.Errorf("reply = %q, want RECORDING", reply)
	}
	if events := dispatch.recorded(); len(events) != 1 || events[0] != session.InputPress {
		t.Errorf("events = %v, want one press", events)
	}
}

func TestServerRejectsUnknownCommandOverTheSocket(t *testing.T) {
	dispatch := &fakeDispatcher{}
	server := newTestServer(dispatch, nil)
	server.socket = filepath.Join(t.TempDir(), "sussurro.sock")

	if err := server.Start(dispatch); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(server.Stop)

	if reply := sendCommand(t, server.socket, "bogus\n"); !strings.HasPrefix(reply, "ERROR") {
		t.Errorf("reply = %q, want an error", reply)
	}
	if events := dispatch.recorded(); len(events) != 0 {
		t.Errorf("dispatched %v, want no state change", events)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	dispatch := &fakeDispatcher{}
	server := newTestServer(dispatch, nil)
	server.socket = filepath.Join(t.TempDir(), "sussurro.sock")

	if err := server.Start(dispatch); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	server.Stop()
	// A second Stop must not close the done channel twice.
	server.Stop()
}

func TestConcurrentCommandsAreSafe(t *testing.T) {
	dispatch := &fakeDispatcher{}
	handler := &fakeHandler{}
	server := newTestServer(dispatch, handler)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); server.Execute("toggle") }()
		go func() { defer wg.Done(); server.Execute("deliver") }()
		go func() { defer wg.Done(); server.Execute("cancel") }()
	}
	wg.Wait()
}

// sendCommand writes one command to the socket and returns the reply line.
func sendCommand(t *testing.T, socket, command string) string {
	t.Helper()

	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		t.Fatalf("dialing %s: %v", socket, err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(command)); err != nil {
		t.Fatalf("writing command: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && reply == "" {
		t.Fatalf("reading reply: %v", err)
	}
	return strings.TrimSpace(reply)
}

func TestDeliverWithNothingReadyReportsIdle(t *testing.T) {
	for _, command := range []string{"deliver", "submit"} {
		t.Run(command, func(t *testing.T) {
			handler := &fakeHandler{err: nothingReady()}
			server := newTestServer(&fakeDispatcher{}, handler)

			// Reporting DELIVERED here would tell a script the text went out
			// when nothing was ready.
			if reply := server.Execute(command); reply != "IDLE" {
				t.Errorf("reply = %q, want IDLE", reply)
			}
		})
	}
}

func TestDeliverWithNothingReadyIsNotAnError(t *testing.T) {
	handler := &fakeHandler{err: nothingReady()}
	server := newTestServer(&fakeDispatcher{}, handler)

	// A script binding deliver to a key must not see a failure just because
	// there was nothing to send.
	if reply := server.Execute("deliver"); strings.HasPrefix(reply, "ERROR") {
		t.Errorf("reply = %q, want a non-error status", reply)
	}
}
