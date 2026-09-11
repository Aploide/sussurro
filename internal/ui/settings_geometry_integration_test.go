//go:build linux && settings_geometry

package ui

import (
	"encoding/json"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	webview "github.com/webview/webview_go"
)

type renderedSettingsGeometry struct {
	ViewportWidth       int                     `json:"viewportWidth"`
	DocumentScrollWidth int                     `json:"documentScrollWidth"`
	ContentScrollWidth  int                     `json:"contentScrollWidth"`
	ContentClientWidth  int                     `json:"contentClientWidth"`
	ModelCount          int                     `json:"modelCount"`
	Panels              []renderedPanelGeometry `json:"panels"`
}

type renderedPanelGeometry struct {
	Name             string `json:"name"`
	VisibleHeight    int    `json:"visibleHeight"`
	NaturalHeight    int    `json:"naturalHeight"`
	ScrollWidth      int    `json:"scrollWidth"`
	ClientWidth      int    `json:"clientWidth"`
	RequestedWidth   int    `json:"requestedWidth"`
	RequestedHeight  int    `json:"requestedHeight"`
	ViewportHeight   int    `json:"viewportHeight"`
	HiddenPanelsZero bool   `json:"hiddenPanelsZero"`
}

type renderedMutedStyle struct {
	Theme    string `json:"theme"`
	Token    string `json:"token"`
	Computed string `json:"computed"`
	Error    string `json:"error"`
}

const mutedStyleProbeScript = `<script>
(async () => {
  for (let attempt = 0; attempt < 200; attempt++) {
    const sample = document.querySelector(".model-desc");
    const root = document.documentElement;
    if (root.dataset.theme === "dark" && sample) {
      reportMutedStyle(JSON.stringify({
        theme: root.dataset.theme,
        token: getComputedStyle(root).getPropertyValue("--muted").trim(),
        computed: getComputedStyle(sample).color,
      }));
      return;
    }
    await new Promise(resolve => setTimeout(resolve, 10));
  }
  reportMutedStyle(JSON.stringify({error: "dark theme did not finish rendering"}));
})();
</script>`

func TestRenderedSettingsGeometryReportsEveryTab(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered WebKit integration test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w := webview.New(false)
	if w == nil {
		t.Fatal("webview.New returned nil")
	}
	defer w.Destroy()
	w.SetTitle("Sussurro Settings geometry test")
	applySettingsSize(w)

	initial := representativeSettingsData(t)
	if err := w.Bind("getInitialData", func() string { return initial }); err != nil {
		t.Fatal(err)
	}
	sw := &settingsWindow{w: w}
	if err := w.Bind("resizeSettingsWindow", func(width, height int) int {
		sw.resizeToContent(width, height)
		return expectedSettingsViewportHeight(width, height)
	}); err != nil {
		t.Fatal(err)
	}

	result := make(chan string, 1)
	if err := w.Bind("reportSettingsGeometry", func(payload string) {
		result <- payload
		w.Terminate()
	}); err != nil {
		t.Fatal(err)
	}

	page := strings.Replace(settingsHTML, "</body>", geometryProbeScript+"</body>", 1)
	w.SetHtml(page)
	watchdogDone := make(chan struct{})
	timer := time.AfterFunc(15*time.Second, func() {
		w.Terminate()
		close(watchdogDone)
	})
	w.Run()
	if !timer.Stop() {
		<-watchdogDone
	}

	select {
	case payload := <-result:
		assertRenderedSettingsGeometry(t, payload)
	default:
		t.Fatal("timed out waiting for rendered Settings geometry")
	}
}

