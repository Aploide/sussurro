package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// minimalModels supplies the sections a config needs besides the one under
// test, so a focused fragment still loads.
const minimalModels = `app:
  log_level: "info"
audio:
  sample_rate: 16000
models:
  asr:
    path: "models/asr.bin"
  llm:
    path: "models/llm.gguf"
`

// reload parses a YAML document the way LoadConfig does, so a test asserts on
// the value the application would actually see rather than on text.
func reload(t *testing.T, content string) *Config {
	t.Helper()
	cfg, err := loadTestConfig(t, content)
	if err != nil {
		t.Fatalf("reloading edited config: %v\n---\n%s", err, content)
	}
	return cfg
}

func TestSetNestedValueUpdatesExistingKey(t *testing.T) {
	const original = `workflow:
  mode: "immediate"
  input:
    backend: "auto"
`
	updated, err := SetNestedValue(original, "workflow.input.backend", `"evdev"`)
	if err != nil {
		t.Fatalf("SetNestedValue() error = %v", err)
	}

	cfg := reload(t, legacyConfig+updated)
	if cfg.Workflow.Input.Backend != InputEvdev {
		t.Errorf("Input.Backend = %q, want evdev", cfg.Workflow.Input.Backend)
	}
	// The sibling key must be untouched.
	if cfg.Workflow.Mode != ModeImmediate {
		t.Errorf("Mode = %q, want immediate", cfg.Workflow.Mode)
	}
}

func TestSetNestedValueDistinguishesLikeNamedKeys(t *testing.T) {
	// Both sections contain a "backend" line, and the one that must NOT change
	// comes first, so a search that ignores structure edits the wrong key.
	const original = `workflow:
  input:
    backend: "auto"
  delivery:
    backend: "auto"
`
	updated, err := SetNestedValue(original, "workflow.delivery.backend", `"wtype"`)
	if err != nil {
		t.Fatalf("SetNestedValue() error = %v", err)
	}

	cfg := reload(t, legacyConfig+updated)
	if cfg.Workflow.Delivery.Backend != DeliveryWtype {
		t.Errorf("Delivery.Backend = %q, want wtype\n%s", cfg.Workflow.Delivery.Backend, updated)
	}
	if cfg.Workflow.Input.Backend != InputAuto {
		t.Errorf("Input.Backend = %q, want the earlier section untouched\n%s",
			cfg.Workflow.Input.Backend, updated)
	}
}

func TestSetNestedValueIgnoresLikeNamedKeyInAnotherSection(t *testing.T) {
	// hotkey.mode precedes workflow.mode in the document. Addressing
	// workflow.mode must not rewrite the hotkey one.
	const original = `hotkey:
  trigger: "ctrl+shift+space"
  mode: "push-to-talk"

workflow:
  mode: "immediate"
`
	updated, err := SetNestedValue(original, "workflow.mode", `"review"`)
	if err != nil {
		t.Fatalf("SetNestedValue() error = %v", err)
	}

	cfg := reload(t, minimalModels+updated)
	if cfg.Workflow.Mode != ModeReview {
		t.Errorf("Workflow.Mode = %q, want review\n%s", cfg.Workflow.Mode, updated)
	}
	if cfg.Hotkey.Mode != "push-to-talk" {
		t.Errorf("Hotkey.Mode = %q, want the earlier like-named key untouched\n%s",
			cfg.Hotkey.Mode, updated)
	}
}

func TestSetNestedValueDoesNotReachIntoALaterSection(t *testing.T) {
	// workflow.mode is absent, but hotkey.mode appears AFTER the workflow
	// block. An unbounded search would rewrite the hotkey key instead of
	// creating the workflow one.
	const original = `workflow:
  input:
    backend: "auto"

hotkey:
  trigger: "ctrl+shift+space"
  mode: "push-to-talk"
`
	updated, err := SetNestedValue(original, "workflow.mode", `"review"`)
	if err != nil {
		t.Fatalf("SetNestedValue() error = %v", err)
	}

	cfg := reload(t, minimalModels+updated)
	if cfg.Workflow.Mode != ModeReview {
		t.Errorf("Workflow.Mode = %q, want review\n%s", cfg.Workflow.Mode, updated)
	}
	if cfg.Hotkey.Mode != "push-to-talk" {
		t.Errorf("Hotkey.Mode = %q, want the later like-named key untouched\n%s",
			cfg.Hotkey.Mode, updated)
	}
}

