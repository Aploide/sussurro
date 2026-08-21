package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// legacyConfig is a pre-review config: it has no workflow section at all.
const legacyConfig = `app:
  name: "Sussurro"
  log_level: "info"
audio:
  sample_rate: 16000
  max_duration: "60s"
models:
  asr:
    path: "models/ggml-base.bin"
    threads: 4
  llm:
    path: "models/llm.gguf"
hotkey:
  trigger: "ctrl+shift+space"
`

// loadTestConfig writes body to a temp file and loads it. LoadConfig uses
// viper's global instance, so it is reset first to keep cases independent.
func loadTestConfig(t *testing.T, body string) (*Config, error) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
	return LoadConfig(path)
}

func TestLegacyConfigKeepsImmediateDefaults(t *testing.T) {
	cfg, err := loadTestConfig(t, legacyConfig)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.Workflow.Mode != ModeImmediate {
		t.Errorf("Mode = %q, want %q", cfg.Workflow.Mode, ModeImmediate)
	}
	if cfg.Workflow.ReviewEnabled() {
		t.Error("ReviewEnabled() = true, want false for a legacy config")
	}
	// Streaming is on by default: showing text while the user speaks is the
	// point of the feature, and passes are sub-second on an accelerated host.
	if !cfg.Workflow.Streaming.Enabled {
		t.Error("Streaming.Enabled = false, want true by default")
	}
	if cfg.Workflow.Input.Backend != InputAuto {
		t.Errorf("Input.Backend = %q, want %q", cfg.Workflow.Input.Backend, InputAuto)
	}
	if cfg.Workflow.Delivery.Backend != DeliveryAuto {
		t.Errorf("Delivery.Backend = %q, want %q", cfg.Workflow.Delivery.Backend, DeliveryAuto)
	}
	if got := cfg.Workflow.StreamingInterval().String(); got != DefaultStreamingInterval {
		t.Errorf("StreamingInterval() = %s, want %s", got, DefaultStreamingInterval)
	}

	// Existing settings must survive untouched.
	if cfg.Hotkey.Trigger != "ctrl+shift+space" {
		t.Errorf("Hotkey.Trigger = %q, want unchanged", cfg.Hotkey.Trigger)
	}
	if cfg.Hotkey.Mode != "push-to-talk" {
		t.Errorf("Hotkey.Mode = %q, want push-to-talk default", cfg.Hotkey.Mode)
	}
}

func TestShippedDefaultConfigLoads(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "configs", "default.yaml"))
	if err != nil {
		t.Fatalf("reading shipped default.yaml: %v", err)
	}

	cfg, err := loadTestConfig(t, string(body))
	if err != nil {
		t.Fatalf("LoadConfig() on shipped defaults error = %v", err)
	}
	if cfg.Workflow.Mode != ModeImmediate {
		t.Errorf("Mode = %q, want shipped defaults to stay immediate", cfg.Workflow.Mode)
	}
	if !cfg.Workflow.Streaming.Enabled {
		t.Error("shipped defaults disable streaming, want enabled")
	}
}

func TestExplicitWorkflowValuesLoad(t *testing.T) {
	cfg, err := loadTestConfig(t, legacyConfig+`workflow:
  mode: "review"
  streaming:
    enabled: true
    interval: "250ms"
  input:
    backend: "evdev"
  delivery:
    backend: "wtype"
`)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if !cfg.Workflow.ReviewEnabled() {
		t.Error("ReviewEnabled() = false, want true")
	}
	if !cfg.Workflow.Streaming.Enabled {
		t.Error("Streaming.Enabled = false, want true")
	}
	if got := cfg.Workflow.StreamingInterval().String(); got != "250ms" {
		t.Errorf("StreamingInterval() = %s, want 250ms", got)
	}
	if cfg.Workflow.Input.Backend != InputEvdev {
		t.Errorf("Input.Backend = %q, want %q", cfg.Workflow.Input.Backend, InputEvdev)
	}
	if cfg.Workflow.Delivery.Backend != DeliveryWtype {
		t.Errorf("Delivery.Backend = %q, want %q", cfg.Workflow.Delivery.Backend, DeliveryWtype)
	}
}

func TestPartialWorkflowSectionKeepsOtherDefaults(t *testing.T) {
	cfg, err := loadTestConfig(t, legacyConfig+`workflow:
  mode: "review"
`)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.Workflow.Mode != ModeReview {
		t.Errorf("Mode = %q, want %q", cfg.Workflow.Mode, ModeReview)
	}
	if cfg.Workflow.Input.Backend != InputAuto {
		t.Errorf("Input.Backend = %q, want default %q", cfg.Workflow.Input.Backend, InputAuto)
	}
	if got := cfg.Workflow.StreamingInterval().String(); got != DefaultStreamingInterval {
		t.Errorf("StreamingInterval() = %s, want default", got)
	}
}

