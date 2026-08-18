package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	App       AppConfig       `mapstructure:"app"`
	Audio     AudioConfig     `mapstructure:"audio"`
	Models    ModelsConfig    `mapstructure:"models"`
	Hotkey    HotkeyConfig    `mapstructure:"hotkey"`
	Injection InjectionConfig `mapstructure:"injection"`
	// Workflow holds the opt-in streaming review settings. Absent from
	// pre-review configs, where Normalize supplies immediate-mode defaults.
	Workflow WorkflowConfig `mapstructure:"workflow"`
}

type AppConfig struct {
	Name            string `mapstructure:"name"`
	Debug           bool   `mapstructure:"debug"`
	LogLevel        string `mapstructure:"log_level"`
	LowercaseOutput bool   `mapstructure:"lowercase_output"`
	SkipLLMCleanup  bool   `mapstructure:"skip_llm_cleanup"`
	// Dictionary lists names and terms the cleanup stage must spell exactly
	// as written (personal vocabulary the ASR tends to mishear).
	Dictionary []string `mapstructure:"dictionary"`
}

type AudioConfig struct {
	SampleRate  int    `mapstructure:"sample_rate"`
	Channels    int    `mapstructure:"channels"`
	BitDepth    int    `mapstructure:"bit_depth"`
	BufferSize  int    `mapstructure:"buffer_size"`
	MaxDuration string `mapstructure:"max_duration"`
}

type ModelsConfig struct {
	ASR ASRConfig `mapstructure:"asr"`
	LLM LLMConfig `mapstructure:"llm"`
}

type ASRConfig struct {
	Path     string `mapstructure:"path"`
	Type     string `mapstructure:"type"`
	Threads  int    `mapstructure:"threads"`
	Language string `mapstructure:"language"`
}

type LLMConfig struct {
	Path        string `mapstructure:"path"`
	ContextSize int    `mapstructure:"context_size"`
	GpuLayers   int    `mapstructure:"gpu_layers"`
	Threads     int    `mapstructure:"threads"`
	// ExtendedPrompt enables richer cleanup instructions (no-summarization
	// contract, list formatting, prompt-level dictionary). Leave false for
	// the bundled qwen3-sussurro fine-tune, which only behaves with the
	// prompt it was trained on; set true when pointing path at a general
	// instruct model.
	ExtendedPrompt bool `mapstructure:"extended_prompt"`
}

type HotkeyConfig struct {
	Trigger string `mapstructure:"trigger"`
	Mode    string `mapstructure:"mode"` // "push-to-talk" or "toggle"
}

type InjectionConfig struct {
	Method string `mapstructure:"method"`
}

// SaveHotkey rewrites only the hotkey.trigger field in the YAML config file.
func SaveHotkey(cfg *Config, trigger string) error {
	configFile, err := userConfigPath()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	// Simple line-by-line replacement of the trigger value.
	lines := strings.Split(string(data), "\n")
	replaced := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "trigger:") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = indent + "trigger: \"" + trigger + "\""
			replaced = true
			break
		}
	}
	if !replaced {
		return fmt.Errorf("trigger key not found in config file")
	}

	return os.WriteFile(configFile, []byte(strings.Join(lines, "\n")), 0644)
}

// SaveHotkeyMode rewrites only the hotkey.mode field in the YAML config file.
// If the key does not exist (old config), it inserts it after the trigger: line.
func SaveHotkeyMode(cfg *Config, mode string) error {
	configFile, err := userConfigPath()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	lines := strings.Split(string(data), "\n")

	// First pass: replace existing mode: key.
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "mode:") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = indent + `mode: "` + mode + `"`
			return os.WriteFile(configFile, []byte(strings.Join(lines, "\n")), 0644)
		}
	}

	// Key missing: insert after trigger: line.
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "trigger:") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			newLine := indent + `mode: "` + mode + `"`
			newLines := make([]string, 0, len(lines)+1)
			newLines = append(newLines, lines[:i+1]...)
			newLines = append(newLines, newLine)
			newLines = append(newLines, lines[i+1:]...)
			return os.WriteFile(configFile, []byte(strings.Join(newLines, "\n")), 0644)
		}
	}

	return fmt.Errorf("trigger key not found in config file; cannot insert mode")
}

// SaveLanguage rewrites only the models.asr.language field in the YAML config file.
// If the key does not exist (old config), it inserts it after the threads: line in the asr: section.
func SaveLanguage(cfg *Config, language string) error {
	configFile, err := userConfigPath()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	lines := strings.Split(string(data), "\n")

	// First pass: replace existing language: key.
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "language:") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = indent + `language: "` + language + `"`
			return os.WriteFile(configFile, []byte(strings.Join(lines, "\n")), 0644)
		}
	}

	// Key missing: insert after threads: inside the asr: subsection.
	inASR := false
	asrIndent := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		trimmed := strings.TrimSpace(line)

		if trimmed == "asr:" {
			inASR = true
			asrIndent = indent
			continue
		}

		if inASR {
			// Leaving the asr: block when indent drops back to its level.
			if indent <= asrIndent {
				inASR = false
				continue
			}
			if strings.HasPrefix(trimmed, "threads:") {
				threadIndent := line[:indent]
				newLine := threadIndent + `language: "` + language + `"`
				newLines := make([]string, 0, len(lines)+1)
				newLines = append(newLines, lines[:i+1]...)
				newLines = append(newLines, newLine)
				newLines = append(newLines, lines[i+1:]...)
				return os.WriteFile(configFile, []byte(strings.Join(newLines, "\n")), 0644)
			}
		}
	}

	return fmt.Errorf("could not find asr.threads key in config file; cannot insert language")
}

