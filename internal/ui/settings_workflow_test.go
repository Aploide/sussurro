package ui

import (
	"strings"
	"testing"

	"github.com/aploide/sussurro/internal/config"
)

// probeFor builds a capability probe describing a specific host.
func probeFor(goos string, installed ...string) capabilityProbe {
	tools := make(map[string]bool, len(installed))
	for _, tool := range installed {
		tools[tool] = true
	}
	return capabilityProbe{
		GOOS:           goos,
		ToolAvailable:  func(tool string) bool { return tools[tool] },
		EvdevAvailable: func() (bool, string) { return false, "requires membership of the 'input' group" },
	}
}

// findChoice locates an option by value.
func findChoice(t *testing.T, choices []choice, value string) choice {
	t.Helper()
	for _, candidate := range choices {
		if candidate.Value == value {
			return candidate
		}
	}
	t.Fatalf("no option %q among %v", value, choices)
	return choice{}
}

// defaultConfig returns a config with normalized workflow defaults.
func defaultConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Workflow.Normalize()
	return cfg
}

func TestWorkflowSettingsReflectConfiguration(t *testing.T) {
	cfg := defaultConfig()
	cfg.Workflow.Mode = config.ModeReview
	cfg.Workflow.Streaming.Enabled = true
	cfg.Workflow.Streaming.Interval = "300ms"
	cfg.Workflow.Input.Device = "Kinesis"
	cfg.Workflow.Input.Chord = "ctrl+shift+space"

	settings := buildWorkflowSettings(cfg, probeFor("linux"))

	if settings.Mode != string(config.ModeReview) {
		t.Errorf("Mode = %q, want review", settings.Mode)
	}
	if !settings.StreamingEnabled || settings.StreamingInterval != "300ms" {
		t.Errorf("streaming = %v/%q, want enabled at 300ms", settings.StreamingEnabled, settings.StreamingInterval)
	}
	if settings.InputDevice != "Kinesis" || settings.InputChord != "ctrl+shift+space" {
		t.Errorf("input = %q/%q, want the configured values", settings.InputDevice, settings.InputChord)
	}
	// Voice editing is reachable exactly when review mode is on.
	if !settings.VoiceEditing {
		t.Error("VoiceEditing = false in review mode, want true")
	}
}

func TestVoiceEditingUnavailableInImmediateMode(t *testing.T) {
	settings := buildWorkflowSettings(defaultConfig(), probeFor("linux"))

	if settings.VoiceEditing {
		t.Error("VoiceEditing = true in immediate mode, want false")
	}
}

func TestUnavailableBackendsExplainWhy(t *testing.T) {
	// This host: Linux, X11, with neither Wayland typing tool installed.
	settings := buildWorkflowSettings(defaultConfig(), probeFor("linux"))

	for _, value := range []string{string(config.DeliveryWtype), string(config.DeliveryYdotool)} {
		option := findChoice(t, settings.DeliveryBackends, value)
		if option.Available {
			t.Errorf("%s reported available with the tool missing", value)
		}
		// A bare "unavailable" leaves the user with nothing to act on.
		if !strings.Contains(option.Reason, "not installed") {
			t.Errorf("%s reason = %q, want it to name the missing tool", value, option.Reason)
		}
	}
}

func TestPortableBackendsAlwaysAvailable(t *testing.T) {
	settings := buildWorkflowSettings(defaultConfig(), probeFor("linux"))

	for _, value := range []string{string(config.DeliveryAuto), string(config.DeliveryClipboardPaste)} {
		if option := findChoice(t, settings.DeliveryBackends, value); !option.Available {
			t.Errorf("%s reported unavailable, want the portable path always offered", value)
		}
	}
}

func TestInstalledToolsBecomeAvailable(t *testing.T) {
	settings := buildWorkflowSettings(defaultConfig(), probeFor("linux", "wtype", "ydotool"))

	for _, value := range []string{string(config.DeliveryWtype), string(config.DeliveryYdotool)} {
		option := findChoice(t, settings.DeliveryBackends, value)
		if !option.Available {
			t.Errorf("%s reported unavailable with the tool installed", value)
		}
		if option.Reason != "" {
			t.Errorf("%s carries reason %q while available", value, option.Reason)
		}
	}
}

