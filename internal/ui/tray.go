package ui

import (
	"log/slog"
	"runtime"

	"fyne.io/systray"
)

// trayIcon / trayIconRec are embedded per-platform in tray_icons_unix.go
// (PNG) and tray_icons_windows.go (ICO — LoadImageW on Windows only accepts
// real ICO content).

// runTray starts the system tray in the calling goroutine (blocks).
// It must be started with go m.runTray() so it doesn't block the UI thread.
func (m *Manager) runTray() {
	// Windows message queues are per-thread: systray creates its hidden window
	// and pumps GetMessage from this goroutine, so it must stay on one OS
	// thread. Harmless on the DBus (Linux) and Cocoa (macOS) backends.
	runtime.LockOSThread()
	systray.Run(m.onTrayReady, m.onTrayExit)
}

func (m *Manager) onTrayReady() {
	// systray fires onReady before it has registered with any host, so on
	// Linux this runs even where no desktop will ever show the icon. Only a
	// tray that is actually hosted is a working route to Settings and Quit,
	// and only then may the overlay stop standing in as the fallback.
	watchTrayHost(m.onTrayHosted)

	systray.SetIcon(trayIcon)
	systray.SetTooltip("Sussurro")

	mSettings := systray.AddMenuItem("Open Settings", "Open the settings window")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Exit Sussurro")

	go func() {
		for {
			select {
			case <-mSettings.ClickedCh:
				m.showSettings()

			case <-mQuit.ClickedCh:
				m.Quit()
				return
			}
		}
	}()
}

// onTrayHosted records the outcome of the tray host probe. A hosted tray
// releases the overlay to hide when idle; without a host the overlay stays up,
// because its right-click menu is then the only route to Settings and Quit.
func (m *Manager) onTrayHosted(hosted bool) {
	if !hosted {
		slog.Info("No system tray host found; the overlay stays visible as the route to Settings and Quit")
		return
	}
	if m.trayReady.Load() {
		return
	}
	m.markTrayReady()
}

// onTrayExit is called by the systray library when it exits (e.g. the OS
// removes the tray icon). Signal the quit channel so processUpdates and any
// other goroutines waiting on it can drain cleanly.
func (m *Manager) onTrayExit() {
	m.Quit()
}

// updateTrayIcon swaps the tray icon based on recording state.
func (m *Manager) updateTrayIcon(state AppState) {
	if state == StateRecording {
		systray.SetIcon(trayIconRec)
	} else {
		systray.SetIcon(trayIcon)
	}
}
