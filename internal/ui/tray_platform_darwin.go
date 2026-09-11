//go:build darwin

package ui

import "fyne.io/systray"

// startTray puts the status item in the menu bar.
//
// systray.Run cannot do this on macOS. Its darwin backend calls [NSApp run]
// itself, from whatever goroutine invoked it, and AppKit requires that call on
// the process's main thread. It was invoked from `go m.runTray()`, so the loop
// belonged to a thread AppKit would not accept — and this process already owns
// an NSApp loop anyway, the webview's, which a second one cannot share. The
// result was a tray that registered its callbacks and never appeared.
//
// RunWithExternalLoop registers the same callbacks but, instead of taking over
// the application, hands back a start function that delivers the launch
// notification to systray's own delegate. The webview's run loop then pumps
// the status item like any other AppKit object. start() touches NSStatusBar,
// so it has to run on the main thread: Manager.Run is called there (main()
// locks the OS thread before run()), which is why this is a plain call rather
// than a goroutine.
//
// The returned stop function is deliberately dropped. It tears the item down
// and calls systray.Quit, and the only path that ends this process is
// Manager.Quit -> platformExit, which _exit()s with the status item owned by a
// process that is about to stop existing.
func (m *Manager) startTray() {
	start, _ := systray.RunWithExternalLoop(m.onTrayReady, m.onTrayExit)
	start()
}

// setTrayIcon pushes an icon to the status item.
//
// The idle icon is a solid white glyph. Pushed plain it is drawn exactly as
// authored, which makes it invisible on a light menu bar — and the menu bar's
// appearance follows the desktop behind it, not only the Dark Mode setting, so
// "it looks fine here" proves nothing. A template image is the macOS
// convention: AppKit reads only the alpha channel and tints the shape to the
// bar's own foreground colour, so one asset reads in both appearances.
//
// The recording icon is deliberately red and carries its meaning in that
// colour, so it is pushed plain — a template would tint it to the same shade
// as idle and erase the distinction.
func setTrayIcon(icon []byte, asTemplate bool) {
	if asTemplate {
		systray.SetTemplateIcon(icon, icon)
		return
	}
	systray.SetIcon(icon)
}
