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

// finishPipelinePass puts the pipeline back to rest, as it is after a
// recording has been stopped and its transcription has finished, so the next
// StartCapture is accepted.
func finishPipelinePass(r *SessionRecognizer) {
	r.pipeline.mu.Lock()
	defer r.pipeline.mu.Unlock()
	r.pipeline.isRecording.Store(false)
	r.pipeline.isTranscribing = false
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
	recognizer.OnResult(Result{Text: "done"})
	finishPipelinePass(recognizer)
	recognizer.StartCapture(2)
	// A late stop for session 1 must not disturb session 2.
	recognizer.StopCapture(1)

	if id, ok := recognizer.Active(); !ok || id != 2 {
		t.Errorf("Active() = %d, %v, want session 2 still capturing", id, ok)
	}
}

func TestRecognizerIgnoresCancelForSupersededSession(t *testing.T) {
	recognizer, sink := newTestRecognizer(t)

	recognizer.StartCapture(1)
	recognizer.OnResult(Result{Text: "session one"})
	finishPipelinePass(recognizer)
	recognizer.StartCapture(2)
	recognizer.CancelCapture(1)

	// Session 2 is untouched, so its result still publishes.
	recognizer.OnResult(Result{Text: "session two"})
	ids, texts := sink.snapshot()
	if len(ids) != 2 || ids[1] != 2 || texts[1] != "session two" {
		t.Errorf("published ids=%v texts=%v, want session 2's result last", ids, texts)
	}
}

func TestRecognizerRefusesCaptureWhilePipelineBusy(t *testing.T) {
	recognizer, sink := newTestRecognizer(t)

	if !recognizer.StartCapture(1) {
		t.Fatal("StartCapture(1) = false on an idle pipeline, want true")
	}
	// The pipeline is still recording session 1, so a second capture cannot
	// begin and must not displace the active session.
	if recognizer.StartCapture(2) {
		t.Fatal("StartCapture(2) = true while recording, want false")
	}
	if id, ok := recognizer.Active(); !ok || id != 1 {
		t.Errorf("Active() = %d, %v, want session 1", id, ok)
	}

	recognizer.OnResult(Result{Text: "one"})
	if _, ok := recognizer.Active(); ok {
		t.Error("Active() reports a session after its result, want none")
	}

	// Transcription of the previous recording refuses a new one as well, so
	// a press during it cannot open a session that never gets a result.
	finishPipelinePass(recognizer)
	recognizer.pipeline.mu.Lock()
	recognizer.pipeline.isTranscribing = true
	recognizer.pipeline.mu.Unlock()
	if recognizer.StartCapture(3) {
		t.Fatal("StartCapture(3) = true while transcribing, want false")
	}
	if _, ok := recognizer.Active(); ok {
		t.Error("Active() reports a session after a refused start, want none")
	}
	if ids, _ := sink.snapshot(); len(ids) != 1 {
		t.Errorf("published %v, want only session 1's result", ids)
	}
}

func TestRecognizerReportsEmptyResultWhenPassPublishesNothing(t *testing.T) {
	// Too-short, silent, and failed recordings end the pass without a
	// result; the session must still hear that it is over.
	recognizer, sink := newTestRecognizer(t)

	recognizer.StartCapture(4)
	// The pipeline stopped recording and its pass ran to completion.
	finishPipelinePass(recognizer)
	recognizer.pipeline.onCompletion()

	ids, texts := sink.snapshot()
	if len(ids) != 1 || ids[0] != 4 || texts[0] != "" {
		t.Fatalf("published ids=%v texts=%q, want an empty result for session 4", ids, texts)
	}
	if _, ok := recognizer.Active(); ok {
		t.Error("Active() still reports session 4 after its pass ended, want none")
	}

	// A completion following a published result adds nothing.
	finishPipelinePass(recognizer)
	recognizer.StartCapture(5)
	recognizer.OnResult(Result{Text: "five"})
	finishPipelinePass(recognizer)
	recognizer.pipeline.onCompletion()
	if ids, _ := sink.snapshot(); len(ids) != 2 {
		t.Errorf("published %v, want no extra result after session 5's", ids)
	}
}

func TestRecognizerIgnoresCompletionOfEarlierPassWhileRecording(t *testing.T) {
	// The pipeline clears its transcribing flag just before running the
	// completion hook, so a new capture can slip in between; that pass's
	// completion must not end the capture that is now recording.
	recognizer, sink := newTestRecognizer(t)

	recognizer.StartCapture(6)
	recognizer.pipeline.onCompletion()

	if ids, _ := sink.snapshot(); len(ids) != 0 {
		t.Fatalf("published %v while still recording, want nothing", ids)
	}
	if id, ok := recognizer.Active(); !ok || id != 6 {
		t.Errorf("Active() = %d, %v, want session 6 still capturing", id, ok)
	}
}

func TestRecognizerCompletionHookComposesWithOthers(t *testing.T) {
	// main.go installs a diagnostic completion hook of its own; whichever is
	// installed first, both must run.
	sink := &tagged{}
	p := newTestPipeline(t, &stubTranscriber{}, &stubCleaner{}, stubContext{})
	var before, after int
	p.SetOnCompletion(func() { before++ })
	recognizer := NewSessionRecognizer(p, sink.record, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.SetOnCompletion(func() { after++ })

	recognizer.StartCapture(8)
	finishPipelinePass(recognizer)
	p.onCompletion()

	if before != 1 || after != 1 {
		t.Errorf("hooks ran before=%d after=%d, want each once", before, after)
	}
	if ids, _ := sink.snapshot(); len(ids) != 1 || ids[0] != 8 {
		t.Errorf("published %v, want the empty result for session 8", ids)
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
