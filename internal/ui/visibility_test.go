package ui

import (
	"github.com/aploide/sussurro/internal/config"
	"sync"
	"testing"
	"time"

	"github.com/aploide/sussurro/internal/session"
)

// visibilityOverlay records show/hide calls alongside the states it was given.
type visibilityOverlay struct {
	mu      sync.Mutex
	calls   []string
	visible bool
}

func (o *visibilityOverlay) Show() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, "show")
	o.visible = true
}

func (o *visibilityOverlay) Hide() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, "hide")
	o.visible = false
}

func (o *visibilityOverlay) SetState(AppState) {}
func (o *visibilityOverlay) PushRMS(float32)   {}
func (o *visibilityOverlay) Close()            {}

func (o *visibilityOverlay) shown() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.visible
}

func (o *visibilityOverlay) history() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.calls...)
}

func TestOverlayHiddenWhenIdle(t *testing.T) {
	// The capsule is feedback about work in progress, not a permanent fixture.
	if CompactModel(session.StateIdle).Visible() {
		t.Error("Visible() = true when idle, want the overlay hidden")
	}
}

func TestOverlayVisibleWhileWorking(t *testing.T) {
	for _, state := range []AppState{session.StateRecording, session.StateTranscribing} {
		t.Run(state.String(), func(t *testing.T) {
			if !CompactModel(state).Visible() {
				t.Errorf("Visible() = false while %s, want the overlay shown", state)
			}
		})
	}
}

func TestReviewVisibilityFollowsTheSession(t *testing.T) {
	tests := []struct {
		review session.ReviewState
		want   bool
	}{
		{review: session.ReviewIdle, want: false},
		{review: session.ReviewRecording, want: true},
		{review: session.ReviewFinalizing, want: true},
		// Ready is waiting on the user, so hiding it would strand a session
		// with no indication that held text exists.
		{review: session.ReviewReady, want: true},
		{review: session.ReviewEditing, want: true},
		{review: session.ReviewApplyingEdit, want: true},
		{review: session.ReviewDelivering, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.review.String(), func(t *testing.T) {
			model := ReviewModel(tt.review, "text", false)
			if got := model.Visible(); got != tt.want {
				t.Errorf("Visible() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReviewIdleHiddenEvenWithStaleText(t *testing.T) {
	// Returning to idle must not keep the capsule up just because the model
	// still carries the text that was delivered.
	model := ReviewModel(session.ReviewIdle, "delivered text", false)
	if model.Visible() {
		t.Error("Visible() = true for an idle review session, want hidden")
	}
}

func TestPresentShowsAndHidesWithState(t *testing.T) {
	overlay := &visibilityOverlay{}

	present(overlay, CompactModel(session.StateRecording), true)
	if !overlay.shown() {
		t.Fatal("overlay hidden while recording")
	}

	present(overlay, CompactModel(session.StateTranscribing), true)
	if !overlay.shown() {
		t.Fatal("overlay hidden while transcribing")
	}

	present(overlay, CompactModel(session.StateIdle), true)
	if overlay.shown() {
		t.Fatal("overlay still shown after returning to idle")
	}

	want := []string{"show", "show", "hide"}
	got := overlay.history()
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls = %v, want %v", got, want)
		}
	}
}

func TestOverlayStaysUpUntilTheTrayAppears(t *testing.T) {
	overlay := &visibilityOverlay{}

	// The right-click menu is the documented fallback into Settings and Quit,
	// so an idle overlay must not vanish before the tray can take over.
	present(overlay, CompactModel(session.StateIdle), false)
	if !overlay.shown() {
		t.Fatal("overlay hidden while idle with no tray, stranding the fallback")
	}

	present(overlay, CompactModel(session.StateIdle), true)
	if overlay.shown() {
		t.Error("overlay still shown once the tray is available")
	}
	// present() hides immediately; the linger lives in Manager.render.
}

func TestMarkTrayReadyTakesTheFallbackDown(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay, hideLingerOverride: testLinger}

	manager.render(CompactModel(session.StateIdle))
	if !overlay.shown() {
		t.Fatal("overlay hidden before the tray was known")
	}

	manager.markTrayReady()

	// Hiding is deferred by hideLinger so finished text can be read.
	waitFor(t, func() bool { return !overlay.shown() },
		"overlay still shown after the tray appeared")
}

// testLinger is short enough that tests do not spend real seconds asleep, but
// long enough to stay above scheduling jitter on a loaded machine. The three
// tests below waited on the production one-second value, which was most of the
// time this package took to run.
const testLinger = 20 * time.Millisecond

// waitFor polls until condition holds, failing with msg on timeout.
func waitFor(t *testing.T, condition func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error(msg)
}

func TestMarkTrayReadyIsIdempotent(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay, hideLingerOverride: testLinger}

	manager.markTrayReady()
	before := len(overlay.history())
	manager.markTrayReady()

	// A second call must not re-render; the tray only appears once.
	if after := len(overlay.history()); after != before {
		t.Errorf("second markTrayReady produced %d extra calls, want 0", after-before)
	}
}

func TestRecordingShowsEvenBeforeTheTrayAppears(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay, hideLingerOverride: testLinger}

	manager.render(CompactModel(session.StateRecording))
	if !overlay.shown() {
		t.Error("overlay hidden while recording")
	}
}

