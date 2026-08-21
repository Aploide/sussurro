package config

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// InteractionMode selects how transcribed text reaches the focused window.
type InteractionMode string

const (
	// ModeImmediate delivers each result as soon as recognition completes.
	// This is upstream's original behavior and remains the default.
	ModeImmediate InteractionMode = "immediate"
	// ModeReview holds results for inspection, editing, and explicit delivery.
	ModeReview InteractionMode = "review"
)

// InputBackend selects the source of recording gestures.
type InputBackend string

const (
	// InputAuto picks the best backend available on the host at runtime.
	InputAuto InputBackend = "auto"
	// InputNative uses the in-process global hotkey listener.
	InputNative InputBackend = "native"
	// InputTrigger accepts commands over the local trigger socket, for
	// compositors that own their own key bindings.
	InputTrigger InputBackend = "trigger"
	// InputEvdev reads key events directly from Linux input devices. Requires
	// membership of the input group, so it stays opt-in.
	InputEvdev InputBackend = "evdev"
)

// DeliveryBackend selects how reviewed text is inserted.
type DeliveryBackend string

const (
	// DeliveryAuto picks the best backend available on the host at runtime.
	DeliveryAuto DeliveryBackend = "auto"
	// DeliveryClipboardPaste stages text on the clipboard and synthesizes a
	// paste keystroke. Upstream's default; works on X11, macOS, and Windows.
	DeliveryClipboardPaste DeliveryBackend = "clipboard-paste"
	// DeliveryWtype types text through the Wayland virtual keyboard protocol.
	DeliveryWtype DeliveryBackend = "wtype"
	// DeliveryYdotool types text through the ydotool uinput daemon.
	DeliveryYdotool DeliveryBackend = "ydotool"
	// DeliveryClipboardOnly copies the text and pastes nothing, leaving the
	// user to place it themselves.
	DeliveryClipboardOnly DeliveryBackend = "clipboard-only"
)

// Defaults for the streaming review workflow. Every default reproduces
// upstream's pre-review behavior so existing configs are unaffected.
const (
	DefaultInteractionMode   = ModeImmediate
	DefaultStreamingEnabled  = true
	DefaultStreamingInterval = "750ms"
	DefaultInputBackend      = InputAuto
	DefaultDeliveryBackend   = DeliveryAuto
	// Pasting automatically is the existing behavior, so it stays the default.
	DefaultClipboardOnly = false

	// minStreamingInterval bounds partial transcription so it cannot starve
	// the final transcription of CPU on modest hardware.
	minStreamingInterval = 100 * time.Millisecond
	maxStreamingInterval = 10 * time.Second
)

var (
	interactionModes = []InteractionMode{ModeImmediate, ModeReview}
	inputBackends    = []InputBackend{InputAuto, InputNative, InputTrigger, InputEvdev}
	deliveryBackends = []DeliveryBackend{DeliveryAuto, DeliveryClipboardPaste, DeliveryWtype, DeliveryYdotool, DeliveryClipboardOnly}
)

// WorkflowConfig holds the opt-in streaming review settings. An absent
// workflow section leaves every field at its zero value, which Normalize
// resolves to the backward-compatible defaults.
type WorkflowConfig struct {
	Mode      InteractionMode `mapstructure:"mode"`
	Streaming StreamingConfig `mapstructure:"streaming"`
	Input     InputConfig     `mapstructure:"input"`
	Delivery  DeliveryConfig  `mapstructure:"delivery"`
}

// StreamingConfig controls bounded partial transcription during recording.
type StreamingConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// Interval is the minimum gap between partial transcription passes,
	// as a Go duration string.
	Interval string `mapstructure:"interval"`
}

// InputConfig selects the recording gesture source.
type InputConfig struct {
	Backend InputBackend `mapstructure:"backend"`
	// Device selects the evdev input device by name substring or exact path.
	// Empty picks the first stable keyboard. Ignored by other backends.
	Device string `mapstructure:"device"`
	// Chord is the key combination that drives recording, in the same
	// notation as hotkey.trigger. Empty follows hotkey.trigger.
	Chord string `mapstructure:"chord"`
	// CancelChord abandons the session in review mode. Empty disables it.
	CancelChord string `mapstructure:"cancel_chord"`
}

// DeliveryConfig selects the text insertion backend.
type DeliveryConfig struct {
	Backend DeliveryBackend `mapstructure:"backend"`
	// ClipboardOnly is the superseded boolean form of
	// Backend: clipboard-only. Splitting "whether to insert" from "how to
	// insert" produced a dropdown that did nothing whenever the box was
	// ticked, so the choice is now a single control. Still read so existing
	// configs keep working; Normalize folds it into Backend.
	ClipboardOnly bool `mapstructure:"clipboard_only"`
}

// ReviewEnabled reports whether results should be held for explicit delivery.
func (w WorkflowConfig) ReviewEnabled() bool { return w.Mode == ModeReview }

// StreamingInterval returns the validated partial transcription interval.
// Call Validate first; an unparseable interval falls back to the default.
func (w WorkflowConfig) StreamingInterval() time.Duration {
	d, err := time.ParseDuration(w.Streaming.Interval)
	if err != nil {
		d, _ = time.ParseDuration(DefaultStreamingInterval)
	}
	return d
}

