package session

import "testing"

func TestStateValues(t *testing.T) {
	tests := []struct {
		state State
		name  string
	}{
		{state: StateIdle, name: "idle"},
		{state: StateRecording, name: "recording"},
		{state: StateTranscribing, name: "transcribing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.state.Valid() {
				t.Fatalf("%v should be valid", tt.state)
			}
			if got := tt.state.String(); got != tt.name {
				t.Fatalf("State.String() = %q, want %q", got, tt.name)
			}
		})
	}

	invalid := State(stateCount)
	if invalid.Valid() {
		t.Fatalf("State(%d) should be invalid", invalid)
	}
	if got := invalid.String(); got != "invalid" {
		t.Fatalf("invalid State.String() = %q, want %q", got, "invalid")
	}
}

func TestInputEventValues(t *testing.T) {
	tests := []struct {
		event InputEvent
		name  string
	}{
		{event: InputPress, name: "press"},
		{event: InputRelease, name: "release"},
		{event: InputToggle, name: "toggle"},
		{event: InputEditPress, name: "edit-press"},
		{event: InputEditRelease, name: "edit-release"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.event.Valid() {
				t.Fatalf("%v should be valid", tt.event)
			}
			if got := tt.event.String(); got != tt.name {
				t.Fatalf("InputEvent.String() = %q, want %q", got, tt.name)
			}
		})
	}

	invalid := InputEvent(inputEventCount)
	if invalid.Valid() {
		t.Fatalf("InputEvent(%d) should be invalid", invalid)
	}
	if got := invalid.String(); got != "invalid" {
		t.Fatalf("invalid InputEvent.String() = %q, want %q", got, "invalid")
	}
}

type fakeRecorder struct {
	starts     int
	stops      int
	stopResult bool
}

func (recorder *fakeRecorder) StartRecording() {
	recorder.starts++
}

func (recorder *fakeRecorder) StopRecording() bool {
	recorder.stops++
	return recorder.stopResult
}

func TestDispatchImmediateInput(t *testing.T) {
	tests := []struct {
		name        string
		event       InputEvent
		stopResult  bool
		wantStarts  int
		wantStops   int
		wantStopped bool
	}{
		{name: "press starts", event: InputPress, wantStarts: 1},
		{name: "release stops active recording", event: InputRelease, stopResult: true, wantStops: 1, wantStopped: true},
		{name: "release ignores idle recorder", event: InputRelease, wantStops: 1},
		{name: "toggle stops active recording", event: InputToggle, stopResult: true, wantStops: 1, wantStopped: true},
		{name: "toggle starts idle recorder", event: InputToggle, wantStarts: 1, wantStops: 1},
		{name: "edit press never starts immediate dictation", event: InputEditPress},
		{name: "edit release never stops immediate dictation", event: InputEditRelease},
		{name: "invalid event is rejected", event: InputEvent(inputEventCount)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &fakeRecorder{stopResult: tt.stopResult}
			stopped := DispatchImmediateInput(recorder, tt.event)

			if recorder.starts != tt.wantStarts {
				t.Fatalf("StartRecording called %d times, want %d", recorder.starts, tt.wantStarts)
			}
			if recorder.stops != tt.wantStops {
				t.Fatalf("StopRecording called %d times, want %d", recorder.stops, tt.wantStops)
			}
			if stopped != tt.wantStopped {
				t.Fatalf("recordingStopped = %t, want %t", stopped, tt.wantStopped)
			}
		})
	}
}
