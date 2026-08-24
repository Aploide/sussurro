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

	// MinDuration is the shortest recording sent to recognition. It guards
	// against Whisper inventing stock phrases from near-silence, so it should
	// stay above an accidental keypress and below a real one-word dictation.
	MinDuration string `mapstructure:"min_duration"`
}

type ModelsConfig struct {
	ASR ASRConfig `mapstructure:"asr"`
	LLM LLMConfig `mapstructure:"llm"`
}

const (
	DefaultVADModelFilename = "ggml-silero-v6.2.0.bin"
	DefaultVADModelURL      = "https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v6.2.0.bin"
	MinimumVADModelSize     = 100 * 1024
)

type ASRConfig struct {
	Path         string  `mapstructure:"path"`
	VADPath      string  `mapstructure:"vad_path"`
	VADThreshold float32 `mapstructure:"vad_threshold"`
	Type         string  `mapstructure:"type"`
	Threads      int     `mapstructure:"threads"`
	Language     string  `mapstructure:"language"`
}

// ResolvedVADPath returns the explicit VAD model path, or the setup-managed
// default for existing configurations that predate vad_path.
func (c ASRConfig) ResolvedVADPath() string {
	if c.VADPath != "" {
		return c.VADPath
	}
	if path := defaultVADModelPath(); path != "" {
		return path
	}
	if c.Path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(c.Path), DefaultVADModelFilename)
}

func defaultVADModelPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".sussurro", "models", DefaultVADModelFilename)
}

type LLMConfig struct {
	Path        string `mapstructure:"path"`
	ContextSize int    `mapstructure:"context_size"`
	GpuLayers   int    `mapstructure:"gpu_layers"`
	Threads     int    `mapstructure:"threads"`
	// ExtendedPrompt enables strict correction-only instructions. Leave false
	// for the bundled qwen3-sussurro model, which needs its trained cleanup
	// prompt plus correction examples; set true for a general instruct model.
	ExtendedPrompt bool `mapstructure:"extended_prompt"`
}

// HotkeyConfig holds the keyboard bindings that start and stop recording.
//
// PushToTalk and Toggle are independent and each optional. The previous design
// had one trigger with a mode applied to it, which made the behaviour a
// property of the binding and so allowed only one at a time; a user wanting a
// held key and a tapped key could not have both.
type HotkeyConfig struct {
	// PushToTalk records while held and transcribes on release.
	PushToTalk string `mapstructure:"push_to_talk"`
	// Toggle starts recording on one press and stops on the next.
	Toggle string `mapstructure:"toggle"`

	// Trigger and Mode are the superseded single-binding form. Still read so
	// existing configs keep their hotkey; Normalize folds them into whichever
	// binding the mode named.
	Trigger string `mapstructure:"trigger"`
	Mode    string `mapstructure:"mode"` // "push-to-talk" or "toggle"
}

// Normalize folds a legacy trigger/mode pair into the binding its mode
// described, but only into a binding that is not already set.
//
// The per-binding check matters for a half-migrated config: someone adding
// toggle: to a file that still has trigger:/mode: would otherwise lose the
// trigger entirely, because a whole-config check would see "some new binding
// exists" and skip the migration.
func (h *HotkeyConfig) Normalize() {
	if h.Trigger == "" {
		return
	}
	if h.Mode == "toggle" {
		if h.Toggle == "" {
			h.Toggle = h.Trigger
		}
		return
	}
	if h.PushToTalk == "" {
		h.PushToTalk = h.Trigger
	}
}

// Configured reports whether any keyboard binding is set. None is valid: on
// Wayland the trigger socket is used instead.
func (h HotkeyConfig) Configured() bool {
	return h.PushToTalk != "" || h.Toggle != ""
}

type InjectionConfig struct {
	Method string `mapstructure:"method"`
}

// SaveLanguage rewrites only the models.asr.language field in the YAML config file.
// If the key does not exist (old config), it inserts it after the threads: line in the asr: section.
func SaveLanguage(cfg *Config, language string) error {
	configFile, err := userConfigPath()
	if err != nil {
		return fmt.Errorf("resolve user config path: %w", err)
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
		return fmt.Errorf("resolve user config path: %w", err)
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
		return fmt.Errorf("resolve user config path: %w", err)
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
	viper.SetDefault("models.asr.vad_path", defaultVADModelPath())
	viper.SetDefault("models.asr.vad_threshold", float32(0.01))
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
		return nil, fmt.Errorf("bind workflow environment: %w", err)
	}

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Try fallback to "default" (old behavior)
			viper.SetConfigName("default")
			if err := viper.ReadInConfig(); err != nil {
				return nil, fmt.Errorf("read fallback configuration: %w", err)
			}
		} else {
			// Configs written on Windows before the path quoting fix are not
			// valid YAML at all (see yamlpath.go), so the app could never start
			// to correct them. viper records the file it chose even when the
			// parse fails, so repair it in place and retry once.
			repaired, rerr := repairConfigPaths(viper.ConfigFileUsed())
			if rerr != nil || !repaired {
				return nil, fmt.Errorf("read configuration: %w", err)
			}
			if err := viper.ReadInConfig(); err != nil {
				return nil, fmt.Errorf("read repaired configuration: %w", err)
			}
		}
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("decode configuration: %w", err)
	}

	if cfg.Models.ASR.VADThreshold <= 0 || cfg.Models.ASR.VADThreshold > 1 {
		return nil, fmt.Errorf("invalid configuration: models.asr.vad_threshold must be greater than 0 and at most 1")
	}

	cfg.Hotkey.Normalize()
	cfg.Workflow.Normalize()
	if err := cfg.Workflow.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// SaveHotkeyBinding writes one hotkey binding to the user's config file.
// name is the YAML key under hotkey: "push_to_talk" or "toggle". An empty
// trigger clears the binding, which is valid — either may be unset.
func SaveHotkeyBinding(name, trigger string) error {
	if err := SaveWorkflowValue("hotkey."+name, YAMLString(trigger)); err != nil {
		return fmt.Errorf("save hotkey binding: %w", err)
	}
	return nil
}