// Normalize fills empty fields with the backward-compatible defaults. It is
// applied before validation so an old config with no workflow section is
// indistinguishable from an explicit immediate-mode config.
func (w *WorkflowConfig) Normalize() {
	if w.Mode == "" {
		w.Mode = DefaultInteractionMode
	}
	if w.Streaming.Interval == "" {
		w.Streaming.Interval = DefaultStreamingInterval
	}
	if w.Input.Backend == "" {
		w.Input.Backend = DefaultInputBackend
	}
	if w.Delivery.Backend == "" {
		w.Delivery.Backend = DefaultDeliveryBackend
	}
	// An existing config's clipboard_only: true must keep behaving the same.
	// It only speaks when the backend was left at its default, so an explicit
	// backend choice is never silently overridden.
	if w.Delivery.ClipboardOnly && w.Delivery.Backend == DefaultDeliveryBackend {
		w.Delivery.Backend = DeliveryClipboardOnly
	}
}

// Validate reports the first invalid workflow setting. Errors name the key,
// the rejected value, and the accepted values so they are actionable without
// consulting documentation.
func (w WorkflowConfig) Validate() error {
	if !containsValue(interactionModes, w.Mode) {
		return enumError("workflow.mode", string(w.Mode), interactionModes)
	}
	if !containsValue(inputBackends, w.Input.Backend) {
		return enumError("workflow.input.backend", string(w.Input.Backend), inputBackends)
	}
	if !containsValue(deliveryBackends, w.Delivery.Backend) {
		return enumError("workflow.delivery.backend", string(w.Delivery.Backend), deliveryBackends)
	}

	if err := validateChordSpec("workflow.input.chord", w.Input.Chord); err != nil {
		return err
	}
	if err := validateChordSpec("workflow.input.cancel_chord", w.Input.CancelChord); err != nil {
		return err
	}

	interval, err := time.ParseDuration(w.Streaming.Interval)
	if err != nil {
		return fmt.Errorf("workflow.streaming.interval: %q is not a duration; use a value like %q",
			w.Streaming.Interval, DefaultStreamingInterval)
	}
	if interval < minStreamingInterval || interval > maxStreamingInterval {
		return fmt.Errorf("workflow.streaming.interval: %s is outside the supported range %s-%s",
			interval, minStreamingInterval, maxStreamingInterval)
	}

	return nil
}

// containsValue reports whether allowed contains value.
func containsValue[T ~string](allowed []T, value T) bool {
	for _, candidate := range allowed {
		if candidate == value {
			return true
		}
	}
	return false
}

// enumError builds an actionable error naming the accepted values.
func enumError[T ~string](key, value string, allowed []T) error {
	names := make([]string, 0, len(allowed))
	for _, candidate := range allowed {
		names = append(names, string(candidate))
	}
	sort.Strings(names)
	if value == "" {
		return fmt.Errorf("%s: value is empty; use one of: %s", key, strings.Join(names, ", "))
	}
	return fmt.Errorf("%s: %q is not supported; use one of: %s", key, value, strings.Join(names, ", "))
}

// workflowEnvKeys maps each workflow config key to its SUSSURRO_ environment
// variable. Explicit binding is required because viper's AutomaticEnv does not
// expose keys to Unmarshal unless they exist in the config file.
var workflowEnvKeys = map[string]string{
	"workflow.mode":                    "SUSSURRO_WORKFLOW_MODE",
	"workflow.streaming.enabled":       "SUSSURRO_WORKFLOW_STREAMING_ENABLED",
	"workflow.streaming.interval":      "SUSSURRO_WORKFLOW_STREAMING_INTERVAL",
	"workflow.input.backend":           "SUSSURRO_WORKFLOW_INPUT_BACKEND",
	"workflow.input.device":            "SUSSURRO_WORKFLOW_INPUT_DEVICE",
	"workflow.input.chord":             "SUSSURRO_WORKFLOW_INPUT_CHORD",
	"workflow.input.cancel_chord":      "SUSSURRO_WORKFLOW_INPUT_CANCEL_CHORD",
	"workflow.delivery.backend":        "SUSSURRO_WORKFLOW_DELIVERY_BACKEND",
	"workflow.delivery.clipboard_only": "SUSSURRO_WORKFLOW_DELIVERY_CLIPBOARD_ONLY",
}

// setWorkflowDefaults registers the backward-compatible workflow defaults.
func setWorkflowDefaults(v *viper.Viper) {
	v.SetDefault("workflow.mode", string(DefaultInteractionMode))
	v.SetDefault("workflow.streaming.enabled", DefaultStreamingEnabled)
	v.SetDefault("workflow.streaming.interval", DefaultStreamingInterval)
	v.SetDefault("workflow.input.backend", string(DefaultInputBackend))
	v.SetDefault("workflow.delivery.backend", string(DefaultDeliveryBackend))
	v.SetDefault("workflow.delivery.clipboard_only", DefaultClipboardOnly)
}

// bindWorkflowEnv binds every workflow key to its environment variable.
func bindWorkflowEnv(v *viper.Viper) error {
	for key, env := range workflowEnvKeys {
		if err := v.BindEnv(key, env); err != nil {
			return fmt.Errorf("binding %s: %w", env, err)
		}
	}
	return nil
}

// validateChordSpec checks a chord string is syntactically usable. The key
// names themselves are resolved by the input backend, which owns the keymap;
// this catches the malformed shapes early, at load time. An empty value is
// valid and means "follow hotkey.trigger".
func validateChordSpec(key, spec string) error {
	if strings.TrimSpace(spec) == "" {
		return nil
	}

	parts := strings.Split(spec, "+")
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			return fmt.Errorf("%s: %q has an empty component; use a form like \"ctrl+shift+space\"", key, spec)
		}
		if seen[name] {
			return fmt.Errorf("%s: %q names %q more than once", key, spec, name)
		}
		seen[name] = true
	}
	return nil
}

// ClipboardOnlyDelivery reports whether delivery should stage text without
// pasting it.
func (w WorkflowConfig) ClipboardOnlyDelivery() bool {
	return w.Delivery.Backend == DeliveryClipboardOnly
}