func TestInvalidWorkflowValuesRejected(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantKey  string
		wantHelp string
	}{
		{
			name:     "unknown mode",
			body:     "workflow:\n  mode: \"instant\"\n",
			wantKey:  "workflow.mode",
			wantHelp: "immediate",
		},
		{
			name:     "unknown input backend",
			body:     "workflow:\n  input:\n    backend: \"libinput\"\n",
			wantKey:  "workflow.input.backend",
			wantHelp: "native",
		},
		{
			name:     "unknown delivery backend",
			body:     "workflow:\n  delivery:\n    backend: \"xdotool\"\n",
			wantKey:  "workflow.delivery.backend",
			wantHelp: "clipboard-paste",
		},
		{
			name:     "unparseable interval",
			body:     "workflow:\n  streaming:\n    interval: \"soon\"\n",
			wantKey:  "workflow.streaming.interval",
			wantHelp: "not a duration",
		},
		{
			name:     "interval below minimum",
			body:     "workflow:\n  streaming:\n    interval: \"5ms\"\n",
			wantKey:  "workflow.streaming.interval",
			wantHelp: "outside the supported range",
		},
		{
			name:     "interval above maximum",
			body:     "workflow:\n  streaming:\n    interval: \"60s\"\n",
			wantKey:  "workflow.streaming.interval",
			wantHelp: "outside the supported range",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadTestConfig(t, legacyConfig+tt.body)
			if err == nil {
				t.Fatal("LoadConfig() error = nil, want a validation error")
			}
			msg := err.Error()
			if !strings.Contains(msg, tt.wantKey) {
				t.Errorf("error %q does not name the key %q", msg, tt.wantKey)
			}
			if !strings.Contains(msg, tt.wantHelp) {
				t.Errorf("error %q does not explain %q", msg, tt.wantHelp)
			}
		})
	}
}

func TestEnvironmentOverridesWorkflowSettings(t *testing.T) {
	t.Setenv("SUSSURRO_WORKFLOW_MODE", "review")
	t.Setenv("SUSSURRO_WORKFLOW_STREAMING_ENABLED", "true")
	t.Setenv("SUSSURRO_WORKFLOW_STREAMING_INTERVAL", "300ms")
	t.Setenv("SUSSURRO_WORKFLOW_INPUT_BACKEND", "trigger")
	t.Setenv("SUSSURRO_WORKFLOW_DELIVERY_BACKEND", "ydotool")

	cfg, err := loadTestConfig(t, legacyConfig)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.Workflow.Mode != ModeReview {
		t.Errorf("Mode = %q, want %q from environment", cfg.Workflow.Mode, ModeReview)
	}
	if !cfg.Workflow.Streaming.Enabled {
		t.Error("Streaming.Enabled = false, want true from environment")
	}
	if got := cfg.Workflow.StreamingInterval().String(); got != "300ms" {
		t.Errorf("StreamingInterval() = %s, want 300ms from environment", got)
	}
	if cfg.Workflow.Input.Backend != InputTrigger {
		t.Errorf("Input.Backend = %q, want %q from environment", cfg.Workflow.Input.Backend, InputTrigger)
	}
	if cfg.Workflow.Delivery.Backend != DeliveryYdotool {
		t.Errorf("Delivery.Backend = %q, want %q from environment", cfg.Workflow.Delivery.Backend, DeliveryYdotool)
	}
}

func TestEnvironmentOverridesConfigFileValue(t *testing.T) {
	t.Setenv("SUSSURRO_WORKFLOW_MODE", "review")

	cfg, err := loadTestConfig(t, legacyConfig+"workflow:\n  mode: \"immediate\"\n")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Workflow.Mode != ModeReview {
		t.Errorf("Mode = %q, want the environment to win over the file", cfg.Workflow.Mode)
	}
}

func TestInvalidEnvironmentValueRejected(t *testing.T) {
	t.Setenv("SUSSURRO_WORKFLOW_DELIVERY_BACKEND", "telepathy")

	_, err := loadTestConfig(t, legacyConfig)
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want a validation error")
	}
	if !strings.Contains(err.Error(), "workflow.delivery.backend") {
		t.Errorf("error %q does not name the offending key", err)
	}
}

func TestNormalizeFillsEmptyFields(t *testing.T) {
	var w WorkflowConfig
	w.Normalize()

	if err := w.Validate(); err != nil {
		t.Fatalf("normalized zero value failed validation: %v", err)
	}
	if w.Mode != DefaultInteractionMode {
		t.Errorf("Mode = %q, want %q", w.Mode, DefaultInteractionMode)
	}
	if w.Input.Backend != DefaultInputBackend {
		t.Errorf("Input.Backend = %q, want %q", w.Input.Backend, DefaultInputBackend)
	}
	if w.Delivery.Backend != DefaultDeliveryBackend {
		t.Errorf("Delivery.Backend = %q, want %q", w.Delivery.Backend, DefaultDeliveryBackend)
	}
	if w.Streaming.Interval != DefaultStreamingInterval {
		t.Errorf("Streaming.Interval = %q, want %q", w.Streaming.Interval, DefaultStreamingInterval)
	}
}

