package ui

import (
	"fmt"
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

	// themeCallback applies a successfully saved appearance change. Settings
	// and native UI callbacks can arrive on different goroutines.
	themeMu       sync.RWMutex
	themeCallback func(config.Theme)

	// Channels for thread-safe state delivery from pipeline goroutines.
	stateChangeCh chan ViewModel
	rmsCh         chan float32
	quitCh        chan struct{}
	quitOnce      sync.Once

	// hotkeyMu serializes persistence and native replacement. bindings keeps
	// the persisted values and callbacks; effective bindings omit Edit when
	// this process is running the immediate workflow.
	hotkeyMu       sync.Mutex
	bindings       HotkeyBindings
	reviewMode     bool
	replaceHotkeys func(Overlay, HotkeyBindings)

	// Called when the user toggles lowercase output in Settings.
	onLowercaseOutput func(bool)

	// Called when the user toggles LLM cleanup bypass in Settings.
	onSkipLLMCleanup func(bool)

	// Called after the personal dictionary is persisted, so both recognition
	// engines can use it for the next dictation without restarting.
	onDictionary func([]string)

	// fillSource reports how full the recording buffer is, from 0 to 1, and
	// whether a meaningful cap exists. It is sampled on the UI goroutine
	// rather than pushed from the audio callback: reading the fill takes the
	// pipeline lock, and the RMS callback runs on the realtime audio thread,
	// where taking that lock is what stalled capture in sussurro-xvj.36.
	fillSource func() (float64, bool)

	// trayReady reports whether the system tray has appeared. Some desktops
	// never host an SNI item, and there the overlay's right-click menu is the
	// only route to Settings and Quit, so it is shown when no tray registers
	// within trayGracePeriod and stays visible even when idle.
	trayReady atomic.Bool

	// hideTimer defers hiding the overlay so finished text can be read.
	// overlayVisible tracks whether the overlay is currently up, so the linger
	// can distinguish a dictation ending on screen from a hidden overlay that
	// has nothing to linger over. Both are guarded by hideMu.
	hideMu         sync.Mutex
	hideTimer      *time.Timer
	overlayVisible bool

	// hideLingerOverride shortens the linger in tests. Zero means hideLinger.
	hideLingerOverride time.Duration

	// trayGraceOverride shortens the tray grace period in tests. Zero means
	// trayGracePeriod.
	trayGraceOverride time.Duration
}

// NewManager constructs the Manager.  Call Run() to start the event loop.
func NewManager(cfg *config.Config) (*Manager, error) {
	return &Manager{
		cfg:            cfg,
		reviewMode:     cfg.Workflow.ReviewEnabled(),
		replaceHotkeys: reinstallOverlayHotkey,
		stateChangeCh:  make(chan ViewModel, 16),
		rmsCh:          make(chan float32, 256),
		quitCh:         make(chan struct{}),
	}, nil
}

// Run initialises the overlay and settings window, starts the tray, and
// enters the GTK/NSApp main loop.  It blocks until Quit() is called.
func (m *Manager) Run() {
	// 1. Create and theme the platform overlay before it can be shown.
	m.overlay = newOverlay()
	m.configureOverlayTheme()

	// 2. Create the webview settings window (hidden).
	m.settings = newSettingsWindow(m)

	// 3. Apply the hotkey now that the overlay exists.
	//
	// InstallHotkey is called before Run() — it has to be, because Run()
	// blocks in the GTK main loop — so at that point m.overlay is still nil
	// and the X11 grab silently did nothing. Callers only stored the
	// callbacks; this is where they take effect.
	bindings := m.effectiveHotkeyBindings()
	if bindings.PushToTalk != "" || bindings.Toggle != "" || bindings.Edit != "" {
		installOverlayHotkey(m.overlay, bindings)
	}

	// 4. Right-click context menu on the overlay (fallback when tray isn't visible).
	installOverlayContextMenu(m.overlay,
		func() { m.settings.Show() },
		func() { m.Quit() },
	)

	// 5. System tray (runs its own goroutine internally on Linux via DBus).
	go m.runTray()

	// 6. The overlay is created unmapped and stays that way while the tray is
	//    given a chance to appear. Only if it does not does the overlay come
	//    up as the fallback route to Settings and Quit, for desktops that host
	//    no SNI item at all.
	//
	//    Showing it immediately and hiding it on tray registration was the
	//    earlier arrangement, and it flashed the overlay on every startup for
	//    however long the tray took to answer over DBus.
	m.scheduleTrayFallback()

	// 7. Goroutine that forwards state/RMS from pipeline to the overlay.
	go m.processUpdates()

	// 8. Block in the webview / GTK / NSApp main loop.
	m.settings.Run()
}

