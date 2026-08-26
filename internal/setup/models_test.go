package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aploide/sussurro/internal/config"
	"go.yaml.in/yaml/v3"
)

func TestSupportedModelsCataloguesOfficialLLMQuantizations(t *testing.T) {
	want := map[string]string{
		"qwen3-sussurro-q4-k-m": "qwen3-sussurro-q4_k_m.gguf",
		"qwen3-sussurro-q5-k-m": "qwen3-sussurro-q5_k_m.gguf",
		"qwen3-sussurro-q8-0":   "qwen3-sussurro-q8_0.gguf",
		"qwen3-sussurro-f16":    "qwen3-sussurro-f16.gguf",
	}
	for _, model := range SupportedModels() {
		if model.Kind != ModelKindLLM {
			continue
		}
		filename, ok := want[model.ID]
		if !ok {
			t.Errorf("unexpected supported LLM %q", model.ID)
			continue
		}
		if model.Filename != filename {
			t.Errorf("%s filename = %q, want %q", model.ID, model.Filename, filename)
		}
		if model.DownloadURL == "" {
			t.Errorf("%s has no download URL", model.ID)
		}
		delete(want, model.ID)
	}
	if len(want) != 0 {
		t.Errorf("missing supported LLMs: %v", want)
	}
	if _, ok := FindModel("arbitrary-gguf"); ok {
		t.Error("arbitrary GGUF was treated as supported")
	}
}

func TestActivateModelUpdatesLoadedConfigAndLiveState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	modelsDir := filepath.Join(home, ".sussurro", "models")
	if err := os.MkdirAll(modelsDir, 0755); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(modelsDir, "qwen3-sussurro-q5_k_m.gguf")
	if err := os.WriteFile(wantPath, []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}

	customDir := t.TempDir()
	configPath := filepath.Join(customDir, "custom.yaml")
	original := "models:\n  asr:\n    path: /custom/asr.bin\n  llm:\n    path: /custom/old.gguf\n"
	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := ActivateModel(cfg, "qwen3-sussurro-q5-k-m"); err != nil {
		t.Fatalf("ActivateModel() error = %v", err)
	}
	if cfg.Models.LLM.Path != wantPath {
		t.Errorf("live LLM path = %q, want %q", cfg.Models.LLM.Path, wantPath)
	}
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var paths struct {
		Models struct {
			ASR struct {
				Path string `yaml:"path"`
			} `yaml:"asr"`
			LLM struct {
				Path string `yaml:"path"`
			} `yaml:"llm"`
		} `yaml:"models"`
	}
	if err := yaml.Unmarshal(body, &paths); err != nil {
		t.Fatalf("parse updated config: %v", err)
	}
	if paths.Models.LLM.Path != wantPath {
		t.Errorf("saved LLM path = %q, want %q", paths.Models.LLM.Path, wantPath)
	}
	if paths.Models.ASR.Path != "/custom/asr.bin" {
		t.Errorf("ASR path = %q, want unchanged", paths.Models.ASR.Path)
	}
	if _, err := os.Stat(filepath.Join(home, ".sussurro", "config.yaml")); !os.IsNotExist(err) {
		t.Errorf("activation wrote the default config: %v", err)
	}
}

func TestEnsureSetupAcceptsConfiguredQ5Model(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	modelsDir := filepath.Join(home, ".sussurro", "models")
	if err := os.MkdirAll(modelsDir, 0755); err != nil {
		t.Fatal(err)
	}
	asrPath := filepath.Join(modelsDir, fileASRSmall)
	vadPath := filepath.Join(modelsDir, fileVAD)
	llmPath := filepath.Join(modelsDir, "qwen3-sussurro-q5_k_m.gguf")
	for path, contents := range map[string][]byte{
		asrPath: []byte("asr"),
		vadPath: make([]byte, config.MinimumVADModelSize),
		llmPath: []byte("llm"),
	} {
		if err := os.WriteFile(path, contents, 0644); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(t.TempDir(), "custom.yaml")
	body := "models:\n  asr:\n    path: " + config.YAMLPathLiteral(asrPath) + "\n    vad_path: " + config.YAMLPathLiteral(vadPath) + "\n  llm:\n    path: " + config.YAMLPathLiteral(llmPath) + "\n"
	if err := os.WriteFile(configPath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureSetup(configPath); err != nil {
		t.Fatalf("EnsureSetup() error = %v", err)
	}
	if _, err := os.Stat(llmPath); err != nil {
		t.Errorf("configured Q5 model was removed: %v", err)
	}
}
