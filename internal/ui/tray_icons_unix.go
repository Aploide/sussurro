//go:build !windows

package ui

import _ "embed"

//go:embed assets/tray.png
var trayIcon []byte

//go:embed assets/tray_rec.png
var trayIconRec []byte
