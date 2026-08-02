//go:build windows

package ui

import (
	"os"

	"fyne.io/systray"
)

// platformExit terminates the process. systray.Quit() removes the notification
// area icon first; without it Windows leaves a ghost tray icon behind until
// the user hovers over it.
func platformExit() {
	systray.Quit()
	os.Exit(0)
}
