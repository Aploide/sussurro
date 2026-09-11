//go:build !darwin

package ui

import "fyne.io/systray"

// startTray runs the tray's own event loop on a goroutine of its own.
//
// On Linux systray speaks DBus and on Windows it pumps a hidden window's
// message queue; neither needs — or may have — the thread this process runs
// its UI loop on, and systray.Run blocks for the life of the tray.
func (m *Manager) startTray() {
	go m.runTray()
}

// setTrayIcon pushes an icon to the tray. Template images are a macOS
// convention with no equivalent here: the DBus StatusNotifierItem and
// Shell_NotifyIcon both take the artwork as authored.
func setTrayIcon(icon []byte, _ bool) {
	systray.SetIcon(icon)
}