func TestVisibilityIsRaceFree(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay, hideLingerOverride: testLinger}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); manager.render(CompactModel(session.StateRecording)) }()
		go func() { defer wg.Done(); manager.render(CompactModel(session.StateIdle)) }()
		go func() { defer wg.Done(); manager.markTrayReady() }()
	}
	wg.Wait()
}

func TestOverlayLingersBeforeHiding(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay, hideLingerOverride: testLinger}
	manager.trayReady.Store(true)

	manager.render(CompactModel(session.StateRecording))
	if !overlay.shown() {
		t.Fatal("overlay hidden while recording")
	}

	manager.render(CompactModel(session.StateIdle))

	// Hiding the instant a dictation ends leaves no time to read the result.
	if !overlay.shown() {
		t.Error("overlay hid immediately, want it to linger")
	}

	waitFor(t, func() bool { return !overlay.shown() },
		"overlay never hid after the linger elapsed")
}

func TestNewDictationCancelsAPendingHide(t *testing.T) {
	overlay := &visibilityOverlay{}
	manager := &Manager{overlay: overlay, hideLingerOverride: testLinger}
	manager.trayReady.Store(true)

	manager.render(CompactModel(session.StateIdle))
	manager.render(CompactModel(session.StateRecording))

	// The previous dictation's linger must not hide the new one mid-flow.
	time.Sleep(testLinger + 50*time.Millisecond)
	if !overlay.shown() {
		t.Error("a pending hide fired during a new recording")
	}
}

func TestPhaseKeepsTextOnScreen(t *testing.T) {
	phases := []struct {
		state  session.State
		status string
	}{
		{session.StateTranscribing, "Finalizing"},
		{session.StateCleaningUp, "Cleaning up"},
	}

	for _, phase := range phases {
		t.Run(phase.status, func(t *testing.T) {
			overlay := &presentingOverlay{}
			manager := &Manager{
				stateChangeCh: make(chan ViewModel, 8),
				overlay:       overlay,
			}

			manager.OnPhase(phase.state, "the text so far")

			select {
			case model := <-manager.stateChangeCh:
				// Blanking text the user is reading, only to restore the same
				// words moments later, reads as the app losing their dictation.
				if model.Transcript != "the text so far" {
					t.Errorf("Transcript = %q, want the partial retained", model.Transcript)
				}
				if model.State != phase.state {
					t.Errorf("State = %s, want %s", model.State, phase.state)
				}
				// The label must name the work actually running: the reuse
				// path performs no recognition, so "Transcribing" there is
				// simply false. With text on screen the finalising pass is
				// completing a transcript the user is already reading, which
				// is why it reads "Finalizing" rather than "Transcribing".
				if model.Status != phase.status {
					t.Errorf("Status = %q, want %q", model.Status, phase.status)
				}
				if !model.Visible() {
					t.Errorf("Visible() = false during %s with text", phase.status)
				}
			default:
				t.Fatal("OnPhase queued nothing")
			}
		})
	}
}

func TestFinishedTextIsShownBeforeHiding(t *testing.T) {
	overlay := &presentingOverlay{}
	manager := &Manager{overlay: overlay, hideLingerOverride: testLinger}
	manager.trayReady.Store(true)

	manager.render(ViewModel{
		State:      session.StateIdle,
		Transcript: "the complete transcription",
		Status:     "Done",
		Mode:       ViewExpanded,
	})

	// The finished text must go on screen immediately. Deferring the draw as
	// well as the hide meant the user never saw the last words at all.
	models := overlay.presented()
	if len(models) == 0 {
		t.Fatal("nothing was presented; the finished text was never drawn")
	}
	if models[0].Transcript != "the complete transcription" {
		t.Errorf("first presented model = %q, want the finished text", models[0].Transcript)
	}

	waitFor(t, func() bool {
		shown := overlay.presented()
		return len(shown) > 1 && shown[len(shown)-1].Transcript == ""
	}, "the overlay never cleared the text after the linger")
}

