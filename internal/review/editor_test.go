package review

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aploide/sussurro/internal/delivery"
	"github.com/aploide/sussurro/internal/session"
)

// fakeTextEditor records edit requests and returns a scripted result.
type fakeTextEditor struct {
	mu           sync.Mutex
	calls        int
	lastText     string
	lastInstruct string
	result       string
	err          error
	// block, when non-nil, holds the edit open until the test closes it.
	block chan struct{}
}

func (f *fakeTextEditor) EditText(original, instruction string) (string, error) {
	f.mu.Lock()
	f.calls++
	f.lastText = original
	f.lastInstruct = instruction
	result, err, block := f.result, f.err, f.block
	f.mu.Unlock()

	if block != nil {
		<-block
	}
	if err != nil {
		// EditText's contract is to hand back the original on failure.
		return original, err
	}
	return result, nil
}

func (f *fakeTextEditor) stats() (calls int, text, instruction string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.lastText, f.lastInstruct
}

// editSink collects the revisions reported back to the controller.
type editSink struct {
	mu      sync.Mutex
	ids     []session.SessionID
	texts   []string
	updated chan struct{}
}

func newEditSink() *editSink {
	return &editSink{updated: make(chan struct{}, 8)}
}

func (s *editSink) record(id session.SessionID, text string) {
	s.mu.Lock()
	s.ids = append(s.ids, id)
	s.texts = append(s.texts, text)
	s.mu.Unlock()
	select {
	case s.updated <- struct{}{}:
	default:
	}
}

func (s *editSink) await(t *testing.T) {
	t.Helper()
	select {
	case <-s.updated:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an edit result")
	}
}

func (s *editSink) snapshot() ([]session.SessionID, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]session.SessionID(nil), s.ids...), append([]string(nil), s.texts...)
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestEditorReportsRevisedText(t *testing.T) {
	model := &fakeTextEditor{result: "The revised text."}
	sink := newEditSink()
	editor := NewEditor(model, sink.record, discardLog())

	editor.ApplyEdit(9, "The original text.", "revise it")
	sink.await(t)

	ids, texts := sink.snapshot()
	if len(ids) != 1 || ids[0] != 9 {
		t.Fatalf("reported ids = %v, want [9]", ids)
	}
	if texts[0] != "The revised text." {
		t.Errorf("reported %q, want the revised text", texts[0])
	}

	calls, text, instruction := model.stats()
	if calls != 1 || text != "The original text." || instruction != "revise it" {
		t.Errorf("editor got %d calls (%q, %q), want the text and instruction once", calls, text, instruction)
	}
}

func TestEditorReportsOriginalOnFailure(t *testing.T) {
	model := &fakeTextEditor{err: errors.New("inference failed")}
	sink := newEditSink()
	editor := NewEditor(model, sink.record, discardLog())

	editor.ApplyEdit(1, "the precious text", "revise it")
	sink.await(t)

	// A failed revision must still return something deliverable.
	_, texts := sink.snapshot()
	if len(texts) != 1 || texts[0] != "the precious text" {
		t.Errorf("reported %v, want the original preserved", texts)
	}
}

func TestEditorDoesNotBlockTheCaller(t *testing.T) {
	model := &fakeTextEditor{result: "revised", block: make(chan struct{})}
	sink := newEditSink()
	editor := NewEditor(model, sink.record, discardLog())

	done := make(chan struct{})
	go func() { editor.ApplyEdit(1, "text", "revise"); close(done) }()

	// A slow model must not stall the controller goroutine that called in.
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ApplyEdit blocked on the model")
	}

	close(model.block)
	sink.await(t)
}

func TestEditorWithoutSinkDoesNotPanic(t *testing.T) {
	model := &fakeTextEditor{result: "revised"}
	editor := NewEditor(model, nil, discardLog())

	editor.ApplyEdit(1, "text", "revise")
	// Give the goroutine a chance to run and return.
	time.Sleep(50 * time.Millisecond)
}

func TestEditorDrivesTheControllerEndToEnd(t *testing.T) {
	model := &fakeTextEditor{result: "The revised text."}
	backend := &recordingBackend{}

	var controller *session.Controller
	editor := NewEditor(model, func(id session.SessionID, text string) {
		controller.OnEdited(id, text)
	}, discardLog())

	controller = session.NewController(
		stubRecognizer{},
		editor,
		delivery.NewDeliverer(backend, noWait),
		nil,
		discardLog(),
	)

	// Dictate, review, then hold again to record an edit instruction.
	controller.Handle(session.InputPress)
	controller.Handle(session.InputRelease)
	controller.OnResult(controller.SessionID(), "The original text.")
	controller.Handle(session.InputPress)
	controller.Handle(session.InputRelease)
	controller.OnResult(controller.SessionID(), "revise it")

	waitFor(t, func() bool { return controller.State() == session.ReviewReady })

	if got := controller.Text(); got != "The revised text." {
		t.Errorf("Text() = %q, want the revised text", got)
	}
	// An edit returns to review; it must never insert anything by itself.
	if len(backend.typed) != 0 {
		t.Errorf("typed %v after an edit, want nothing until asked", backend.typed)
	}
	if !controller.CanUndoEdit() {
		t.Error("CanUndoEdit() = false after an edit, want the revision recoverable")
	}
}

func TestCancelDuringEditDiscardsTheRevision(t *testing.T) {
	model := &fakeTextEditor{result: "revised", block: make(chan struct{})}

	var controller *session.Controller
	editor := NewEditor(model, func(id session.SessionID, text string) {
		controller.OnEdited(id, text)
	}, discardLog())

	controller = session.NewController(
		stubRecognizer{},
		editor,
		delivery.NewDeliverer(&recordingBackend{}, noWait),
		nil,
		discardLog(),
	)

	controller.Handle(session.InputPress)
	controller.Handle(session.InputRelease)
	controller.OnResult(controller.SessionID(), "the original")
	controller.Handle(session.InputPress)
	controller.Handle(session.InputRelease)
	controller.OnResult(controller.SessionID(), "revise it")

	// The user gives up while the model is still working.
	controller.Cancel()
	close(model.block)
	time.Sleep(100 * time.Millisecond)

	if got := controller.State(); got != session.ReviewIdle {
		t.Errorf("state = %s, want idle", got)
	}
	if got := controller.Text(); got != "" {
		t.Errorf("Text() = %q, want the cancelled session to stay empty", got)
	}
}

// waitFor polls until condition holds or the test times out.
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the expected state")
}
