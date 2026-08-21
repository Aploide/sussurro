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
	// The legacy trigger migrates to push-to-talk rather than being lost.
	if cfg.Hotkey.PushToTalk != "ctrl+shift+space" {
		t.Errorf("Hotkey.PushToTalk = %q, want the legacy trigger migrated", cfg.Hotkey.PushToTalk)
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

func TestDeliveryDefaultsToPasting(t *testing.T) {
	cfg, err := loadTestConfig(t, legacyConfig)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Workflow.ClipboardOnlyDelivery() {
		t.Error("ClipboardOnlyDelivery() = true by default, want pasting")
	}
}

func TestClipboardOnlyBackendLoads(t *testing.T) {
	cfg, err := loadTestConfig(t, legacyConfig+`workflow:
  delivery:
    backend: "clipboard-only"
`)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.Workflow.ClipboardOnlyDelivery() {
		t.Error("ClipboardOnlyDelivery() = false, want true")
	}
}

func TestLegacyClipboardOnlyBooleanStillWorks(t *testing.T) {
	// Configs written before the two controls were merged must keep behaving
	// the same, or the change silently starts pasting into people's windows.
	cfg, err := loadTestConfig(t, legacyConfig+`workflow:
  delivery:
    clipboard_only: true
`)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.Workflow.ClipboardOnlyDelivery() {
		t.Error("ClipboardOnlyDelivery() = false for a legacy clipboard_only config")
	}
	if cfg.Workflow.Delivery.Backend != DeliveryClipboardOnly {
		t.Errorf("Backend = %q, want it folded to clipboard-only", cfg.Workflow.Delivery.Backend)
	}
}

func TestExplicitBackendWinsOverLegacyBoolean(t *testing.T) {
	// An explicit backend choice must not be overridden by a stale boolean.
	cfg, err := loadTestConfig(t, legacyConfig+`workflow:
  delivery:
    backend: "wtype"
    clipboard_only: true
`)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Workflow.Delivery.Backend != DeliveryWtype {
		t.Errorf("Backend = %q, want the explicit choice preserved", cfg.Workflow.Delivery.Backend)
	}
}

func TestClipboardOnlyFromEnvironment(t *testing.T) {
	t.Setenv("SUSSURRO_WORKFLOW_DELIVERY_BACKEND", "clipboard-only")

	cfg, err := loadTestConfig(t, legacyConfig)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.Workflow.ClipboardOnlyDelivery() {
		t.Error("ClipboardOnlyDelivery() = false, want true from the environment")
	}
}

func TestIndependentHotkeyBindings(t *testing.T) {
	cfg, err := loadTestConfig(t, minimalModels+`hotkey:
  push_to_talk: "super+7"
  toggle: "super+8"
`)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	// Both at once is the point: the old design made this impossible.
	if cfg.Hotkey.PushToTalk != "super+7" {
		t.Errorf("PushToTalk = %q, want super+7", cfg.Hotkey.PushToTalk)
	}
	if cfg.Hotkey.Toggle != "super+8" {
		t.Errorf("Toggle = %q, want super+8", cfg.Hotkey.Toggle)
	}
}

func TestEitherHotkeyBindingMayBeUnset(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		pushToTalk string
		toggle     string
	}{
		{
			name:       "toggle only",
			body:       "hotkey:\n  toggle: \"super+8\"\n",
			pushToTalk: "",
			toggle:     "super+8",
		},
		{
			name:       "push to talk only",
			body:       "hotkey:\n  push_to_talk: \"super+7\"\n",
			pushToTalk: "super+7",
			toggle:     "",
		},
		{
			name:       "neither",
			body:       "hotkey:\n  push_to_talk: \"\"\n",
			pushToTalk: "",
			toggle:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadTestConfig(t, minimalModels+tt.body)
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if cfg.Hotkey.PushToTalk != tt.pushToTalk {
				t.Errorf("PushToTalk = %q, want %q", cfg.Hotkey.PushToTalk, tt.pushToTalk)
			}
			if cfg.Hotkey.Toggle != tt.toggle {
				t.Errorf("Toggle = %q, want %q", cfg.Hotkey.Toggle, tt.toggle)
			}
		})
	}
}

