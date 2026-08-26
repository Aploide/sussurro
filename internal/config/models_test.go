package config

import (
	"os"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestSaveModelPathUsesLoadedConfigFile(t *testing.T) {
	cfg, err := loadTestConfig(t, legacyConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveModelPath(cfg, ModelRoleLLM, "/models/qwen3-sussurro-q8_0.gguf"); err != nil {
		t.Fatalf("SaveModelPath() error = %v", err)
	}

	body, err := os.ReadFile(cfg.SourcePath())
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
		t.Fatal(err)
	}
	if paths.Models.LLM.Path != "/models/qwen3-sussurro-q8_0.gguf" {
		t.Errorf("LLM path = %q", paths.Models.LLM.Path)
	}
	if paths.Models.ASR.Path != "models/ggml-base.bin" {
		t.Errorf("ASR path = %q, want unchanged", paths.Models.ASR.Path)
	}
}

func TestSaveModelPathRejectsUnknownRoleWithoutWriting(t *testing.T) {
	cfg, err := loadTestConfig(t, legacyConfig)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(cfg.SourcePath())
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveModelPath(cfg, ModelRole("other"), "/tmp/model"); err == nil {
		t.Fatal("SaveModelPath() error = nil")
	}
	after, err := os.ReadFile(cfg.SourcePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("invalid model role changed the config")
	}
}
