package ui

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aploide/sussurro/internal/config"
	"github.com/aploide/sussurro/internal/session"
)

// Manager is the top-level UI controller.
// It implements StateNotifier so the pipeline can call it directly.
type Manager struct {
	cfg      *config.Config
	overlay  Overlay
	settings *settingsWindow

	// Channels for thread-safe state delivery from pipeline goroutines.
	stateChangeCh chan ViewModel
	rmsCh         chan float32
	quitCh        chan struct{}
	quitOnce      sync.Once

	// Stored hotkey callbacks so the hotkey can be re-registered at runtime.
	// bindings holds the configured hotkeys and their callbacks.
	bindings HotkeyBindings

	// Called when the user toggles lowercase output in Settings.
	onLowercaseOutput func(bool)

	// Called when the user toggles LLM cleanup bypass in Settings.
	onSkipLLMCleanup func(bool)

	// trayReady reports whether the system tray has appeared. Until it does,
	// the overlay stays visible even when idle: its right-click menu is the
	// documented fallback route to Settings and Quit, and some desktops never
	// host an SNI item at all. Hiding it there would leave no way in.
	trayReady atomic.Bool

	// hideTimer defers hiding the overlay so finished text can be read.
	hideMu    sync.Mutex
	hideTimer *time.Timer
}

// NewManager constructs the Manager.  Call Run() to start the event loop.
func NewManager(cfg *config.Config) (*Manager, error) {
	return &Manager{
		cfg:           cfg,
		stateChangeCh: make(chan ViewModel, 16),
		rmsCh:         make(chan float32, 256),
		quitCh:        make(chan struct{}),
	}, nil
}

// Run initialises the overlay and settings window, starts the tray, and
// enters the GTK/NSApp main loop.  It blocks until Quit() is called.
func (m *Manager) Run() {
	// 1. Create the platform overlay (GTK3 on Linux, NSPanel on macOS).
	m.overlay = newOverlay()

	// 2. Create the webview settings window (hidden).
	m.settings = newSettingsWindow(m)

	// 3. Apply the hotkey now that the overlay exists.
	//
	// InstallHotkey is called before Run() — it has to be, because Run()
	// blocks in the GTK main loop — so at that point m.overlay is still nil
	// and the X11 grab silently did nothing. Callers only stored the
	// callbacks; this is where they take effect.
	if m.bindings.PushToTalk != "" || m.bindings.Toggle != "" {
		installOverlayHotkey(m.overlay, m.bindings)
	}

	// 4. Right-click context menu on the overlay (fallback when tray isn't visible).
	installOverlayContextMenu(m.overlay,
		func() { m.settings.Show() },
		func() { m.Quit() },
	)

	// 5. The overlay is created unmapped. Show it until the tray confirms
	//    itself, so a desktop that never hosts an SNI item still has the
	//    right-click menu as a way into Settings and Quit.
	m.render(CompactModel(session.StateIdle))

	// 6. System tray (runs its own goroutine internally on Linux via DBus).
	go m.runTray()

	// 7. Goroutine that forwards state/RMS from pipeline to the overlay.
	go m.processUpdates()

	// 8. Block in the webview / GTK / NSApp main loop.
	m.settings.Run()
}

// hideLinger is how long finished text stays on screen after a dictation
// ends. Hiding the instant the key is released gives the user no chance to
// read what was delivered.
const hideLinger = time.Second

// render draws a model, keeping the overlay on screen while the tray has not
// appeared so its context menu stays reachable.
//
// A model that would hide the overlay is deferred by hideLinger, and
// cancelled if anything else arrives first, so a new dictation is never
// delayed by the previous one's linger.
func (m *Manager) render(model ViewModel) {
	trayReady := m.trayReady.Load()

	m.hideMu.Lock()
	if m.hideTimer != nil {
		m.hideTimer.Stop()
		m.hideTimer = nil
	}

	if model.Visible() || !trayReady {
		m.hideMu.Unlock()
		present(m.overlay, model, trayReady)
		return
	}

	// Draw the finished state and keep it on screen for the linger, then hide.
	//
	// present() hides whenever the model is not Visible(), and a finished
	// dictation is StateIdle, so the draw has to bypass that decision — going
	// through present() here would hide instantly, which is precisely the
	// defect this path exists to fix.
	//
	// The linger runs from this moment, when the text goes on screen, not from
	// the key release: the final pass runs in between and would otherwise
	// consume most of the second.
	if presenter, ok := m.overlay.(Presenter); ok {
		presenter.Present(model)
	} else {
		m.overlay.SetState(model.State)
	}
	m.overlay.Show()

	m.hideTimer = time.AfterFunc(hideLinger, func() {
		m.hideMu.Lock()
		m.hideTimer = nil
		m.hideMu.Unlock()

		// Clear the text as it goes, so a later show cannot flash the
		// previous dictation.
		cleared := model
		cleared.Transcript = ""
		present(m.overlay, cleared, trayReady)
	})
	m.hideMu.Unlock()
}

// markTrayReady records that the tray is hosting Sussurro, which releases the
// overlay to hide when idle.
func (m *Manager) markTrayReady() {
	if m.trayReady.Swap(true) {
		return
	}
	// The overlay is showing only as a fallback, so take it down now rather
	// than waiting for the next state change.
	m.render(CompactModel(session.StateIdle))
}

