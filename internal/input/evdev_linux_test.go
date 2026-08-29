//go:build linux

package input

import (
	"bytes"
	"encoding/binary"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aploide/sussurro/internal/session"
)

// encodeEvent renders one input_event exactly as the kernel writes it.
func encodeEvent(evType uint16, code KeyCode, value KeyState) []byte {
	buf := make([]byte, inputEventSize)
	binary.LittleEndian.PutUint64(buf[0:8], 0)
	binary.LittleEndian.PutUint64(buf[8:16], 0)
	binary.LittleEndian.PutUint16(buf[16:18], evType)
	binary.LittleEndian.PutUint16(buf[18:20], uint16(code))
	binary.LittleEndian.PutUint32(buf[20:24], uint32(value))
	return buf
}

// eventStream builds a readable stream of key events.
func eventStream(events ...[]byte) io.ReadCloser {
	var buf bytes.Buffer
	for _, event := range events {
		buf.Write(event)
	}
	return io.NopCloser(&buf)
}

// recordingDispatcher captures the events the reader dispatched.
type recordingDispatcher struct {
	mu      sync.Mutex
	events  []session.InputEvent
	updated chan struct{}
}

func newRecordingDispatcher() *recordingDispatcher {
	return &recordingDispatcher{updated: make(chan struct{}, 32)}
}

func (d *recordingDispatcher) Dispatch(event session.InputEvent) session.InputOutcome {
	d.mu.Lock()
	d.events = append(d.events, event)
	d.mu.Unlock()
	select {
	case d.updated <- struct{}{}:
	default:
	}
	if event == session.InputRelease {
		return session.InputStopped
	}
	return session.InputStarted
}

func (d *recordingDispatcher) recorded() []session.InputEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]session.InputEvent(nil), d.events...)
}

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// drain waits until the reader has consumed a finite scripted stream. Stop()
// closes the device, which is right for a live one but would race a test's
// bounded input.
func drain(t *testing.T, reader *Reader) {
	t.Helper()
	reader.Start()
	select {
	case <-reader.drained():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the reader to consume the stream")
	}
	reader.Stop()
}

func TestDecodeEventParsesKernelLayout(t *testing.T) {
	raw := encodeEvent(evKey, KeySpace, KeyPressed)

	event, ok := decodeEvent(raw)
	if !ok {
		t.Fatal("decodeEvent() reported failure on a well-formed event")
	}
	if event.Type != evKey {
		t.Errorf("Type = %d, want %d", event.Type, evKey)
	}
	if KeyCode(event.Code) != KeySpace {
		t.Errorf("Code = %d, want %d", event.Code, KeySpace)
	}
	if KeyState(event.Value) != KeyPressed {
		t.Errorf("Value = %d, want %d", event.Value, KeyPressed)
	}
}

func TestDecodeEventRejectsShortBuffer(t *testing.T) {
	if _, ok := decodeEvent(make([]byte, inputEventSize-1)); ok {
		t.Error("decodeEvent() accepted a truncated buffer")
	}
}

func TestReaderDispatchesChordGestures(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})
	dispatch := newRecordingDispatcher()

	source := eventStream(
		encodeEvent(evKey, KeyLeftCtrl, KeyPressed),
		encodeEvent(evKey, KeyLeftShift, KeyPressed),
		encodeEvent(evKey, KeySpace, KeyPressed),
		encodeEvent(evKey, KeySpace, KeyReleased),
	)

	reader := NewReader(source, detector, dispatch, nil, testLog())
	drain(t, reader)

	events := dispatch.recorded()
	if len(events) != 2 {
		t.Fatalf("dispatched %v, want a press and a release", events)
	}
	if events[0] != session.InputPress || events[1] != session.InputRelease {
		t.Errorf("dispatched %v, want [press release]", events)
	}
}