func TestLinuxOnlyBackendsMarkedOnOtherPlatforms(t *testing.T) {
	for _, goos := range []string{"darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			settings := buildWorkflowSettings(defaultConfig(), probeFor(goos, "wtype", "ydotool"))

			for _, value := range []string{
				string(config.DeliveryWtype),
				string(config.DeliveryYdotool),
			} {
				option := findChoice(t, settings.DeliveryBackends, value)
				if option.Available {
					t.Errorf("%s offered on %s, want it marked unavailable", value, goos)
				}
				if !strings.Contains(option.Reason, "Linux") {
					t.Errorf("%s reason = %q, want it to say Linux only", value, option.Reason)
				}
			}

			evdev := findChoice(t, settings.InputBackends, string(config.InputEvdev))
			if evdev.Available || !strings.Contains(evdev.Reason, "Linux") {
				t.Errorf("evdev on %s = %+v, want unavailable and marked Linux only", goos, evdev)
			}
		})
	}
}

func TestEvdevReportsPermissionDiagnostic(t *testing.T) {
	settings := buildWorkflowSettings(defaultConfig(), probeFor("linux"))

	evdev := findChoice(t, settings.InputBackends, string(config.InputEvdev))
	if evdev.Available {
		t.Fatal("evdev reported available without input group membership")
	}
	// The user needs to know it is a permission problem, not a missing feature.
	if !strings.Contains(evdev.Reason, "input") {
		t.Errorf("evdev reason = %q, want the input group named", evdev.Reason)
	}
}

func TestInputBackendChangesAreMarkedAsNeedingRestart(t *testing.T) {
	settings := buildWorkflowSettings(defaultConfig(), probeFor("linux"))

	// Input backends are wired at startup, so a live switch would mislead.
	for _, option := range settings.InputBackends {
		if !option.Restart {
			t.Errorf("input backend %q is not marked as requiring a restart", option.Value)
		}
	}
}

func TestEveryConfiguredValueIsOffered(t *testing.T) {
	settings := buildWorkflowSettings(defaultConfig(), probeFor("linux"))

	// A value the config accepts but the UI never lists is unreachable.
	for _, value := range []string{string(config.ModeImmediate), string(config.ModeReview)} {
		findChoice(t, settings.Modes, value)
	}
	for _, value := range []string{
		string(config.InputAuto), string(config.InputNative),
		string(config.InputTrigger), string(config.InputEvdev),
	} {
		findChoice(t, settings.InputBackends, value)
	}
	for _, value := range []string{
		string(config.DeliveryAuto), string(config.DeliveryClipboardPaste),
		string(config.DeliveryWtype), string(config.DeliveryYdotool),
	} {
		findChoice(t, settings.DeliveryBackends, value)
	}
}

