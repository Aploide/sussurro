package ui

import (
	_ "embed"
	"fmt"
	"strings"
	"unsafe"

	webview "github.com/webview/webview_go"
)

//go:embed assets/index.html
var settingsHTMLTemplate string

//go:embed assets/style.css
var settingsCSS string

//go:embed assets/app.js
var settingsJS string

// settingsHTML is the fully assembled single-file HTML page.
var settingsHTML string

func init() {
	h := strings.ReplaceAll(settingsHTMLTemplate, "{{CSS}}", settingsCSS)
	h = strings.ReplaceAll(h, "{{JS}}", settingsJS)
	settingsHTML = h
}

// settingsWindow wraps the webview settings window.
type settingsWindow struct {
	w   webview.WebView
	mgr *Manager
}

// newSettingsWindow creates the webview window (hidden until Show() is called).
func newSettingsWindow(mgr *Manager) *settingsWindow {
	w := webview.New(false)
	w.SetTitle("Sussurro Settings")
	// Sized in device pixels: on a 1.33-scaled display 580 became only 435
	// CSS px, narrower than the layout is built for. Wider keeps the content
	// comfortable once the webview applies display scaling.
	w.SetSize(820, 780, webview.HintNone)

	sw := &settingsWindow{w: w, mgr: mgr}

	// Bind Go functions accessible from JavaScript
	bindBridge(sw)

	// Load the settings HTML
	w.SetHtml(settingsHTML)

	// Hide immediately (before the event loop starts; webview.New shows by default).
	// Safe to call from the main goroutine before w.Run().
	hideWebviewWindow(unsafe.Pointer(w.Window()))

	// Intercept the WM "X" button: hide instead of destroy so the window
	// can be reopened without recreating it.
	interceptSettingsClose(unsafe.Pointer(w.Window()))

	return sw
}

// Show presents the settings window and refreshes its data.
func (sw *settingsWindow) Show() {
	sw.w.Dispatch(func() {
		showWebviewWindow(unsafe.Pointer(sw.w.Window()))
		sw.w.Eval("reloadSettings()")
	})
}

// Hide conceals the settings window.
func (sw *settingsWindow) Hide() {
	sw.w.Dispatch(func() {
		hideWebviewWindow(unsafe.Pointer(sw.w.Window()))
	})
}

// pushDownloadProgress pushes a download progress update to the JS layer.
func (sw *settingsWindow) pushDownloadProgress(name string, pct float64) {
	sw.w.Dispatch(func() {
		sw.w.Eval(fmt.Sprintf("onDownloadProgress('%s', %f)", name, pct))
	})
}

// Run starts the webview event loop (blocks until Terminate is called).
func (sw *settingsWindow) Run() {
	sw.w.Run()
}

// Terminate stops the webview event loop.
func (sw *settingsWindow) Terminate() {
	sw.w.Terminate()
}