// hideLinger is how long finished text stays on screen after a dictation
// ends. Hiding the instant the key is released gives the user no chance to
// read what was delivered.
const hideLinger = time.Second

// lingerFor returns the linger this Manager should use.
//
// A variable rather than the constant directly, so tests can shrink it: three
// of them slept against the full one-second value, which was most of the time
// the ui package took to run. Production never sets it and gets hideLinger.
func (m *Manager) lingerFor() time.Duration {
	if m.hideLingerOverride > 0 {
		return m.hideLingerOverride
	}
	return hideLinger
}

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

	// The linger holds a finished dictation on screen long enough to read, so
	// it applies when there is a result to read: either text in this model, or
	// an overlay already up that is now going idle.
	//
	// An idle model with no text and a hidden overlay has nothing to linger
	// over, and taking this path for it meant showing the overlay purely to
	// hide it a second later. That is what flashed the overlay at startup,
	// where markTrayReady renders exactly such a model (sussurro-xvj.62).
	hasResult := model.Transcript != "" || m.overlayVisible
	if model.Visible() || !trayReady || !hasResult {
		m.overlayVisible = model.Visible() || !trayReady
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

	m.hideTimer = time.AfterFunc(m.lingerFor(), func() {
		m.hideMu.Lock()
		m.hideTimer = nil
		m.hideMu.Unlock()

		// Clear the text as it goes, so a later show cannot flash the
		// previous dictation.
		cleared := model
		cleared.Transcript = ""
		m.hideMu.Lock()
		m.overlayVisible = false
		m.hideMu.Unlock()
		present(m.overlay, cleared, trayReady)
	})
	m.hideMu.Unlock()
}

// trayGracePeriod is how long the tray is given to register before the overlay
// comes up as the fallback route to Settings and Quit.
//
// Long enough that a working tray always wins the race, so the overlay is never
// shown on a desktop that has one; short enough that a desktop without one is
// not left unreachable for an uncomfortable stretch.
const trayGracePeriod = 3 * time.Second

// scheduleTrayFallback shows the overlay if the tray has not registered by the
// end of the grace period, and does nothing at all if it has.
func (m *Manager) scheduleTrayFallback() {
	grace := trayGracePeriod
	if m.trayGraceOverride > 0 {
		grace = m.trayGraceOverride
	}
	time.AfterFunc(grace, m.showFallbackIfNoTray)
}

// showFallbackIfNoTray puts the overlay up when no tray has registered, since
// its right-click menu is then the only route to Settings and Quit. A tray
// that did register means the overlay is not needed and stays hidden.
func (m *Manager) showFallbackIfNoTray() {
	if m.trayReady.Load() {
		return
	}
	m.publish(CompactModel(session.StateIdle))
}

