package pipeline

import (
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/aploide/sussurro/internal/session"
)

// tagged records the session-tagged results the recognizer publishes.
type tagged struct {
	mu    sync.Mutex
	ids   []session.SessionID
	texts []string
}

func (r *tagged) record(id session.SessionID, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, id)
	r.texts = append(r.texts, text)
}

func (r *tagged) snapshot() ([]session.SessionID, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]session.SessionID(nil), r.ids...), append([]string(nil), r.texts...)
}

func newTestRecognizer(t *testing.T) (*SessionRecognizer, *tagged) {
	t.Helper()
	sink := &tagged{}
	p := newTestPipeline(t, &stubTranscriber{}, &stubCleaner{}, stubContext{})
	return NewSessionRecognizer(p, sink.record, slog.New(slog.NewTextHandler(io.Discard, nil))), sink
}

func TestRecognizerTagsResultWithActiveSession(t *testing.T) {
	recognizer, sink := newTestRecognizer(t)

	recognizer.StartCapture(7)
	recognizer.OnResult(Result{Text: "transcribed"})

	ids, texts := sink.snapshot()
	if len(ids) != 1 || ids[0] != 7 {
		t.Fatalf("session ids = %v, want [7]", ids)
	}
	if texts[0] != "transcribed" {
		t.Errorf("text = %q, want transcribed", texts[0])
	}
}

func TestRecognizerDropsResultWithoutActiveSession(t *testing.T) {
	recognizer, sink := newTestRecognizer(t)

	recognizer.OnResult(Result{Text: "orphan"})

	if ids, _ := sink.snapshot(); len(ids) != 0 {
		t.Fatalf("published %v with no active session, want nothing", ids)
	}
}

func TestRecognizerDropsResultAfterCancel(t *testing.T) {
	recognizer, sink := newTestRecognizer(t)

	recognizer.StartCapture(3)
	recognizer.CancelCapture(3)
	// The in-flight transcription completes after the user cancelled.
	recognizer.OnResult(Result{Text: "too late"})

	if ids, _ := sink.snapshot(); len(ids) != 0 {
		t.Fatalf("published %v after cancel, want nothing", ids)
	}
}

func TestRecognizerPublishesOneResultPerCapture(t *testing.T) {
	recognizer, sink := newTestRecognizer(t)

	recognizer.StartCapture(1)
	recognizer.OnResult(Result{Text: "first"})
	// A spurious second result belongs to no capture.
	recognizer.OnResult(Result{Text: "second"})

	_, texts := sink.snapshot()
	if len(texts) != 1 || texts[0] != "first" {
		t.Fatalf("published %v, want only the first result", texts)
	}
}

func TestRecognizerIgnoresStopForSupersededSession(t *testing.T) {
	recognizer, _ := newTestRecognizer(t)

	recognizer.StartCapture(1)
	recognizer.StartCapture(2)
	// A late stop for session 1 must not disturb session 2.
	recognizer.StopCapture(1)

	recognizer.mu.Lock()
	defer recognizer.mu.Unlock()
	if !recognizer.capturing || recognizer.active != 2 {
		t.Errorf("active session = %d capturing=%v, want session 2 still capturing",
			recognizer.active, recognizer.capturing)
	}
}

func TestRecognizerIgnoresCancelForSupersededSession(t *testing.T) {
	recognizer, sink := newTestRecognizer(t)

	recognizer.StartCapture(1)
	recognizer.StartCapture(2)
	recognizer.CancelCapture(1)

	// Session 2 is untouched, so its result still publishes.
	recognizer.OnResult(Result{Text: "session two"})
	ids, texts := sink.snapshot()
	if len(ids) != 1 || ids[0] != 2 || texts[0] != "session two" {
		t.Errorf("published ids=%v texts=%v, want session 2's result", ids, texts)
	}
}

func TestRecognizerConcurrentUse(t *testing.T) {
	recognizer, _ := newTestRecognizer(t)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		id := session.SessionID(i)
		wg.Add(4)
		go func() { defer wg.Done(); recognizer.StartCapture(id) }()
		go func() { defer wg.Done(); recognizer.StopCapture(id) }()
		go func() { defer wg.Done(); recognizer.CancelCapture(id) }()
		go func() { defer wg.Done(); recognizer.OnResult(Result{Text: "text"}) }()
	}
	wg.Wait()
}
