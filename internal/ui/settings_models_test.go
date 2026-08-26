package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitialDataDiscoversOnlySupportedInstalledLLMs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	modelsDir := filepath.Join(home, ".sussurro", "models")
	if err := os.MkdirAll(modelsDir, 0755); err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(modelsDir, "qwen3-sussurro-q5_k_m.gguf")
	for _, path := range []string{activePath, filepath.Join(modelsDir, "unrelated.gguf")} {
		if err := os.WriteFile(path, []byte("fixture"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := defaultConfig()
	cfg.Models.LLM.Path = activePath
	models := buildInitialData(&Manager{cfg: cfg}).Models

	llms := modelsOfType(models, "llm")
	if len(llms) != 4 {
		t.Fatalf("LLM count = %d, want four supported quantizations: %#v", len(llms), llms)
	}
	active := findModelInfo(t, llms, "qwen3-sussurro-q5-k-m")
	if !active.Installed || !active.Active || !active.Selectable || !active.Downloadable {
		t.Errorf("Q5 model flags = %+v, want installed, active, selectable, downloadable", active)
	}
	for _, model := range llms {
		if strings.Contains(model.Name, "unrelated") || model.ID == "configured-llm" {
			t.Errorf("unrelated GGUF was advertised as supported: %+v", model)
		}
	}
}

func TestInitialDataPreservesExternallyConfiguredLLM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	customPath := filepath.Join(home, "models", "my-cleanup.gguf")
	if err := os.MkdirAll(filepath.Dir(customPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(customPath, []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := defaultConfig()
	cfg.Models.LLM.Path = customPath
	custom := findModelInfo(t, buildInitialData(&Manager{cfg: cfg}).Models, "configured-llm")
	if !custom.Active || !custom.Installed {
		t.Errorf("configured LLM flags = %+v, want active and installed", custom)
	}
	if custom.Selectable || custom.Downloadable {
		t.Errorf("configured LLM flags = %+v, want unmanaged model disabled", custom)
	}
	if !strings.Contains(custom.Description, "compatibility not verified") {
		t.Errorf("configured LLM description = %q, want compatibility warning", custom.Description)
	}
}

func TestOfficialLLMDownloadAndSelectionStayConnected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	url, destination, name := resolveModelDownload("qwen3-sussurro-q8-0")
	if !strings.HasSuffix(url, "/qwen3-sussurro-q8_0.gguf") {
		t.Errorf("download URL = %q", url)
	}
	if destination != filepath.Join(home, ".sussurro", "models", "qwen3-sussurro-q8_0.gguf") {
		t.Errorf("download destination = %q", destination)
	}
	if name != "Qwen 3 Sussurro Q8_0" {
		t.Errorf("download name = %q", name)
	}

	js := readAsset(t, "app.js")
	if strings.Contains(js, "if (m.type === 'llm')") {
		t.Error("app.js still disables every LLM radio")
	}
	for _, want := range []string{
		"radio.disabled = !m.selectable",
		"window.setActiveModel(m.id)",
		"if (!m.installed) { await reloadSettings(); return; }",
		"if (res.startsWith('error')) { await reloadSettings(); return; }",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js is missing selection contract %q", want)
		}
	}
}

func modelsOfType(models []modelInfo, kind string) []modelInfo {
	var matching []modelInfo
	for _, model := range models {
		if model.Type == kind {
			matching = append(matching, model)
		}
	}
	return matching
}

func findModelInfo(t *testing.T, models []modelInfo, id string) modelInfo {
	t.Helper()
	for _, model := range models {
		if model.ID == id {
			return model
		}
	}
	t.Fatalf("model %q not found in %#v", id, models)
	return modelInfo{}
}