func TestApplyWorkflowFieldSetsEveryKey(t *testing.T) {
	tests := []struct {
		key    string
		value  string
		verify func(*testing.T, config.WorkflowConfig)
	}{
		{
			key: "workflow.mode", value: "review",
			verify: func(t *testing.T, w config.WorkflowConfig) {
				if w.Mode != config.ModeReview {
					t.Errorf("Mode = %q, want review", w.Mode)
				}
			},
		},
		{
			key: "workflow.streaming.enabled", value: "true",
			verify: func(t *testing.T, w config.WorkflowConfig) {
				if !w.Streaming.Enabled {
					t.Error("Streaming.Enabled = false, want true")
				}
			},
		},
		{
			key: "workflow.streaming.interval", value: "400ms",
			verify: func(t *testing.T, w config.WorkflowConfig) {
				if w.Streaming.Interval != "400ms" {
					t.Errorf("Interval = %q, want 400ms", w.Streaming.Interval)
				}
			},
		},
		{
			key: "workflow.input.backend", value: "native",
			verify: func(t *testing.T, w config.WorkflowConfig) {
				if w.Input.Backend != config.InputNative {
					t.Errorf("Input.Backend = %q, want native", w.Input.Backend)
				}
			},
		},
		{
			key: "workflow.input.device", value: "Kinesis",
			verify: func(t *testing.T, w config.WorkflowConfig) {
				if w.Input.Device != "Kinesis" {
					t.Errorf("Device = %q, want Kinesis", w.Input.Device)
				}
			},
		},
		{
			key: "workflow.input.chord", value: "ctrl+alt+space",
			verify: func(t *testing.T, w config.WorkflowConfig) {
				if w.Input.Chord != "ctrl+alt+space" {
					t.Errorf("Chord = %q, want ctrl+alt+space", w.Input.Chord)
				}
			},
		},
		{
			key: "workflow.input.cancel_chord", value: "ctrl+shift+alt",
			verify: func(t *testing.T, w config.WorkflowConfig) {
				if w.Input.CancelChord != "ctrl+shift+alt" {
					t.Errorf("CancelChord = %q, want ctrl+shift+alt", w.Input.CancelChord)
				}
			},
		},
		{
			key: "workflow.delivery.backend", value: "wtype",
			verify: func(t *testing.T, w config.WorkflowConfig) {
				if w.Delivery.Backend != config.DeliveryWtype {
					t.Errorf("Delivery.Backend = %q, want wtype", w.Delivery.Backend)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			var workflow config.WorkflowConfig
			workflow.Normalize()
			if err := applyWorkflowField(&workflow, tt.key, tt.value); err != nil {
				t.Fatalf("applyWorkflowField() error = %v", err)
			}
			tt.verify(t, workflow)
		})
	}
}

func TestApplyWorkflowFieldRejectsUnknownKey(t *testing.T) {
	var workflow config.WorkflowConfig
	if err := applyWorkflowField(&workflow, "workflow.telepathy", "on"); err == nil {
		t.Fatal("applyWorkflowField() error = nil, want a rejection")
	}
}

func TestSaveWorkflowSettingRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "unknown mode", key: "workflow.mode", value: "instant"},
		{name: "unknown input backend", key: "workflow.input.backend", value: "libinput"},
		{name: "unknown delivery backend", key: "workflow.delivery.backend", value: "telepathy"},
		{name: "unparseable interval", key: "workflow.streaming.interval", value: "soon"},
		{name: "out of range interval", key: "workflow.streaming.interval", value: "5ms"},
		{name: "malformed chord", key: "workflow.input.chord", value: "ctrl++space"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			before := cfg.Workflow

			// Validation must reject before anything is written to disk, so a
			// missing config file cannot mask a validation failure.
			err := saveWorkflowSetting(cfg, tt.key, tt.value)
			if err == nil {
				t.Fatal("saveWorkflowSetting() error = nil, want a validation error")
			}
			if cfg.Workflow != before {
				t.Errorf("config changed to %+v despite the rejection", cfg.Workflow)
			}
		})
	}
}

func TestSaveWorkflowSettingUsesTheAuthoritativeValidator(t *testing.T) {
	cfg := defaultConfig()

	// The same value a config file would reject must be rejected here, with
	// the same message, rather than by a second set of rules.
	err := saveWorkflowSetting(cfg, "workflow.mode", "instant")
	if err == nil {
		t.Fatal("saveWorkflowSetting() error = nil, want a rejection")
	}
	if !strings.Contains(err.Error(), "workflow.mode") {
		t.Errorf("error %q does not name the setting", err)
	}
	if !strings.Contains(err.Error(), "immediate") {
		t.Errorf("error %q does not list the accepted values", err)
	}
}

func TestYAMLScalarQuotesStringsAndNotBooleans(t *testing.T) {
	if got := yamlScalar("workflow.streaming.enabled", "true"); got != "true" {
		t.Errorf("boolean rendered as %q, want an unquoted true", got)
	}
	if got := yamlScalar("workflow.streaming.enabled", "false"); got != "false" {
		t.Errorf("boolean rendered as %q, want an unquoted false", got)
	}
	got := yamlScalar("workflow.mode", "review")
	if !strings.Contains(got, "review") || got == "review" {
		t.Errorf("string rendered as %q, want it quoted", got)
	}
}
