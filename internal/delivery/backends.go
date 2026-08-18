package delivery

import (
	"fmt"
	"os/exec"
)

// ClipboardBackend stages text on the clipboard and synthesizes a paste
// keystroke. This is upstream's portable path and works on X11, macOS, and
// Windows without any optional tool installed.
type ClipboardBackend struct {
	write    ClipboardWriter
	injector Injector
	// submitKey sends Enter. Nil means the host offers no way to submit.
	submitKey func() error
}

// NewClipboardBackend builds the portable clipboard-paste backend.
func NewClipboardBackend(write ClipboardWriter, injector Injector, submitKey func() error) *ClipboardBackend {
	return &ClipboardBackend{write: write, injector: injector, submitKey: submitKey}
}

// Name implements Backend.
func (b *ClipboardBackend) Name() string { return string(BackendClipboardPaste) }

// Type stages the text and pastes it. A clipboard failure aborts before the
// paste, so a stale clipboard is never pasted in place of the new text.
func (b *ClipboardBackend) Type(text string) error {
	if b.write != nil {
		if err := b.write(text); err != nil {
			return fmt.Errorf("staging clipboard: %w", err)
		}
	}
	if b.injector == nil {
		return fmt.Errorf("no paste backend available")
	}
	return b.injector.Inject(text)
}

// Submit sends Enter.
func (b *ClipboardBackend) Submit() error {
	if b.submitKey == nil {
		return fmt.Errorf("%s cannot send Enter on this host", b.Name())
	}
	return b.submitKey()
}

// commandBackend types text by invoking an external tool.
type commandBackend struct {
	name string
	run  commandRunner
	// typeArgs builds the argv for inserting text.
	typeArgs func(text string) []string
	// submitArgs builds the argv for sending Enter.
	submitArgs []string
}

// Name implements Backend.
func (b *commandBackend) Name() string { return b.name }

// Type implements Backend.
func (b *commandBackend) Type(text string) error {
	args := b.typeArgs(text)
	return b.run(args[0], args[1:]...)
}

// Submit implements Backend.
func (b *commandBackend) Submit() error {
	return b.run(b.submitArgs[0], b.submitArgs[1:]...)
}

// newWtypeBackend types through the Wayland virtual keyboard protocol.
// The "--" terminator keeps text starting with a dash from being read as
// flags, and -d paces keystrokes for applications that drop fast input.
func newWtypeBackend(run commandRunner) Backend {
	return &commandBackend{
		name: string(BackendWtype),
		run:  run,
		typeArgs: func(text string) []string {
			return []string{"wtype", "-d", "2", "--", text}
		},
		submitArgs: []string{"wtype", "-k", "Return"},
	}
}

// newYdotoolBackend types through the uinput daemon. ydotool uses Linux evdev
// key names, so Enter is "enter" rather than X11's "Return".
func newYdotoolBackend(run commandRunner) Backend {
	return &commandBackend{
		name: string(BackendYdotool),
		run:  run,
		typeArgs: func(text string) []string {
			return []string{"ydotool", "type", "--key-delay", "2", "--", text}
		},
		submitArgs: []string{"ydotool", "key", "enter"},
	}
}

// available reports whether tool is executable on this host.
func available(look lookPath, tool string) bool {
	if look == nil {
		look = exec.LookPath
	}
	_, err := look(tool)
	return err == nil
}