func TestSetNestedValueDoesNotMatchADeeperKeyOfTheSameName(t *testing.T) {
	// A key nested one level deeper must not satisfy a request for the
	// shallower one. Matching it would also strand the block it lives in.
	const original = `workflow:
  input:
    backend: "evdev"
    device: "Kinesis"
`
	updated, err := SetNestedValue(original, "workflow.device", `"wrong"`)
	if err != nil {
		t.Fatalf("SetNestedValue() error = %v", err)
	}

	cfg := reload(t, minimalModels+updated)
	// The nested block must survive intact, not be collapsed or re-levelled.
	if cfg.Workflow.Input.Backend != InputEvdev {
		t.Errorf("Input.Backend = %q, want evdev preserved\n%s", cfg.Workflow.Input.Backend, updated)
	}
	if cfg.Workflow.Input.Device != "Kinesis" {
		t.Errorf("Input.Device = %q, want the deeper key untouched\n%s",
			cfg.Workflow.Input.Device, updated)
	}
}

func TestSetNestedValueCreatesMissingSections(t *testing.T) {
	// A pre-review config has no workflow section at all.
	updated, err := SetNestedValue(legacyConfig, "workflow.input.backend", `"evdev"`)
	if err != nil {
		t.Fatalf("SetNestedValue() error = %v", err)
	}

	cfg := reload(t, updated)
	if cfg.Workflow.Input.Backend != InputEvdev {
		t.Errorf("Input.Backend = %q, want evdev", cfg.Workflow.Input.Backend)
	}
	// Everything that was already there must survive.
	if cfg.Hotkey.Trigger != "ctrl+shift+space" {
		t.Errorf("Hotkey.Trigger = %q, want it preserved", cfg.Hotkey.Trigger)
	}
	if cfg.Audio.SampleRate != 16000 {
		t.Errorf("Audio.SampleRate = %d, want it preserved", cfg.Audio.SampleRate)
	}
}

func TestSetNestedValueCreatesMissingLeafInExistingSection(t *testing.T) {
	const original = `workflow:
  mode: "review"
  input:
    backend: "evdev"
`
	updated, err := SetNestedValue(original, "workflow.input.device", `"Kinesis"`)
	if err != nil {
		t.Fatalf("SetNestedValue() error = %v", err)
	}

	cfg := reload(t, legacyConfig+updated)
	if cfg.Workflow.Input.Device != "Kinesis" {
		t.Errorf("Input.Device = %q, want Kinesis", cfg.Workflow.Input.Device)
	}
	if cfg.Workflow.Input.Backend != InputEvdev {
		t.Errorf("Input.Backend = %q, want it preserved", cfg.Workflow.Input.Backend)
	}
}

func TestSetNestedValuePreservesComments(t *testing.T) {
	const original = `workflow:
  # Which interaction mode to use.
  mode: "immediate"
  input:
    # Which input backend to use.
    backend: "auto"
`
	updated, err := SetNestedValue(original, "workflow.input.backend", `"native"`)
	if err != nil {
		t.Fatalf("SetNestedValue() error = %v", err)
	}

	for _, comment := range []string{"Which interaction mode to use.", "Which input backend to use."} {
		if !strings.Contains(updated, comment) {
			t.Errorf("edited document dropped the comment %q", comment)
		}
	}
}

func TestSetNestedValueHandlesBooleans(t *testing.T) {
	updated, err := SetNestedValue(legacyConfig, "workflow.streaming.enabled", YAMLBool(true))
	if err != nil {
		t.Fatalf("SetNestedValue() error = %v", err)
	}

	cfg := reload(t, updated)
	if !cfg.Workflow.Streaming.Enabled {
		t.Error("Streaming.Enabled = false, want true")
	}
}

