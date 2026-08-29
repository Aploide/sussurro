package ui

import (
	"fmt"
	"runtime"

	"github.com/aploide/sussurro/internal/config"
	"github.com/aploide/sussurro/internal/delivery"
)

// choice is one selectable option in the settings UI. Unavailable options are
// shown with the reason rather than hidden, so a user can tell the difference
// between "not offered here" and "needs something installed".
type choice struct {
	Value     string `json:"value"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	// Reason explains why an option is unavailable. Empty when available.
	Reason string `json:"reason,omitempty"`
	// Restart marks options that only take effect after a restart.
	Restart bool `json:"restart,omitempty"`
}

// workflowSettings is the review-workflow section of the settings model.
type workflowSettings struct {
	Mode              string   `json:"mode"`
	Modes             []choice `json:"modes"`
	StreamingEnabled  bool     `json:"streamingEnabled"`
	StreamingInterval string   `json:"streamingInterval"`
	InputBackend      string   `json:"inputBackend"`
	InputBackends     []choice `json:"inputBackends"`
	InputDevice       string   `json:"inputDevice"`
	InputChord        string   `json:"inputChord"`
	InputCancelChord  string   `json:"inputCancelChord"`
	DeliveryBackend   string   `json:"deliveryBackend"`
	DeliveryBackends  []choice `json:"deliveryBackends"`
	// VoiceEditing reports whether review mode's voice editing is reachable.
	// It has no separate switch: it is available exactly when review mode is.
	VoiceEditing bool `json:"voiceEditing"`
}

// capabilityProbe reports what the host supports. Injected so the settings
// model is testable without depending on the machine running the tests.
type capabilityProbe struct {
	// ToolAvailable reports whether an executable is on PATH.
	ToolAvailable func(tool string) bool
	// GOOS is the target operating system.
	GOOS string
	// EvdevAvailable reports whether /dev/input can be read.
	EvdevAvailable func() (bool, string)
}

// buildWorkflowSettings renders the current workflow configuration together
// with what this host can actually do.
func buildWorkflowSettings(cfg *config.Config, probe capabilityProbe) workflowSettings {
	workflow := cfg.Workflow

	return workflowSettings{
		Mode:              string(workflow.Mode),
		Modes:             interactionModeChoices(),
		StreamingEnabled:  workflow.Streaming.Enabled,
		StreamingInterval: workflow.Streaming.Interval,
		InputBackend:      string(workflow.Input.Backend),
		InputBackends:     inputBackendChoices(probe),
		InputDevice:       workflow.Input.Device,
		InputChord:        workflow.Input.Chord,
		InputCancelChord:  workflow.Input.CancelChord,
		DeliveryBackend:   string(workflow.Delivery.Backend),
		DeliveryBackends:  deliveryBackendChoices(probe),
		VoiceEditing:      workflow.ReviewEnabled(),
	}
}

// interactionModeChoices lists the interaction modes. Both are available
// everywhere; review changes behaviour rather than requiring anything.
func interactionModeChoices() []choice {
	return []choice{
		{Value: string(config.ModeImmediate), Label: "Immediate", Available: true},
		{Value: string(config.ModeReview), Label: "Review", Available: true},
	}
}

// inputBackendChoices lists the input backends with host availability.
func inputBackendChoices(probe capabilityProbe) []choice {
	evdevAvailable, evdevReason := true, ""
	if probe.GOOS != "linux" {
		evdevAvailable, evdevReason = false, "Linux only"
	} else if probe.EvdevAvailable != nil {
		evdevAvailable, evdevReason = probe.EvdevAvailable()
	}

	return []choice{
		{Value: string(config.InputAuto), Label: "Automatic", Available: true, Restart: true},
		{Value: string(config.InputNative), Label: "Native hotkey", Available: true, Restart: true},
		{Value: string(config.InputTrigger), Label: "Compositor trigger", Available: true, Restart: true},
		{
			Value:     string(config.InputEvdev),
			Label:     "Linux input devices (evdev)",
			Available: evdevAvailable,
			Reason:    evdevReason,
			Restart:   true,
		},
	}
}

// deliveryBackendChoices lists the delivery backends with host availability.
// A tool that is not installed is reported rather than silently unselectable,
// because installing it is the fix.
func deliveryBackendChoices(probe capabilityProbe) []choice {
	toolChoice := func(value config.DeliveryBackend, label, tool string) choice {
		if probe.GOOS != "linux" {
			return choice{Value: string(value), Label: label, Reason: "Linux only"}
		}
		if probe.ToolAvailable != nil && probe.ToolAvailable(tool) {
			return choice{Value: string(value), Label: label, Available: true}
		}
		return choice{Value: string(value), Label: label, Reason: fmt.Sprintf("%s is not installed", tool)}
	}

	return []choice{
		{Value: string(config.DeliveryAuto), Label: "Automatic", Available: true},
		{Value: string(config.DeliveryClipboardPaste), Label: "Clipboard paste", Available: true},
		// Sits with the other clipboard option and ahead of the tool-specific
		// ones: it is always available, whereas those depend on what the host
		// has installed. Not a way of inserting text but a decision not to,
		// which is why it belongs in this list at all — offering it separately
		// produced a dropdown that did nothing whenever the other control was
		// set.
		{
			Value:     string(config.DeliveryClipboardOnly),
			Label:     "Copy to clipboard, don't paste",
			Available: true,
		},
		toolChoice(config.DeliveryWtype, "wtype (Wayland)", "wtype"),
		toolChoice(config.DeliveryYdotool, "ydotool", "ydotool"),
	}
}

// hostCapabilities probes the machine the application is running on.
func hostCapabilities() capabilityProbe {
	return capabilityProbe{
		GOOS:           runtime.GOOS,
		ToolAvailable:  delivery.ToolAvailable,
		EvdevAvailable: evdevAvailable,
	}
}

// saveWorkflowSetting validates a workflow change against the authoritative
// config validator, then persists it. Validation runs on a copy of the whole
// workflow section so a change is rejected for the same reasons a config file
// would be, rather than by a second, drifting set of rules.
func saveWorkflowSetting(cfg *config.Config, key, value string) error {
	updated := cfg.Workflow
	if err := applyWorkflowField(&updated, key, value); err != nil {
		return err
	}
	updated.Normalize()
	if err := updated.Validate(); err != nil {
		return err
	}

	if err := config.SaveWorkflowValue(cfg, key, yamlScalar(key, value)); err != nil {
		return err
	}
	cfg.Workflow = updated
	return nil
}

// applyWorkflowField sets one field on a workflow config by its dotted key.
func applyWorkflowField(workflow *config.WorkflowConfig, key, value string) error {
	switch key {
	case "workflow.mode":
		workflow.Mode = config.InteractionMode(value)
	case "workflow.streaming.enabled":
		workflow.Streaming.Enabled = value == "true"
	case "workflow.streaming.interval":
		workflow.Streaming.Interval = value
	case "workflow.input.backend":
		workflow.Input.Backend = config.InputBackend(value)
	case "workflow.input.device":
		workflow.Input.Device = value
	case "workflow.input.chord":
		workflow.Input.Chord = value
	case "workflow.input.cancel_chord":
		workflow.Input.CancelChord = value
	case "workflow.delivery.backend":
		workflow.Delivery.Backend = config.DeliveryBackend(value)
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
	return nil
}

// yamlScalar renders a value for the config file, quoting everything except
// the one boolean setting.
func yamlScalar(key, value string) string {
	if key == "workflow.streaming.enabled" {
		return config.YAMLBool(value == "true")
	}
	return config.YAMLString(value)
}