func TestFinishedTextStaysForTheFullLinger(t *testing.T) {
	overlay := &presentingOverlay{}
	manager := &Manager{overlay: overlay, hideLingerOverride: testLinger}
	manager.trayReady.Store(true)

	manager.render(ViewModel{
		State:      session.StateIdle,
		Transcript: "final words",
		Mode:       ViewExpanded,
	})

	// Half a linger in, the text must still be there: the linger is measured
	// from when it was displayed, not from when the key was released.
	time.Sleep(testLinger / 2)
	models := overlay.presented()
	if models[len(models)-1].Transcript != "final words" {
		t.Errorf("text cleared after %v, want it held for the full linger", testLinger/2)
	}
}

func TestOnFinishedCarriesTheText(t *testing.T) {
	manager := &Manager{stateChangeCh: make(chan ViewModel, 4)}

	manager.OnFinished("what was actually transcribed")

	select {
	case model := <-manager.stateChangeCh:
		if model.Transcript != "what was actually transcribed" {
			t.Errorf("Transcript = %q, want the finished text", model.Transcript)
		}
	default:
		t.Fatal("OnFinished queued nothing")
	}
}

func TestBothHotkeyBindingsAreInstalled(t *testing.T) {
	manager := &Manager{}

	// The point of the redesign: a held key and a tapped key at once, which
	// the single-trigger-plus-mode design could not express.
	manager.InstallHotkey(HotkeyBindings{
		PushToTalk: "super+7",
		Toggle:     "super+8",
		Edit:       "super+9",
		OnPress:    func() {},
		OnRelease:  func() {},
		OnToggle:   func() {},
	})

	if manager.bindings.PushToTalk != "super+7" {
		t.Errorf("PushToTalk = %q, want super+7", manager.bindings.PushToTalk)
	}
	if manager.bindings.Toggle != "super+8" {
		t.Errorf("Toggle = %q, want super+8", manager.bindings.Toggle)
	}
	if manager.bindings.Edit != "super+9" {
		t.Errorf("Edit = %q, want super+9", manager.bindings.Edit)
	}
}

func TestImmediateModeKeepsEditBindingPersistedButInactive(t *testing.T) {
	cfg := &config.Config{}
	manager, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	manager.InstallHotkey(HotkeyBindings{Edit: "super+9"})
	if manager.bindings.Edit != "super+9" {
		t.Fatalf("stored Edit = %q, want super+9", manager.bindings.Edit)
	}
	if active := manager.effectiveHotkeyBindings(); active.Edit != "" {
		t.Errorf("active Edit = %q in immediate mode, want empty", active.Edit)
	}
}

func TestReviewModeActivatesPersistedEditBinding(t *testing.T) {
	cfg := &config.Config{}
	cfg.Workflow.Mode = config.ModeReview
	manager, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	manager.InstallHotkey(HotkeyBindings{Edit: "super+9"})
	if active := manager.effectiveHotkeyBindings(); active.Edit != "super+9" {
		t.Errorf("active Edit = %q, want super+9", active.Edit)
	}
}

func TestHotkeyUpdatesReachNativeReplacementInOrder(t *testing.T) {
	cfg := &config.Config{}
	manager, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	seen := make(chan string, 2)
	manager.replaceHotkeys = func(_ Overlay, bindings HotkeyBindings) {
		seen <- bindings.PushToTalk
		if bindings.PushToTalk == "first" {
			close(firstEntered)
			<-releaseFirst
		}
	}

	firstDone := make(chan struct{})
	go func() {
		manager.UpdateHotkeyBindings("first", "", "")
		close(firstDone)
	}()
	<-firstEntered
	if value := <-seen; value != "first" {
		t.Fatalf("first replacement = %q", value)
	}
	secondDone := make(chan struct{})
	go func() {
		manager.UpdateHotkeyBindings("second", "", "")
		close(secondDone)
	}()
	select {
	case value := <-seen:
		t.Fatalf("second replacement overtook first: %q", value)
	default:
	}
	close(releaseFirst)
	<-firstDone
	<-secondDone
	if value := <-seen; value != "second" {
		t.Fatalf("second replacement = %q", value)
	}
	if manager.bindings.PushToTalk != "second" {
		t.Errorf("final binding = %q, want second", manager.bindings.PushToTalk)
	}
}