func TestSetNestedValueRoundTripsEveryWorkflowKey(t *testing.T) {
	tests := []struct {
		key    string
		value  string
		verify func(*testing.T, *Config)
	}{
		{
			key:   "workflow.mode",
			value: `"review"`,
			verify: func(t *testing.T, cfg *Config) {
				if cfg.Workflow.Mode != ModeReview {
					t.Errorf("Mode = %q, want review", cfg.Workflow.Mode)
				}
			},
		},
		{
			key:   "workflow.streaming.interval",
			value: `"250ms"`,
			verify: func(t *testing.T, cfg *Config) {
				if got := cfg.Workflow.StreamingInterval().String(); got != "250ms" {
					t.Errorf("interval = %s, want 250ms", got)
				}
			},
		},
		{
			key:   "workflow.input.chord",
			value: `"ctrl+alt+space"`,
			verify: func(t *testing.T, cfg *Config) {
				if cfg.Workflow.Input.Chord != "ctrl+alt+space" {
					t.Errorf("Chord = %q, want ctrl+alt+space", cfg.Workflow.Input.Chord)
				}
			},
		},
		{
			key:   "workflow.input.cancel_chord",
			value: `"ctrl+shift+alt"`,
			verify: func(t *testing.T, cfg *Config) {
				if cfg.Workflow.Input.CancelChord != "ctrl+shift+alt" {
					t.Errorf("CancelChord = %q, want ctrl+shift+alt", cfg.Workflow.Input.CancelChord)
				}
			},
		},
		{
			key:   "workflow.delivery.backend",
			value: `"ydotool"`,
			verify: func(t *testing.T, cfg *Config) {
				if cfg.Workflow.Delivery.Backend != DeliveryYdotool {
					t.Errorf("Delivery.Backend = %q, want ydotool", cfg.Workflow.Delivery.Backend)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			updated, err := SetNestedValue(legacyConfig, tt.key, tt.value)
			if err != nil {
				t.Fatalf("SetNestedValue() error = %v", err)
			}
			tt.verify(t, reload(t, updated))
		})
	}
}

func TestSetNestedValueIsIdempotent(t *testing.T) {
	first, err := SetNestedValue(legacyConfig, "workflow.mode", `"review"`)
	if err != nil {
		t.Fatalf("SetNestedValue() error = %v", err)
	}
	second, err := SetNestedValue(first, "workflow.mode", `"review"`)
	if err != nil {
		t.Fatalf("SetNestedValue() error = %v", err)
	}

	// Writing the same value twice must not duplicate the key.
	if first != second {
		t.Errorf("second write changed the document:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if count := strings.Count(second, "mode:"); count != 1 {
		t.Errorf("document contains %d mode: keys, want 1", count)
	}
}

func TestSetNestedValueSurvivesRepeatedEdits(t *testing.T) {
	document := legacyConfig
	edits := []struct{ key, value string }{
		{key: "workflow.mode", value: `"review"`},
		{key: "workflow.streaming.enabled", value: "true"},
		{key: "workflow.streaming.interval", value: `"300ms"`},
		{key: "workflow.input.backend", value: `"evdev"`},
		{key: "workflow.input.device", value: `"Kinesis"`},
		{key: "workflow.delivery.backend", value: `"clipboard-paste"`},
	}

	for _, edit := range edits {
		updated, err := SetNestedValue(document, edit.key, edit.value)
		if err != nil {
			t.Fatalf("SetNestedValue(%s) error = %v", edit.key, err)
		}
		document = updated
	}

	cfg := reload(t, document)
	if cfg.Workflow.Mode != ModeReview || !cfg.Workflow.Streaming.Enabled ||
		cfg.Workflow.Input.Backend != InputEvdev || cfg.Workflow.Input.Device != "Kinesis" ||
		cfg.Workflow.Delivery.Backend != DeliveryClipboardPaste {
		t.Errorf("after six edits the config is %+v, want every edit applied", cfg.Workflow)
	}
	if got := cfg.Workflow.StreamingInterval().String(); got != "300ms" {
		t.Errorf("interval = %s, want 300ms", got)
	}
}

func TestSetNestedValueRejectsEmptyKey(t *testing.T) {
	if _, err := SetNestedValue(legacyConfig, "", `"x"`); err == nil {
		t.Fatal("SetNestedValue() error = nil, want a rejection")
	}
}

func TestSetNestedValueEditsTheShippedDefaults(t *testing.T) {
	// The shipped file is the realistic input: deeply commented and complete.
	viper.Reset()
	body := readShippedDefaults(t)

	updated, err := SetNestedValue(body, "workflow.input.backend", `"native"`)
	if err != nil {
		t.Fatalf("SetNestedValue() error = %v", err)
	}

	cfg := reload(t, updated)
	if cfg.Workflow.Input.Backend != InputNative {
		t.Errorf("Input.Backend = %q, want native", cfg.Workflow.Input.Backend)
	}
	// Nothing else in a fully-populated file may shift.
	if cfg.Workflow.Delivery.Backend != DeliveryAuto || cfg.Workflow.Mode != ModeImmediate {
		t.Errorf("workflow = %+v, want only the input backend changed", cfg.Workflow)
	}
	if cfg.Models.LLM.ContextSize != 4096 {
		t.Errorf("Models.LLM.ContextSize = %d, want it preserved", cfg.Models.LLM.ContextSize)
	}
}
