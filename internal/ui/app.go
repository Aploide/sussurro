package ui

import (
	"sync"
	"time"

	"github.com/cesp99/sussurro/internal/config"
)

// Manager is the top-level UI controller.
// It implements StateNotifier so the pipeline can call it directly.
type Manager struct {
	cfg      *config.Config
	overlay  Overlay
	settings *settingsWindow

	// Channels for thread-safe state delivery from pipeline goroutines.
	stateChangeCh chan AppState
	rmsCh         chan float32
	quitCh        chan struct{}
	quitOnce      sync.Once

	// Stored hotkey callbacks so the hotkey can be re-registered at runtime.
	hotkeyOnDown func()
	hotkeyOnUp   func()
	hotkeyOnTap  func()
}

// NewManager constructs the Manager.  Call Run() to start the event loop.
func NewManager(cfg *config.Config) (*Manager, error) {
	return &Manager{
		cfg:           cfg,
		stateChangeCh: make(chan AppState, 16),
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

	// 3. Right-click context menu on the overlay (fallback when tray isn't visible).
	installOverlayContextMenu(m.overlay,
		func() { m.settings.Show() },
		func() { m.Quit() },
	)

	// 4. System tray (runs its own goroutine internally on Linux via DBus).
	go m.runTray()

	// 5. Goroutine that forwards state/RMS from pipeline to the overlay.
	go m.processUpdates()

	// 6. Block in the webview / GTK / NSApp main loop.
	m.settings.Run()
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
// The state int maps to AppState: 0=Idle, 1=Recording, 2=Transcribing.
func (m *Manager) OnStateChange(state int) {
	select {
	case m.stateChangeCh <- AppState(state):
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
		case state := <-m.stateChangeCh:
			m.overlay.SetState(state)
			m.updateTrayIcon(state)

		case rms := <-m.rmsCh:
			m.overlay.PushRMS(rms)

		case <-m.quitCh:
			return
		}
	}
}

// InstallHotkey registers a platform hotkey tied to the overlay.
// Implemented in app_linux.go / app_darwin.go.
func (m *Manager) InstallHotkey(trigger string, onDown, onUp func()) {
	m.hotkeyOnDown = onDown
	m.hotkeyOnUp = onUp
	installOverlayHotkey(m.overlay, trigger, onDown, onUp)
}

// InstallToggleHotkey registers a second hotkey that toggles recording on each
// key press (press once to start, press again to stop/transcribe).
func (m *Manager) InstallToggleHotkey(trigger string, onTap func()) {
	m.hotkeyOnTap = onTap
	installOverlayToggleHotkey(m.overlay, trigger, onTap)
}

// reinstallHotkey unregisters the current hotkey and registers a new one with
// the given trigger string, reusing the original onDown/onUp callbacks.
func (m *Manager) reinstallHotkey(trigger string) {
	if m.hotkeyOnDown == nil || m.hotkeyOnUp == nil {
		return
	}
	reinstallOverlayHotkey(m.overlay, trigger, m.hotkeyOnDown, m.hotkeyOnUp)
}