func TestEitherBindingMayBeEmpty(t *testing.T) {
	for _, tt := range []struct{ name, ptt, toggle, edit string }{
		{name: "toggle only", toggle: "super+8"},
		{name: "push to talk only", ptt: "super+7"},
		{name: "edit only", edit: "super+9"},
		{name: "none"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manager := &Manager{}
			// Must not panic or refuse: an unset binding is valid, since
			// Wayland uses the trigger socket instead.
			manager.InstallHotkey(HotkeyBindings{
				PushToTalk: tt.ptt,
				Toggle:     tt.toggle,
				Edit:       tt.edit,
			})

			if manager.bindings.PushToTalk != tt.ptt {
				t.Errorf("PushToTalk = %q, want %q", manager.bindings.PushToTalk, tt.ptt)
			}
			if manager.bindings.Toggle != tt.toggle {
				t.Errorf("Toggle = %q, want %q", manager.bindings.Toggle, tt.toggle)
			}
			if manager.bindings.Edit != tt.edit {
				t.Errorf("Edit = %q, want %q", manager.bindings.Edit, tt.edit)
			}
		})
	}
}

// TestClipboardOnlyDeliveryIsConfirmed covers sussurro-xvj.44: copying
// without pasting produces no visible effect in the target window, so the
// overlay has to say it happened.
func TestClipboardOnlyDeliveryIsConfirmed(t *testing.T) {
	cfg := &config.Config{}
	cfg.Workflow.Delivery.Backend = config.DeliveryClipboardOnly

	manager := &Manager{
		cfg:           cfg,
		stateChangeCh: make(chan ViewModel, 8),
		overlay:       &presentingOverlay{},
	}

	manager.OnFinished("the quick brown fox")

	select {
	case model := <-manager.stateChangeCh:
		if model.Status != "Copied" {
			t.Errorf("Status = %q, want the copy confirmed", model.Status)
		}
	default:
		t.Fatal("OnFinished queued nothing")
	}
}

// TestPasteDeliveryKeepsTheGenericStatus checks the other delivery methods
// are unchanged: the text appearing in the window is its own confirmation.
func TestPasteDeliveryKeepsTheGenericStatus(t *testing.T) {
	cfg := &config.Config{}
	cfg.Workflow.Delivery.Backend = config.DeliveryClipboardPaste

	manager := &Manager{
		cfg:           cfg,
		stateChangeCh: make(chan ViewModel, 8),
		overlay:       &presentingOverlay{},
	}

	manager.OnFinished("the quick brown fox")

	select {
	case model := <-manager.stateChangeCh:
		if model.Status != "Done" {
			t.Errorf("Status = %q, want the generic completion status", model.Status)
		}
	default:
		t.Fatal("OnFinished queued nothing")
	}
}

// TestCompletionStatusWithoutConfigDoesNotPanic guards the nil-config path
// used by tests that construct a Manager directly.
func TestCompletionStatusWithoutConfigDoesNotPanic(t *testing.T) {
	manager := &Manager{}
	if got := manager.completionStatus(); got != "Done" {
		t.Errorf("completionStatus() = %q with no config, want %q", got, "Done")
	}
}

// TestCleaningUpGetsItsOwnNativeState covers the gap that reopened
// sussurro-xvj.34: the native overlays draw their own label from the state
// they are given, so folding cleanup into the transcribing state made the
// capsule read "transcribing" during work where no recognition runs.
func TestCleaningUpGetsItsOwnNativeState(t *testing.T) {
	transcribing, ok := nativeOverlayState(session.StateTranscribing)
	if !ok {
		t.Fatal("transcribing has no native state")
	}
	cleaning, ok := nativeOverlayState(session.StateCleaningUp)
	if !ok {
		t.Fatal("cleaning up has no native state")
	}
	if transcribing == cleaning {
		t.Error("cleaning up shares the transcribing native state; the capsule " +
			"would draw the transcribing label during cleanup")
	}
}

// TestPostRecordingEntryPointsSayFinalizing covers both notifier routes into
// the immediate-mode UI. A normal release with a partial uses OnPhase; the
// max-duration cap uses OnStateChange. Neither may revive the old label.
func TestPostRecordingEntryPointsSayFinalizing(t *testing.T) {
	tests := []struct {
		name   string
		notify func(*Manager)
	}{
		{"phase with partial", func(manager *Manager) {
			manager.OnPhase(session.StateTranscribing, "text so far")
		}},
		{"phase without partial", func(manager *Manager) {
			manager.OnPhase(session.StateTranscribing, "")
		}},
		{"state change", func(manager *Manager) {
			manager.OnStateChange(session.StateTranscribing)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := &Manager{
				stateChangeCh: make(chan ViewModel, 8),
				overlay:       &presentingOverlay{},
			}
			test.notify(manager)

			select {
			case model := <-manager.stateChangeCh:
				if model.Status != "Finalizing" {
					t.Errorf("Status = %q, want %q", model.Status, "Finalizing")
				}
			default:
				t.Fatal("notification queued nothing")
			}
		})
	}
}