// Quit terminates the application. Safe to call from any goroutine or
// GTK callback; idempotent via sync.Once.
func (m *Manager) Quit() {
	m.quitOnce.Do(func() {
		close(m.quitCh)
		// Exit after a brief window so in-flight GTK events can drain.
		// os.Exit is used instead of gtk_main_quit() to avoid issues with
		// GTK popup-menu nested event loops swallowing the quit signal.
		go func() {
			time.Sleep(100 * time.Millisecond)
			platformExit()
		}()
	})
}

// --- StateNotifier implementation (compatible with pipeline.StateNotifier) ---

// OnStateChange is called by the pipeline from its own goroutine.
func (m *Manager) OnStateChange(state AppState) {
	if !state.Valid() {
		return
	}
	m.publish(CompactModel(state))
}

// OnPhase implements pipeline.TranscribingNotifier: it keeps the text already
// on screen while post-recording work runs, rather than blanking it, and
// labels the phase that is actually running.
func (m *Manager) OnPhase(state session.State, partial string) {
	// A non-empty partial means live text is already on screen, which is
	// exactly when "Finalizing" is the truthful word rather than
	// "Transcribing". Deriving it from the text itself rather than the
	// streaming config keeps the label matched to what the user can see, even
	// when streaming is on but produced nothing.
	streaming := strings.TrimSpace(partial) != ""

	m.Present(ViewModel{
		State:      state,
		Transcript: partial,
		Partial:    true,
		Status:     compactStatus(state, streaming),
		Mode:       ViewExpanded,
	})
}

// OnFinished implements pipeline.TranscribingNotifier: it shows the completed
// transcription so the user can read what was produced. render() displays it,
// then hides the overlay a second later.
func (m *Manager) OnFinished(text string) {
	m.Present(ViewModel{
		State:      session.StateIdle,
		Transcript: text,
		Status:     m.completionStatus(),
		Mode:       ViewExpanded,
	})
}

// completionStatus describes what happened to the finished text.
//
// Pasting is self-evident: the words appear in the window the user was
// typing into. Copying without pasting is not, so it says so explicitly
// rather than leaving the user unsure whether anything was delivered.
func (m *Manager) completionStatus() string {
	if m.cfg != nil && m.cfg.Workflow.ClipboardOnlyDelivery() {
		return "Copied to clipboard!"
	}
	return "Done"
}

// Present queues an already-built view model for display. Review-mode
// adapters call this from their own goroutines.
func (m *Manager) Present(model ViewModel) {
	if !model.State.Valid() || !model.Mode.Valid() {
		return
	}
	m.publish(model)
}

// publish queues a model without blocking the caller. Dropping an update
// under pressure is correct: each model is a complete snapshot, so the next
// one supersedes anything lost.
func (m *Manager) publish(model ViewModel) {
	select {
	case m.stateChangeCh <- model:
	default: // drop if channel full (non-blocking)
	}
}

// OnRMSData is called by the audio capture loop from its own goroutine.
func (m *Manager) OnRMSData(rms float32) {
	select {
	case m.rmsCh <- rms:
	default:
	}
}

// processUpdates relays state/RMS messages to the overlay thread-safely.
func (m *Manager) processUpdates() {
	for {
		select {
		case model := <-m.stateChangeCh:
			m.render(model)
			m.updateTrayIcon(model.State)

		case rms := <-m.rmsCh:
			m.overlay.PushRMS(rms)

		case <-m.quitCh:
			return
		}
	}
}

// UpdateHotkeyBindings changes the bindings live, without a restart, keeping
// the callbacks already registered.
func (m *Manager) UpdateHotkeyBindings(pushToTalk, toggle string) {
	m.bindings.PushToTalk = pushToTalk
	m.bindings.Toggle = toggle
	m.cfg.Hotkey.PushToTalk = pushToTalk
	m.cfg.Hotkey.Toggle = toggle
	reinstallOverlayHotkey(m.overlay, m.bindings)
}

// InstallHotkey records the bindings and their callbacks. The grab itself
// happens in Run(), once the platform overlay exists: this is routinely
// called before Run(), which is what silently broke the hotkey when it tried
// to grab against a nil overlay.
func (m *Manager) InstallHotkey(bindings HotkeyBindings) {
	m.bindings = bindings
	if m.overlay != nil {
		installOverlayHotkey(m.overlay, bindings)
	}
}

// SetLowercaseOutputCallback stores a function that is called whenever the user
// toggles the lowercase output setting in the Settings window.
func (m *Manager) SetLowercaseOutputCallback(fn func(bool)) {
	m.onLowercaseOutput = fn
}

// applyLowercaseOutput forwards the new value to the registered callback (if any).
func (m *Manager) applyLowercaseOutput(v bool) {
	if m.onLowercaseOutput != nil {
		m.onLowercaseOutput(v)
	}
}

// SetSkipLLMCleanupCallback stores a function that is called whenever the user
// toggles the raw output setting in the Settings window.
func (m *Manager) SetSkipLLMCleanupCallback(fn func(bool)) {
	m.onSkipLLMCleanup = fn
}

// applySkipLLMCleanup forwards the new value to the registered callback (if any).
func (m *Manager) applySkipLLMCleanup(v bool) {
	if m.onSkipLLMCleanup != nil {
		m.onSkipLLMCleanup(v)
	}
}

// reinstallHotkey re-registers the current bindings.
func (m *Manager) reinstallHotkey() {
	if m.bindings.PushToTalk == "" && m.bindings.Toggle == "" {
		return
	}
	reinstallOverlayHotkey(m.overlay, m.bindings)
}
