package delivery

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// BackendName identifies a delivery backend. The values match the
// workflow.delivery.backend configuration keys, without importing config here:
// delivery is a leaf package that configuration selects, not the reverse.
type BackendName string

const (
	// BackendAuto picks the best backend available on the host.
	BackendAuto BackendName = "auto"
	// BackendClipboardPaste stages text and synthesizes a paste keystroke.
	BackendClipboardPaste BackendName = "clipboard-paste"
	// BackendWtype types through the Wayland virtual keyboard protocol.
	BackendWtype BackendName = "wtype"
	// BackendYdotool types through the ydotool uinput daemon.
	BackendYdotool BackendName = "ydotool"
)

// Action is an explicit delivery request from the review workflow.
type Action uint8

const (
	// ActionDeliver inserts the exact text and nothing else.
	ActionDeliver Action = iota
	// ActionDeliverAndSubmit inserts the exact text and then sends Enter.
	ActionDeliverAndSubmit
	actionCount
)

// Valid reports whether action is a defined delivery action.
func (action Action) Valid() bool { return action < actionCount }

func (action Action) String() string {
	switch action {
	case ActionDeliver:
		return "deliver"
	case ActionDeliverAndSubmit:
		return "deliver-and-submit"
	default:
		return "invalid"
	}
}

// Submits reports whether the action sends Enter after the text.
func (action Action) Submits() bool { return action == ActionDeliverAndSubmit }

// Backend inserts text into the focused window.
type Backend interface {
	// Type inserts exactly text, adding nothing.
	Type(text string) error
	// Submit sends the Enter key.
	Submit() error
	// Name identifies the backend for logging and diagnostics.
	Name() string
}

// ReleaseWaiter blocks until the keys that triggered delivery are released.
// Typing while Ctrl or Shift is still physically held turns the text into
// shortcuts, so every delivery waits first.
type ReleaseWaiter interface {
	WaitForRelease()
}

// ReleaseWaiterFunc adapts a function to ReleaseWaiter.
type ReleaseWaiterFunc func()

// WaitForRelease implements ReleaseWaiter.
func (fn ReleaseWaiterFunc) WaitForRelease() { fn() }

// defaultReleaseDelay matches James's fixed settle time: the gesture release
// fires on the first key-up, while other modifiers may be held a few ms more.
const defaultReleaseDelay = 100 * time.Millisecond

// SleepReleaseWaiter waits a fixed settle period. It is the portable default
// where no real key-state source is available.
func SleepReleaseWaiter(delay time.Duration) ReleaseWaiter {
	return ReleaseWaiterFunc(func() { time.Sleep(delay) })
}

// Deliverer performs delivery actions through a backend, waiting for input
// release first. It never appends a space or any other character to the text.
type Deliverer struct {
	backend Backend
	waiter  ReleaseWaiter
}

// NewDeliverer builds a deliverer. A nil waiter uses the default settle delay.
func NewDeliverer(backend Backend, waiter ReleaseWaiter) *Deliverer {
	if waiter == nil {
		waiter = SleepReleaseWaiter(defaultReleaseDelay)
	}
	return &Deliverer{backend: backend, waiter: waiter}
}

// Backend returns the backend in use.
func (d *Deliverer) Backend() Backend { return d.backend }

// Do performs action. Empty text delivers nothing at all, so no state can send
// a bare Enter into a window the user never dictated into.
func (d *Deliverer) Do(action Action, text string) error {
	if !action.Valid() {
		return fmt.Errorf("delivery: unknown action %d", action)
	}
	if text == "" {
		return fmt.Errorf("delivery: refusing to %s empty text", action)
	}

	d.waiter.WaitForRelease()

	if err := d.backend.Type(text); err != nil {
		return fmt.Errorf("delivery via %s: %w", d.backend.Name(), err)
	}
	if !action.Submits() {
		return nil
	}
	if err := d.backend.Submit(); err != nil {
		return fmt.Errorf("submit via %s: %w", d.backend.Name(), err)
	}
	return nil
}

// Deliver implements the review controller's Deliverer interface.
func (d *Deliverer) Deliver(text string, submit bool) error {
	action := ActionDeliver
	if submit {
		action = ActionDeliverAndSubmit
	}
	return d.Do(action, text)
}

// commandRunner executes an external delivery tool. Tests substitute it to
// assert the exact arguments without running anything.
type commandRunner func(name string, args ...string) error

// runCommand executes name with args, surfacing tool output in the error so a
// failed paste is diagnosable.
func runCommand(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
			return fmt.Errorf("%s: %w: %s", name, err, trimmed)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// lookPath reports whether an executable is on PATH. Tests replace it to
// simulate hosts with and without the optional Wayland tools.
type lookPath func(name string) (string, error)