func TestReaderIgnoresNonKeyEvents(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})
	dispatch := newRecordingDispatcher()

	// Type 3 is EV_ABS: pointer traffic must never drive a recording.
	source := eventStream(
		encodeEvent(3, KeySpace, KeyPressed),
		encodeEvent(3, KeyLeftCtrl, KeyPressed),
	)

	reader := NewReader(source, detector, dispatch, nil, testLog())
	drain(t, reader)

	if events := dispatch.recorded(); len(events) != 0 {
		t.Errorf("dispatched %v for non-key events, want none", events)
	}
}

func TestReaderReportsCancelSeparately(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), mustParse(t, "ctrl+shift+alt"))
	dispatch := newRecordingDispatcher()

	cancels := 0
	source := eventStream(
		encodeEvent(evKey, KeyLeftCtrl, KeyPressed),
		encodeEvent(evKey, KeyLeftShift, KeyPressed),
		encodeEvent(evKey, KeyLeftAlt, KeyPressed),
	)

	reader := NewReader(source, detector, dispatch, func() { cancels++ }, testLog())
	drain(t, reader)

	if cancels != 1 {
		t.Errorf("cancels = %d, want 1", cancels)
	}
	// Cancel is a workflow action, not a recording gesture.
	if events := dispatch.recorded(); len(events) != 0 {
		t.Errorf("dispatched %v for a cancel, want none", events)
	}
}

func TestReaderIgnoresAutorepeatStream(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})
	dispatch := newRecordingDispatcher()

	events := [][]byte{
		encodeEvent(evKey, KeyLeftCtrl, KeyPressed),
		encodeEvent(evKey, KeyLeftShift, KeyPressed),
		encodeEvent(evKey, KeySpace, KeyPressed),
	}
	// A long hold streams autorepeat for every held key.
	for i := 0; i < 20; i++ {
		events = append(events, encodeEvent(evKey, KeySpace, KeyRepeated))
	}
	events = append(events, encodeEvent(evKey, KeySpace, KeyReleased))

	reader := NewReader(eventStream(events...), detector, dispatch, nil, testLog())
	drain(t, reader)

	got := dispatch.recorded()
	if len(got) != 2 {
		t.Fatalf("dispatched %v, want exactly one press and one release", got)
	}
}

func TestReaderStopIsIdempotent(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})
	reader := NewReader(eventStream(), detector, newRecordingDispatcher(), nil, testLog())

	reader.Start()
	reader.Stop()
	// A second Stop must not close the stop channel twice.
	reader.Stop()
}

// blockingSource never returns data until closed, like an idle device.
type blockingSource struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingSource() *blockingSource {
	return &blockingSource{closed: make(chan struct{})}
}

func (s *blockingSource) Read([]byte) (int, error) {
	<-s.closed
	return 0, os.ErrClosed
}

func (s *blockingSource) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func TestReaderStopUnblocksAnIdleDevice(t *testing.T) {
	detector := NewDetector(mustParse(t, "ctrl+shift+space"), Chord{})
	reader := NewReader(newBlockingSource(), detector, newRecordingDispatcher(), nil, testLog())

	reader.Start()

	done := make(chan struct{})
	go func() { reader.Stop(); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() blocked on an idle device read")
	}
}

func TestMatchDeviceByExactPath(t *testing.T) {
	candidates := []DeviceCandidate{
		{Path: "/dev/input/event3", Name: "Some Keyboard"},
		{Path: "/dev/input/event4", Name: "Other Keyboard"},
	}

	got, err := MatchDevice("/dev/input/event4", candidates)
	if err != nil {
		t.Fatalf("MatchDevice() error = %v", err)
	}
	if got.Path != "/dev/input/event4" {
		t.Errorf("matched %s, want the exact path", got.Path)
	}
}

