package ui

import (
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
	// The tray is now a working route to Settings and Quit, so the overlay no
	// longer has to stay up as the fallback.
	m.markTrayReady()

	systray.SetIcon(trayIcon)
	systray.SetTooltip("Sussurro")

	mSettings := systray.AddMenuItem("Open Settings", "Open the settings window")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Exit Sussurro")

	go func() {
		for {
			select {
			case <-mSettings.ClickedCh:
				m.settings.Show()

			case <-mQuit.ClickedCh:
				m.Quit()
				return
			}
		}
	}()
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
