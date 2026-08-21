package ui

import (
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
	hotkeyOnDown func()
	hotkeyOnUp   func()

	// Factory that builds the right callbacks for a given mode
	// ("push-to-talk" or "toggle"). Set once by the caller via
	// SetHotkeyCallbackFactory before InstallHotkey is called.
	hotkeyCallbackFactory func(mode string) (onDown func(), onUp func())

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
	if m.hotkeyOnDown != nil && m.hotkeyOnUp != nil {
		installOverlayHotkey(m.overlay, m.cfg.Hotkey.Trigger, m.hotkeyOnDown, m.hotkeyOnUp)
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

	// Draw the final state, then hide after the linger.
	m.hideTimer = time.AfterFunc(hideLinger, func() {
		m.hideMu.Lock()
		m.hideTimer = nil
		m.hideMu.Unlock()
		present(m.overlay, model, trayReady)
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

// OnTranscribing implements pipeline.TranscribingNotifier: it keeps the text
// already on screen while the final pass runs, rather than blanking it.
func (m *Manager) OnTranscribing(partial string) {
	m.Present(ViewModel{
		State:      session.StateTranscribing,
		Transcript: partial,
		Partial:    true,
		Status:     "Transcribing",
		Mode:       ViewExpanded,
	})
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

// SetHotkeyCallbackFactory stores a function that builds onDown/onUp callbacks
// for a given mode string ("push-to-talk" or "toggle"). Must be called before
// InstallHotkey.
func (m *Manager) SetHotkeyCallbackFactory(fn func(mode string) (func(), func())) {
	m.hotkeyCallbackFactory = fn
}

// UpdateHotkeyMode switches the active recording mode live, without requiring
// a restart. It rebuilds the callbacks via the factory and reinstalls the hotkey.
func (m *Manager) UpdateHotkeyMode(mode string) {
	if m.hotkeyCallbackFactory == nil {
		return
	}
	onDown, onUp := m.hotkeyCallbackFactory(mode)
	m.hotkeyOnDown = onDown
	m.hotkeyOnUp = onUp
	reinstallOverlayHotkey(m.overlay, m.cfg.Hotkey.Trigger, onDown, onUp)
}

// InstallHotkey registers a platform hotkey tied to the overlay.
// Implemented in app_linux.go / app_darwin.go.
// InstallHotkey records the hotkey callbacks. The grab itself happens in
// Run(), once the platform overlay exists: this is routinely called before
// Run(), which is what silently broke the hotkey when it tried to grab
// against a nil overlay.
func (m *Manager) InstallHotkey(trigger string, onDown, onUp func()) {
	m.hotkeyOnDown = onDown
	m.hotkeyOnUp = onUp
	if m.overlay != nil {
		installOverlayHotkey(m.overlay, trigger, onDown, onUp)
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

// reinstallHotkey unregisters the current hotkey and registers a new one with
// the given trigger string, reusing the original onDown/onUp callbacks.
func (m *Manager) reinstallHotkey(trigger string) {
	if m.hotkeyOnDown == nil || m.hotkeyOnUp == nil {
		return
	}
	reinstallOverlayHotkey(m.overlay, trigger, m.hotkeyOnDown, m.hotkeyOnUp)
}