func TestNormalizePreservesExplicitFields(t *testing.T) {
	w := WorkflowConfig{
		Mode:      ModeReview,
		Streaming: StreamingConfig{Enabled: true, Interval: "1s"},
		Input:     InputConfig{Backend: InputNative},
		Delivery:  DeliveryConfig{Backend: DeliveryClipboardPaste},
	}
	w.Normalize()

	if w.Mode != ModeReview || w.Streaming.Interval != "1s" ||
		w.Input.Backend != InputNative || w.Delivery.Backend != DeliveryClipboardPaste {
		t.Errorf("Normalize() overwrote explicit values: %+v", w)
	}
}

func TestStreamingIntervalFallsBackWhenUnparseable(t *testing.T) {
	// StreamingInterval must stay usable even if a caller skips Validate.
	w := WorkflowConfig{Streaming: StreamingConfig{Interval: "nonsense"}}
	if got := w.StreamingInterval().String(); got != DefaultStreamingInterval {
		t.Errorf("StreamingInterval() = %s, want fallback %s", got, DefaultStreamingInterval)
	}
}

func TestEvdevInputOptionsLoad(t *testing.T) {
	cfg, err := loadTestConfig(t, legacyConfig+`workflow:
  input:
    backend: "evdev"
    device: "Kinesis"
    chord: "ctrl+shift+space"
    cancel_chord: "ctrl+shift+alt"
`)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.Workflow.Input.Device != "Kinesis" {
		t.Errorf("Device = %q, want Kinesis", cfg.Workflow.Input.Device)
	}
	if cfg.Workflow.Input.Chord != "ctrl+shift+space" {
		t.Errorf("Chord = %q, want ctrl+shift+space", cfg.Workflow.Input.Chord)
	}
	if cfg.Workflow.Input.CancelChord != "ctrl+shift+alt" {
		t.Errorf("CancelChord = %q, want ctrl+shift+alt", cfg.Workflow.Input.CancelChord)
	}
}

func TestEvdevInputOptionsDefaultToEmpty(t *testing.T) {
	cfg, err := loadTestConfig(t, legacyConfig)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Empty means "first stable keyboard" and "follow hotkey.trigger", so a
	// legacy config needs no new keys.
	if cfg.Workflow.Input.Device != "" || cfg.Workflow.Input.Chord != "" ||
		cfg.Workflow.Input.CancelChord != "" {
		t.Errorf("evdev options = %+v, want all empty by default", cfg.Workflow.Input)
	}
}

func TestMalformedChordRejected(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "empty component",
			body: "workflow:\n  input:\n    chord: \"ctrl++space\"\n",
			want: "empty component",
		},
		{
			name: "duplicate component",
			body: "workflow:\n  input:\n    chord: \"ctrl+ctrl+space\"\n",
			want: "more than once",
		},
		{
			name: "malformed cancel chord",
			body: "workflow:\n  input:\n    cancel_chord: \"alt++esc\"\n",
			want: "empty component",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadTestConfig(t, legacyConfig+tt.body)
			if err == nil {
				t.Fatal("LoadConfig() error = nil, want a validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not explain %q", err, tt.want)
			}
		})
	}
}

// readShippedDefaults returns the contents of the shipped configs/default.yaml.
func readShippedDefaults(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "configs", "default.yaml"))
	if err != nil {
		t.Fatalf("reading shipped default.yaml: %v", err)
	}
	return string(body)
}

func TestClipboardOnlyDefaultsToPasting(t *testing.T) {
	cfg, err := loadTestConfig(t, legacyConfig)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	// Auto-paste is the existing behaviour and must stay the default.
	if cfg.Workflow.Delivery.ClipboardOnly {
		t.Error("ClipboardOnly = true by default, want false")
	}
}

func TestClipboardOnlyLoadsFromConfig(t *testing.T) {
	cfg, err := loadTestConfig(t, legacyConfig+`workflow:
  delivery:
    clipboard_only: true
`)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.Workflow.Delivery.ClipboardOnly {
		t.Error("ClipboardOnly = false, want true")
	}
}

func TestClipboardOnlyFromEnvironment(t *testing.T) {
	t.Setenv("SUSSURRO_WORKFLOW_DELIVERY_CLIPBOARD_ONLY", "true")

	cfg, err := loadTestConfig(t, legacyConfig)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.Workflow.Delivery.ClipboardOnly {
		t.Error("ClipboardOnly = false, want true from the environment")
	}
}
