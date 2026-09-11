//go:build windows

package ui

import _ "embed"

// Windows requires real ICO bytes: systray writes the icon to a temp file and
// loads it with LoadImageW(IMAGE_ICON, LR_LOADFROMFILE), which rejects PNG.
// The .ico files are generated from the PNG sources (16/20/24/32 px).

//go:embed assets/tray.ico
var trayIcon []byte

//go:embed assets/tray_rec.ico
var trayIconRec []byte