// SaveLowercaseOutput rewrites only the app.lowercase_output field in the YAML config file.
// If the key does not exist (old config), it inserts it after the log_level: line in the app: section.
func SaveLowercaseOutput(cfg *Config, enabled bool) error {
	configFile, err := userConfigPath()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	val := "false"
	if enabled {
		val = "true"
	}

	lines := strings.Split(string(data), "\n")

	// First pass: replace existing lowercase_output: key.
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "lowercase_output:") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = indent + "lowercase_output: " + val
			return os.WriteFile(configFile, []byte(strings.Join(lines, "\n")), 0644)
		}
	}

	// Key missing: insert after log_level: line.
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "log_level:") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			newLine := indent + "lowercase_output: " + val
			newLines := make([]string, 0, len(lines)+1)
			newLines = append(newLines, lines[:i+1]...)
			newLines = append(newLines, newLine)
			newLines = append(newLines, lines[i+1:]...)
			return os.WriteFile(configFile, []byte(strings.Join(newLines, "\n")), 0644)
		}
	}

	return fmt.Errorf("log_level key not found in config file; cannot insert lowercase_output")
}

// SaveSkipLLMCleanup rewrites only the app.skip_llm_cleanup field in the YAML config file.
// If the key does not exist (old config), it inserts it after lowercase_output:
// (or after log_level: if lowercase_output: is also missing).
func SaveSkipLLMCleanup(cfg *Config, enabled bool) error {
	configFile, err := userConfigPath()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	val := "false"
	if enabled {
		val = "true"
	}

	lines := strings.Split(string(data), "\n")

	// First pass: replace existing skip_llm_cleanup: key.
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "skip_llm_cleanup:") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = indent + "skip_llm_cleanup: " + val
			return os.WriteFile(configFile, []byte(strings.Join(lines, "\n")), 0644)
		}
	}

	// Key missing: insert after lowercase_output: if present.
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "lowercase_output:") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			newLine := indent + "skip_llm_cleanup: " + val
			newLines := make([]string, 0, len(lines)+1)
			newLines = append(newLines, lines[:i+1]...)
			newLines = append(newLines, newLine)
			newLines = append(newLines, lines[i+1:]...)
			return os.WriteFile(configFile, []byte(strings.Join(newLines, "\n")), 0644)
		}
	}

	// Backward-compat fallback: insert after log_level:.
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "log_level:") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			newLine := indent + "skip_llm_cleanup: " + val
			newLines := make([]string, 0, len(lines)+1)
			newLines = append(newLines, lines[:i+1]...)
			newLines = append(newLines, newLine)
			newLines = append(newLines, lines[i+1:]...)
			return os.WriteFile(configFile, []byte(strings.Join(newLines, "\n")), 0644)
		}
	}

	return fmt.Errorf("log_level key not found in config file; cannot insert skip_llm_cleanup")
}

func LoadConfig(path string) (*Config, error) {
	if path != "" {
		// If a specific file path is provided, use it directly
		viper.SetConfigFile(path)
	} else {
		// Otherwise search in default locations
		viper.SetConfigName("config") // Look for config.yaml (or .json, .toml)
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		// Use the resolved home directory rather than a "$HOME" literal:
		// viper does not expand it, and the variable does not exist on Windows.
		if home, err := os.UserHomeDir(); err == nil {
			viper.AddConfigPath(filepath.Join(home, ".sussurro"))
		}
		viper.AddConfigPath("./configs")
	}

	viper.SetDefault("models.asr.language", "en")
	viper.SetDefault("hotkey.mode", "push-to-talk")
	viper.SetDefault("app.lowercase_output", false)
	viper.SetDefault("app.skip_llm_cleanup", false)
	setWorkflowDefaults(viper.GetViper())

	viper.SetEnvPrefix("SUSSURRO")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	// AutomaticEnv alone does not surface keys that Unmarshal has to discover,
	// so bind the workflow keys explicitly.
	if err := bindWorkflowEnv(viper.GetViper()); err != nil {
		return nil, err
	}

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Try fallback to "default" (old behavior)
			viper.SetConfigName("default")
			if err := viper.ReadInConfig(); err != nil {
				return nil, err
			}
		} else {
			// Configs written on Windows before the path quoting fix are not
			// valid YAML at all (see yamlpath.go), so the app could never start
			// to correct them. viper records the file it chose even when the
			// parse fails, so repair it in place and retry once.
			repaired, rerr := repairConfigPaths(viper.ConfigFileUsed())
			if rerr != nil || !repaired {
				return nil, err
			}
			if err := viper.ReadInConfig(); err != nil {
				return nil, err
			}
		}
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	cfg.Workflow.Normalize()
	if err := cfg.Workflow.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}
