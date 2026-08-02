//go:build linux

package ui

import "os"

// WebKitGTK's DMABUF renderer does not work on every driver/compositor pair.
// On the NVIDIA proprietary driver under Wayland it takes the whole GDK
// connection down the moment the first frame is composited:
//
//	Gdk-Message: Error 71 (Protocol error) dispatching to Wayland display.
//
// The page itself has already loaded by then, so the symptom is a settings
// window that opens as an empty grey frame with no content. Under X11 the same
// backend fails less dramatically ("Failed to create GBM buffer ... Invalid
// argument") and falls back on its own.
//
// The settings page is a static form, so nothing is lost by using the plain
// renderer. This must run before the first webview is created — hence init()
// rather than a call inside newSettingsWindow. An explicit value from the
// environment always wins, so the accelerated path can still be forced with
// WEBKIT_DISABLE_DMABUF_RENDERER=0.
func init() {
	if _, set := os.LookupEnv("WEBKIT_DISABLE_DMABUF_RENDERER"); !set {
		os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
	}
}
