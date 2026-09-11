package pipeline

import "testing"

func TestRunWhenIdleRunsImmediately(t *testing.T) {
	p := &Pipeline{}
	called := false
	p.RunWhenIdle(func() { called = true })
	if !called {
		t.Error("idle callback was queued instead of run")
	}
}

func TestRunWhenIdleDefersThroughActiveDictation(t *testing.T) {
	for _, tt := range []struct {
		name         string
		recording    bool
		transcribing bool
	}{
		{name: "recording", recording: true},
		{name: "cleanup", transcribing: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := &Pipeline{isTranscribing: tt.transcribing}
			p.isRecording.Store(tt.recording)
			called := false
			sawGate := false
			p.RunWhenIdle(func() {
				called = true
				sawGate = p.isTranscribing
			})
			if called {
				t.Fatal("callback ran during active dictation")
			}

			p.mu.Lock()
			p.isRecording.Store(false)
			// Completion keeps this gate set while callbacks run, preventing a
			// new recording from starting with half-updated engines.
			p.isTranscribing = true
			p.runAfterCurrentLocked()
			p.isTranscribing = false
			p.mu.Unlock()

			if !called {
				t.Error("deferred callback did not run at completion")
			}
			if !sawGate {
				t.Error("callback ran after the transcription gate was released")
			}
			if len(p.afterCurrent) != 0 {
				t.Errorf("%d callbacks remain queued", len(p.afterCurrent))
			}
		})
	}
}
