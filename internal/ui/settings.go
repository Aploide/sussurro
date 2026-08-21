package ui

import (
	_ "embed"
	"fmt"
	"math"
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
	applySettingsSize(w)

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

// Content requirements of the settings page, in CSS pixels. Both were
// measured from the rendered page rather than chosen by eye: the width is the
// point below which controls overflow their rows, and the height is the
// tallest tab panel. See sussurro-xvj.33.
const (
	settingsContentWidth  = 793
	settingsContentHeight = 408
	// settingsChrome covers the tab strip, window padding, and title bar,
	// which sit outside the measured panel height.
	settingsChrome = 120

	// maxSettingsWidth and maxSettingsHeight keep the window on a small
	// laptop display (1366x768) even at high scaling, where the scaled
	// requirement would otherwise exceed the screen.
	maxSettingsWidth  = 1300
	maxSettingsHeight = 740
)

// applySettingsSize sizes the settings window so its CSS viewport matches what
// the content needs on this display.
//
// webview sizes in device pixels, but the page lays out in CSS pixels, and
// fractional display scaling divides one into the other. Sizing by a fixed
// device-pixel number therefore under-sizes the content on any scaled display:
// at 1.33 scaling the previous 820px window gave the page only 616 CSS px,
// cropping controls that need 793.
//
// HintMin additionally prevents the user shrinking the window below the width
// its controls need, which is the state the cropping bug reported.
func applySettingsSize(w webview.WebView) {
	scale := windowScale()
	if scale < 1 {
		// A sub-1 scale would shrink the window below the content's needs.
		scale = 1
	}

	width := scaleDimension(settingsContentWidth, scale, maxSettingsWidth)
	height := scaleDimension(settingsContentHeight+settingsChrome, scale, maxSettingsHeight)

	w.SetSize(width, height, webview.HintMin)
	w.SetSize(width, height, webview.HintNone)
}

// scaleDimension converts a CSS-pixel requirement into device pixels, capped
// so the window still fits a small display.
//
// It rounds up: rounding to nearest can land a pixel short of the requirement
// once the browser divides the size back down, which is enough to start
// cropping a control that fits exactly.
func scaleDimension(css int, scale float64, max int) int {
	scaled := int(math.Ceil(float64(css) * scale))
	if scaled > max {
		return max
	}
	return scaled
}
