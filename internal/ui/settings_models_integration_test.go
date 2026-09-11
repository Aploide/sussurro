//go:build linux && settings_geometry

package ui

import (
	"encoding/json"
	"runtime"
	"sync"
	"testing"
	"time"

	webview "github.com/webview/webview_go"
)

type renderedModelFlow struct {
	Error                 string `json:"error"`
	FailureRestoredActive bool   `json:"failureRestoredActive"`
	DownloadEnabled       bool   `json:"downloadEnabled"`
	SelectionActivated    bool   `json:"selectionActivated"`
}

func TestRenderedModelSelectionFlow(t *testing.T) {
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
	w.SetTitle("Sussurro Settings model flow test")
	applySettingsSize(w)

	states := []string{
		modelFlowSettingsData(t, false, "q4"),
		modelFlowSettingsData(t, true, "q4"),
		modelFlowSettingsData(t, true, "q8"),
	}
	state := 0
	var stateMu sync.Mutex
	if err := w.Bind("getInitialData", func() string {
		stateMu.Lock()
		defer stateMu.Unlock()
		return states[state]
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Bind("resizeSettingsWindow", func(_, _ int) {}); err != nil {
		t.Fatal(err)
	}
	if err := w.Bind("markModelDownloaded", func() {
		stateMu.Lock()
		state = 1
		stateMu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Bind("setActiveModel", func(id string) string {
		if id == "q5" {
			return "error: simulated activation failure"
		}
		if id != "q8" {
			return "error: unexpected model"
		}
		stateMu.Lock()
		state = 2
		stateMu.Unlock()
		return "ok"
	}); err != nil {
		t.Fatal(err)
	}

	result := make(chan string, 1)
	if err := w.Bind("reportModelFlow", func(payload string) {
		result <- payload
		w.Terminate()
	}); err != nil {
		t.Fatal(err)
	}
	w.SetHtml(settingsHTML + modelFlowProbeScript)
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
		var flow renderedModelFlow
		if err := json.Unmarshal([]byte(payload), &flow); err != nil {
			t.Fatalf("decode model flow %q: %v", payload, err)
		}
		if flow.Error != "" {
			t.Fatalf("rendered model flow error: %s", flow.Error)
		}
		if !flow.FailureRestoredActive {
			t.Error("failed selection did not restore the active model radio")
		}
		if !flow.DownloadEnabled {
			t.Error("completed download did not enable model selection")
		}
		if !flow.SelectionActivated {
			t.Error("successful LLM selection did not become active")
		}
	default:
		t.Fatal("timed out waiting for rendered model flow")
	}
}

func modelFlowSettingsData(t *testing.T, q8Installed bool, active string) string {
	t.Helper()
	data := make(map[string]any)
	if err := json.Unmarshal([]byte(representativeSettingsData(t)), &data); err != nil {
		t.Fatal(err)
	}
	data["models"] = []map[string]any{
		{"id": "small", "name": "Whisper Small", "desc": "ASR", "size": "488 MB", "installed": true, "active": true, "downloadable": true, "selectable": true, "type": "whisper"},
		{"id": "q4", "name": "Qwen Q4", "desc": "LLM", "size": "1.28 GB", "installed": true, "active": active == "q4", "downloadable": true, "selectable": true, "type": "llm"},
		{"id": "q5", "name": "Qwen Q5", "desc": "LLM", "size": "1.47 GB", "installed": true, "active": false, "downloadable": true, "selectable": true, "type": "llm"},
		{"id": "q8", "name": "Qwen Q8", "desc": "LLM", "size": "2.17 GB", "installed": q8Installed, "active": active == "q8", "downloadable": true, "selectable": q8Installed, "type": "llm"},
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

const modelFlowProbeScript = `<script>
(() => {
  const waitFor = async predicate => {
    const deadline = performance.now() + 3000;
    while (!predicate()) {
      if (performance.now() >= deadline) {
        throw new Error('model flow timed out; active=' + activeID() +
          ', q4=' + radio('q4')?.checked + ', q5=' + radio('q5')?.checked +
          ', q8=' + radio('q8')?.checked + ', q8Disabled=' + radio('q8')?.disabled);
      }
      await new Promise(resolve => setTimeout(resolve, 20));
    }
  };
  const radio = id => document.querySelector('.model-item[data-id="' + id + '"] input');
  const activeID = () => document.querySelector('#llm-list .model-item.active')?.dataset.id;
  let stage = 'initial render';
  async function probe() {
    await reloadSettings();

    stage = 'failed selection restore';
    radio('q5').checked = true;
    radio('q5').dispatchEvent(new Event('change', {bubbles: true}));
    await waitFor(() => radio('q4')?.checked && activeID() === 'q4');
    const failureRestoredActive = radio('q4').checked && activeID() === 'q4';

    stage = 'download enables selection';
    await window.markModelDownloaded();
    window.onDownloadComplete('q8');
    await waitFor(() => radio('q8') && !radio('q8').disabled);
    const downloadEnabled = !radio('q8').disabled;

    stage = 'successful selection';
    radio('q8').checked = true;
    radio('q8').dispatchEvent(new Event('change', {bubbles: true}));
    await waitFor(() => radio('q8')?.checked && activeID() === 'q8');
    const selectionActivated = radio('q8').checked && activeID() === 'q8';

    window.reportModelFlow(JSON.stringify({failureRestoredActive, downloadEnabled, selectionActivated}));
  }
  document.addEventListener('DOMContentLoaded', () => probe().catch(error =>
    window.reportModelFlow(JSON.stringify({error: stage + ': ' + String(error)}))));
})();
</script>`