// markTrayReady records that the tray is hosting Sussurro, which releases the
// overlay to hide when idle.
func (m *Manager) markTrayReady() {
	if m.trayReady.Swap(true) {
		return
	}
	// The overlay may already be up as the fallback, if the tray took longer
	// than the grace period. Re-render so it comes down now rather than at the
	// next state change.
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

// ToggleSettings shows the settings window, or hides it when it is already
// visible. Safe to call from any goroutine: settingsWindow.Toggle marshals
// onto the UI thread itself.
//
// It toggles rather than only raising because its caller is a key binding, and
// a key that opens a window should close it again. The tray menu keeps using
// Show, where "Settings" should never mean "close settings".
//
// The nil check is not defensive padding. m.settings is created inside Run(),
// so a trigger command arriving between process start and that assignment
// would otherwise dereference nil. Dropping the request is right here — the
// window the user asked for does not exist yet.
func (m *Manager) ToggleSettings() {
	if m.settings == nil {
		return
	}
	m.settings.Toggle()
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
	m.Present(ViewModel{
		State:      state,
		Transcript: partial,
		Partial:    true,
		Status:     compactStatus(state),
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
		// Short enough to sit in the overlay's waveform slot, which is where
		// status words are shown once recording has stopped.
		return "Copied"
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
			// RMS arrives once per audio chunk while recording, which is
			// exactly the cadence the fill bar wants, so it rides along
			// rather than running a timer of its own.
			if m.fillSource != nil {
				if fill, bounded := m.fillSource(); bounded {
					pushBufferFill(m.overlay, fill)
				}
			}

		case <-m.quitCh:
			return
		}
	}
}

// SaveHotkeyBinding persists and applies one binding in a single ordered path.
func (m *Manager) SaveHotkeyBinding(name, trigger string) error {
	m.hotkeyMu.Lock()
	defer m.hotkeyMu.Unlock()

	switch name {
	case "push_to_talk", "toggle", "edit":
	default:
		return fmt.Errorf("unknown hotkey binding %q", name)
	}
	if err := config.SaveHotkeyBinding(m.cfg, name, trigger); err != nil {
		return err
	}
	switch name {
	case "push_to_talk":
		m.cfg.Hotkey.PushToTalk = trigger
		m.bindings.PushToTalk = trigger
	case "toggle":
		m.cfg.Hotkey.Toggle = trigger
		m.bindings.Toggle = trigger
	case "edit":
		m.cfg.Hotkey.Edit = trigger
		m.bindings.Edit = trigger
	}
	m.replaceHotkeyBindingsLocked()
	return nil
}

// UpdateHotkeyBindings changes all bindings live in call order. Tests and
// non-Settings callers use this when replacing a complete snapshot.
func (m *Manager) UpdateHotkeyBindings(pushToTalk, toggle, edit string) {
	m.hotkeyMu.Lock()
	defer m.hotkeyMu.Unlock()
	m.bindings.PushToTalk = pushToTalk
	m.bindings.Toggle = toggle
	m.bindings.Edit = edit
	m.cfg.Hotkey.PushToTalk = pushToTalk
	m.cfg.Hotkey.Toggle = toggle
	m.cfg.Hotkey.Edit = edit
	m.replaceHotkeyBindingsLocked()
}

func (m *Manager) replaceHotkeyBindingsLocked() {
	replace := m.replaceHotkeys
	if replace == nil {
		replace = reinstallOverlayHotkey
	}
	replace(m.overlay, m.effectiveHotkeyBindingsLocked())
}

func (m *Manager) effectiveHotkeyBindings() HotkeyBindings {
	m.hotkeyMu.Lock()
	defer m.hotkeyMu.Unlock()
	return m.effectiveHotkeyBindingsLocked()
}

func (m *Manager) effectiveHotkeyBindingsLocked() HotkeyBindings {
	bindings := m.bindings
	if !m.reviewMode {
		bindings.Edit = ""
	}
	return bindings
}

// InstallHotkey records the bindings and their callbacks. The grab itself
// happens in Run(), once the platform overlay exists: this is routinely
// called before Run(), which is what silently broke the hotkey when it tried
// to grab against a nil overlay.
func (m *Manager) InstallHotkey(bindings HotkeyBindings) {
	m.hotkeyMu.Lock()
	defer m.hotkeyMu.Unlock()
	m.bindings = bindings
	if m.overlay != nil {
		installOverlayHotkey(m.overlay, m.effectiveHotkeyBindingsLocked())
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

// SetDictionaryCallback stores the live-application hook for personal
// vocabulary changed in Settings.
func (m *Manager) SetDictionaryCallback(fn func([]string)) {
	m.onDictionary = fn
}

func (m *Manager) applyDictionary(terms []string) {
	if m.onDictionary != nil {
		m.onDictionary(append([]string(nil), terms...))
	}
}

// SetBufferFillSource stores a function reporting recording-buffer fill from
// 0 to 1, and whether a meaningful cap exists. An unbounded cap reports false
// and the overlay draws no indicator, rather than one pinned at zero.
//
// Must be called before Run().
func (m *Manager) SetBufferFillSource(fn func() (float64, bool)) {
	m.fillSource = fn
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
	m.hotkeyMu.Lock()
	defer m.hotkeyMu.Unlock()
	bindings := m.effectiveHotkeyBindingsLocked()
	if bindings.PushToTalk == "" && bindings.Toggle == "" && bindings.Edit == "" {
		return
	}
	replace := m.replaceHotkeys
	if replace == nil {
		replace = reinstallOverlayHotkey
	}
	replace(m.overlay, bindings)
}
