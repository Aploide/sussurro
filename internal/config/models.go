package config

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

// ModelRole identifies the configured engine whose model path is changing.
type ModelRole string

const (
	ModelRoleASR ModelRole = "asr"
	ModelRoleLLM ModelRole = "llm"
)

// SaveModelPath atomically persists a model path in the file that supplied cfg.
// It does not mutate cfg; callers update live state only after the write succeeds.
func SaveModelPath(cfg *Config, role ModelRole, path string) error {
	if role != ModelRoleASR && role != ModelRoleLLM {
		return fmt.Errorf("unknown model role %q", role)
	}

	configSaveMu.Lock()
	defer configSaveMu.Unlock()

	configFile, err := configPath(cfg)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("read configuration: %w", err)
	}
	key := "models." + string(role) + ".path"
	updated, err := SetNestedValue(string(data), key, YAMLPathLiteral(path))
	if err != nil {
		return fmt.Errorf("set %s: %w", key, err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(updated), &document); err != nil {
		return fmt.Errorf("%s cannot be saved without changing this config's YAML structure: %w", key, err)
	}
	if err := writeConfigAtomically(configFile, []byte(updated)); err != nil {
		return fmt.Errorf("write config atomically: %w", err)
	}
	return nil
}