func TestMatchDeviceByNameSubstring(t *testing.T) {
	candidates := []DeviceCandidate{
		{Path: "/dev/input/event3", Name: "Kinesis Advantage360"},
		{Path: "/dev/input/event4", Name: "Logitech Mouse"},
	}

	got, err := MatchDevice("kinesis", candidates)
	if err != nil {
		t.Fatalf("MatchDevice() error = %v", err)
	}
	if got.Path != "/dev/input/event3" {
		t.Errorf("matched %s, want the Kinesis keyboard", got.Path)
	}
}

func TestMatchDeviceRejectsAmbiguousPattern(t *testing.T) {
	candidates := []DeviceCandidate{
		{Path: "/dev/input/event3", Name: "Generic Keyboard"},
		{Path: "/dev/input/event4", Name: "Generic Keyboard Consumer Control"},
	}

	// Silently picking one would bind the hotkey to the wrong keyboard.
	_, err := MatchDevice("generic", candidates)
	if err == nil {
		t.Fatal("MatchDevice() error = nil, want ambiguity reported")
	}
	if !strings.Contains(err.Error(), "matches 2 devices") {
		t.Errorf("error %q does not report the ambiguity", err)
	}
}

func TestMatchDeviceReportsAvailableDevicesWhenNothingMatches(t *testing.T) {
	candidates := []DeviceCandidate{{Path: "/dev/input/event3", Name: "Kinesis Advantage360"}}

	_, err := MatchDevice("dvorak", candidates)
	if err == nil {
		t.Fatal("MatchDevice() error = nil, want a rejection")
	}
	// The message must let the user pick a working value.
	if !strings.Contains(err.Error(), "Kinesis Advantage360") {
		t.Errorf("error %q does not list the available devices", err)
	}
}

func TestMatchDevicePrefersStablePathWithNoPattern(t *testing.T) {
	candidates := []DeviceCandidate{
		{Path: "/dev/input/event3", Name: "Some Keyboard"},
		{Path: "/dev/input/by-id/usb-Vendor-event-kbd", Name: "Some Keyboard", Stable: true},
	}

	got, err := MatchDevice("", candidates)
	if err != nil {
		t.Fatalf("MatchDevice() error = %v", err)
	}
	// by-id survives reboots and re-plugging; event numbering does not.
	if !got.Stable {
		t.Errorf("matched %s, want the stable by-id path", got.Path)
	}
}

func TestMatchDeviceWithNoCandidates(t *testing.T) {
	if _, err := MatchDevice("anything", nil); err == nil {
		t.Fatal("MatchDevice() error = nil, want a rejection with no devices")
	}
}

func TestPermissionAdviceExplainsTheInputGroup(t *testing.T) {
	err := PermissionAdvice("/dev/input/event3", fs.ErrPermission)
	if err == nil {
		t.Fatal("PermissionAdvice() = nil, want an error")
	}

	message := err.Error()
	// The advice must be enough to fix the problem without searching.
	for _, want := range []string{"input", "usermod", "log out", "native"} {
		if !strings.Contains(message, want) {
			t.Errorf("advice %q does not mention %q", message, want)
		}
	}
}

func TestPermissionAdvicePassesThroughOtherErrors(t *testing.T) {
	err := PermissionAdvice("/dev/input/event3", fs.ErrNotExist)
	if err == nil {
		t.Fatal("PermissionAdvice() = nil, want an error")
	}
	if strings.Contains(err.Error(), "usermod") {
		t.Errorf("advice %q wrongly blames permissions for a missing device", err)
	}
}

func TestDiscoverKeyboardsDoesNotRequireDeviceAccess(t *testing.T) {
	// Discovery reads directory entries and sysfs only, so it must succeed
	// even for a user with no permission to open the devices.
	candidates, err := DiscoverKeyboards()
	if err != nil {
		t.Fatalf("DiscoverKeyboards() error = %v", err)
	}
	for _, candidate := range candidates {
		if candidate.Path == "" {
			t.Error("discovered a candidate with no path")
		}
	}
	t.Logf("discovered %d candidate keyboards on this host", len(candidates))
}
