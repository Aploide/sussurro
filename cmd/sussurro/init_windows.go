//go:build windows

package main

import "golang.org/x/sys/windows"

// WebView2 (the settings window) requires the UI thread to be a
// single-threaded COM apartment, but the audio stack (miniaudio via malgo)
// initializes COM as multithreaded on whatever thread creates the capture
// engine — the same main thread, before the UI exists. If the multithreaded
// apartment wins, webview.New never completes. Claim STA first; miniaudio
// tolerates the already-initialized apartment.
func init() {
	_ = windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)
}