func TestLegacyHotkeyTriggerMigrates(t *testing.T) {
	// An existing config must keep its hotkey rather than silently losing it.
	tests := []struct {
		name       string
		body       string
		pushToTalk string
		toggle     string
	}{
		{
			name:       "push-to-talk mode",
			body:       "hotkey:\n  trigger: \"super+7\"\n  mode: \"push-to-talk\"\n",
			pushToTalk: "super+7",
		},
		{
			name:   "toggle mode",
			body:   "hotkey:\n  trigger: \"super+8\"\n  mode: \"toggle\"\n",
			toggle: "super+8",
		},
		{
			name:       "no mode defaults to push-to-talk",
			body:       "hotkey:\n  trigger: \"super+9\"\n",
			pushToTalk: "super+9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadTestConfig(t, minimalModels+tt.body)
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if cfg.Hotkey.PushToTalk != tt.pushToTalk {
				t.Errorf("PushToTalk = %q, want %q", cfg.Hotkey.PushToTalk, tt.pushToTalk)
			}
			if cfg.Hotkey.Toggle != tt.toggle {
				t.Errorf("Toggle = %q, want %q", cfg.Hotkey.Toggle, tt.toggle)
			}
		})
	}
}

func TestExplicitBindingWinsOverLegacyTrigger(t *testing.T) {
	cfg, err := loadTestConfig(t, minimalModels+`hotkey:
  push_to_talk: "super+7"
  trigger: "ctrl+shift+space"
  mode: "push-to-talk"
`)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	// A stale trigger must not override a deliberate choice.
	if cfg.Hotkey.PushToTalk != "super+7" {
		t.Errorf("PushToTalk = %q, want the explicit binding preserved", cfg.Hotkey.PushToTalk)
	}
}

func TestHalfMigratedConfigKeepsBothBindings(t *testing.T) {
	// Adding one new binding to a file that still has trigger/mode must not
	// discard the trigger: the user gets a toggle and silently loses
	// push-to-talk, with nothing to indicate why.
	cfg, err := loadTestConfig(t, minimalModels+`hotkey:
  trigger: "super+8"
  mode: "push-to-talk"
  toggle: "super+9"
`)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Hotkey.PushToTalk != "super+8" {
		t.Errorf("PushToTalk = %q, want the legacy trigger migrated alongside the new toggle",
			cfg.Hotkey.PushToTalk)
	}
	if cfg.Hotkey.Toggle != "super+9" {
		t.Errorf("Toggle = %q, want the explicit binding kept", cfg.Hotkey.Toggle)
	}
}

func TestLegacyTriggerDoesNotOverwriteItsOwnBinding(t *testing.T) {
	// mode names toggle, and toggle is already set explicitly: the trigger
	// has nowhere to go and must not clobber it.
	cfg, err := loadTestConfig(t, minimalModels+`hotkey:
  trigger: "ctrl+shift+space"
  mode: "toggle"
  toggle: "super+9"
`)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Hotkey.Toggle != "super+9" {
		t.Errorf("Toggle = %q, want the explicit binding preserved", cfg.Hotkey.Toggle)
	}
}

func TestHotkeyConfigured(t *testing.T) {
	if (HotkeyConfig{}).Configured() {
		t.Error("Configured() = true with no bindings")
	}
	if !(HotkeyConfig{PushToTalk: "super+7"}).Configured() {
		t.Error("Configured() = false with a push-to-talk binding")
	}
	if !(HotkeyConfig{Toggle: "super+8"}).Configured() {
		t.Error("Configured() = false with a toggle binding")
	}
}