func TestRenderedSettingsUsesDarkMutedStyle(t *testing.T) {
	if testing.Short() {
		t.Skip("rendered WebKit integration test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w := webview.New(false)
	if w == nil {
		t.Fatal("webview.New returned nil")
	}
	defer w.Destroy()
	w.SetTitle("Sussurro Settings muted-style test")
	applySettingsSize(w)

	initial := representativeSettingsData(t)
	if err := w.Bind("getInitialData", func() string { return initial }); err != nil {
		t.Fatal(err)
	}
	sw := &settingsWindow{w: w}
	if err := w.Bind("resizeSettingsWindow", func(width, height int) int {
		sw.resizeToContent(width, height)
		return expectedSettingsViewportHeight(width, height)
	}); err != nil {
		t.Fatal(err)
	}

	result := make(chan string, 1)
	if err := w.Bind("reportMutedStyle", func(payload string) {
		result <- payload
		w.Terminate()
	}); err != nil {
		t.Fatal(err)
	}

	page := strings.Replace(settingsHTML, "</body>", mutedStyleProbeScript+"</body>", 1)
	w.SetHtml(page)
	watchdogDone := make(chan struct{})
	timer := time.AfterFunc(15*time.Second, func() {
		w.Terminate()
		close(watchdogDone)
	})
	w.Run()
	if !timer.Stop() {
		<-watchdogDone
	}

	select {
	case payload := <-result:
		var style renderedMutedStyle
		if err := json.Unmarshal([]byte(payload), &style); err != nil {
			t.Fatalf("decode rendered muted style: %v", err)
		}
		if style.Error != "" {
			t.Fatal(style.Error)
		}
		if style.Theme != "dark" {
			t.Errorf("rendered theme = %q, want dark", style.Theme)
		}
		if style.Token != "#a0a0a8" {
			t.Errorf("rendered --muted = %q, want #a0a0a8", style.Token)
		}
		if style.Computed != "rgb(160, 160, 168)" {
			t.Errorf("rendered muted text = %q, want rgb(160, 160, 168)", style.Computed)
		}
	default:
		t.Fatal("timed out waiting for rendered muted style")
	}
}

func representativeSettingsData(t *testing.T) string {
	t.Helper()
	data := map[string]any{
		"platform": "linux", "version": "integration", "theme": "dark",
		"pushToTalkHotkey": "RIGHTALT", "toggleHotkey": "", "isWayland": false,
		"language": "en", "lowercaseOutput": false, "skipLLMCleanup": false,
		"dictionary": []string{"Sussurro", "Kubernetes", "PostgreSQL"},
		"models": []map[string]any{
			{"id": "small", "name": "Small", "desc": "Fast English recognition model", "size": "466 MB", "installed": true, "active": true, "downloadable": true, "selectable": true, "type": "whisper"},
			{"id": "medium", "name": "Medium", "desc": "More accurate multilingual recognition model", "size": "1.5 GB", "installed": true, "active": false, "downloadable": true, "selectable": true, "type": "whisper"},
			{"id": "large", "name": "Large v3 Turbo", "desc": "Highest accuracy recognition model", "size": "1.6 GB", "installed": false, "active": false, "downloadable": true, "selectable": false, "type": "whisper"},
			{"id": "qwen", "name": "Qwen cleanup", "desc": "Local transcription cleanup model", "size": "1.3 GB", "installed": true, "active": true, "downloadable": true, "selectable": true, "type": "llm"},
		},
		"workflow": map[string]any{
			"mode": "review", "streamingEnabled": true, "streamingInterval": "750ms",
			"inputBackend": "auto", "inputDevice": "/dev/input/event0", "inputChord": "RIGHTALT",
			"inputCancelChord": "ESC", "deliveryBackend": "auto", "voiceEditing": true,
			"modes":            []map[string]any{{"value": "immediate", "label": "Immediate", "available": true}, {"value": "review", "label": "Review", "available": true}},
			"inputBackends":    []map[string]any{{"value": "auto", "label": "Automatic", "available": true}, {"value": "native", "label": "Native hotkey", "available": true}, {"value": "trigger", "label": "Compositor trigger", "available": true}, {"value": "evdev", "label": "evdev", "available": true}},
			"deliveryBackends": []map[string]any{{"value": "auto", "label": "Automatic", "available": true}, {"value": "clipboard", "label": "Clipboard", "available": true}, {"value": "wtype", "label": "wtype", "available": true}},
		},
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func expectedSettingsViewportHeight(cssWidth, cssHeight int) int {
	scale := windowScale()
	if scale < 1 {
		scale = 1
	}
	_, deviceHeight := settingsSizeForContent(cssWidth, cssHeight, scale)
	return int(math.Floor(float64(deviceHeight) / scale))
}

func assertRenderedSettingsGeometry(t *testing.T, payload string) {
	t.Helper()
	var geometry renderedSettingsGeometry
	if err := json.Unmarshal([]byte(payload), &geometry); err != nil {
		t.Fatalf("decode geometry %q: %v", payload, err)
	}
	if geometry.ModelCount != 4 {
		t.Fatalf("rendered %d models, want representative bridge content with 4", geometry.ModelCount)
	}
	if geometry.ViewportWidth < settingsContentWidth {
		t.Errorf("CSS viewport width = %d, want at least %d", geometry.ViewportWidth, settingsContentWidth)
	}
	if geometry.DocumentScrollWidth > geometry.ViewportWidth+1 {
		t.Errorf("document overflows horizontally: scroll=%d client=%d", geometry.DocumentScrollWidth, geometry.ViewportWidth)
	}
	if geometry.ContentScrollWidth > geometry.ContentClientWidth+1 {
		t.Errorf("content overflows horizontally: scroll=%d client=%d", geometry.ContentScrollWidth, geometry.ContentClientWidth)
	}
	wantPanels := map[string]bool{
		"models": false, "workflow": false, "output": false,
		"input": false, "appearance": false,
	}
	if len(geometry.Panels) != len(wantPanels) {
		t.Fatalf("measured %d tab panels, want %d", len(geometry.Panels), len(wantPanels))
	}
	for _, panel := range geometry.Panels {
		if _, ok := wantPanels[panel.Name]; !ok {
			t.Errorf("measured unexpected panel %q", panel.Name)
		} else {
			wantPanels[panel.Name] = true
		}
		if panel.VisibleHeight <= 0 {
			t.Errorf("panel %q was measured while hidden", panel.Name)
		}
		if !panel.HiddenPanelsZero {
			t.Errorf("panel %q left another tab visible during measurement", panel.Name)
		}
		if panel.RequestedWidth != geometry.ViewportWidth {
			t.Errorf("panel %q requested width %d for viewport %d", panel.Name, panel.RequestedWidth, geometry.ViewportWidth)
		}
		if panel.RequestedHeight != panel.NaturalHeight {
			t.Errorf("panel %q measured %d CSS px but requested %d", panel.Name, panel.NaturalHeight, panel.RequestedHeight)
		}
		scale := windowScale()
		if scale < 1 {
			scale = 1
		}
		_, deviceHeight := settingsSizeForContent(
			panel.RequestedWidth, panel.RequestedHeight, scale,
		)
		uncappedCSSHeight := panel.RequestedHeight
		if uncappedCSSHeight < settingsMinContentHeight {
			uncappedCSSHeight = settingsMinContentHeight
		}
		// A panel may only be cropped when the physical screen cannot hold
		// it. Excusing a crop whenever the configured cap was exceeded made
		// this guard blind to the bug it should have caught, because a cap
		// set below the screen size is itself the cause: the Dictation tab
		// lost 105 CSS px on a 1920px-tall display and this still passed.
		uncappedDeviceHeight := int(math.Ceil(float64(uncappedCSSHeight)*scale)) + settingsChrome
		_, screenHeight := workAreaSize()
		if screenHeight <= 0 {
			// Headless: no screen to defer to, so nothing may be cropped.
			screenHeight = uncappedDeviceHeight
		}
		if uncappedDeviceHeight <= screenHeight && panel.ViewportHeight+2 < panel.NaturalHeight {
			t.Errorf("panel %q needs %d CSS px (%d device) but viewport is %d, and the %d px screen could have held it",
				panel.Name, panel.NaturalHeight, uncappedDeviceHeight,
				panel.ViewportHeight, screenHeight)
		}
		expectedViewportHeight := expectedSettingsViewportHeight(
			panel.RequestedWidth, panel.RequestedHeight,
		)
		// Native frame metrics and fractional WebKit scaling can differ by a
		// pixel from the nominal device-to-CSS conversion.
		if delta := panel.ViewportHeight - expectedViewportHeight; delta < -2 || delta > 2 {
			t.Errorf("panel %q resized to %d CSS px, want %d (device height %d)", panel.Name, panel.ViewportHeight, expectedViewportHeight, deviceHeight)
		}
		// WebKit can round a fractional CSS border into one extra scroll pixel.
		if panel.ScrollWidth > panel.ClientWidth+1 {
			t.Errorf("panel %q overflows horizontally: scroll=%d client=%d", panel.Name, panel.ScrollWidth, panel.ClientWidth)
		}
	}
	for name, measured := range wantPanels {
		if !measured {
			t.Errorf("panel %q was not measured", name)
		}
	}
}

const geometryProbeScript = `<script>
(() => {
  const frame = () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  let latestRequest = null;
  let latestResize = null;
  const resizeSettingsWindow = window.resizeSettingsWindow;
  window.resizeSettingsWindow = (width, height) => {
    latestRequest = {width, height};
    latestResize = Promise.resolve(resizeSettingsWindow(width, height));
    return latestResize;
  };
  async function waitForResizeRequest() {
    const deadline = performance.now() + 2000;
    while (!latestRequest || !latestResize) {
      if (performance.now() >= deadline) throw new Error('resize request timed out');
      await frame();
    }
    const expectedHeight = await Promise.race([
      latestResize,
      new Promise((_, reject) => setTimeout(() => reject(new Error('resize bridge timed out')), 2000)),
    ]);
    while (Math.abs(document.documentElement.clientHeight - expectedHeight) > 2) {
      if (performance.now() >= deadline) throw new Error('native resize timed out');
      await frame();
    }
  }
  async function probe() {
    await reloadSettings();
    await frame();
    const tabs = Array.from(document.querySelectorAll('[data-tab]'));
    const panels = Array.from(document.querySelectorAll('[data-tab-panel]'));
    const measurements = [];
    for (const tab of tabs) {
      latestRequest = null;
      latestResize = null;
      lastRequestedSettingsSize = '';
      tab.click();
      await waitForResizeRequest();
      const panel = panels.find(candidate => candidate.dataset.tabPanel === tab.dataset.tab);
      measurements.push({
        name: tab.dataset.tab,
        visibleHeight: Math.ceil(panel.getBoundingClientRect().height),
        naturalHeight: naturalSettingsHeight(),
        scrollWidth: panel.scrollWidth,
        clientWidth: panel.clientWidth,
        requestedWidth: latestRequest?.width || 0,
        requestedHeight: latestRequest?.height || 0,
        viewportHeight: document.documentElement.clientHeight,
        hiddenPanelsZero: panels.filter(candidate => candidate !== panel).every(candidate =>
          candidate.hidden && candidate.getBoundingClientRect().height === 0),
      });
    }
    const content = document.querySelector('.content');
    window.reportSettingsGeometry(JSON.stringify({
      viewportWidth: document.documentElement.clientWidth,
      documentScrollWidth: document.documentElement.scrollWidth,
      contentScrollWidth: content.scrollWidth,
      contentClientWidth: content.clientWidth,
      modelCount: document.querySelectorAll('.model-item').length,
      panels: measurements,
    }));
  }
  document.addEventListener('DOMContentLoaded', () => { probe().catch(error =>
    window.reportSettingsGeometry(JSON.stringify({error: String(error), panels: []}))); });
})();
</script>`
