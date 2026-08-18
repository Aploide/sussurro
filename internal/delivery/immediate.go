// Package delivery moves completed recognition results into the focused
// application. It sits behind the pipeline's result boundary so review mode can
// hold text before any delivery occurs.
package delivery

import (
	"fmt"
	"io"
	"log/slog"
)

// Injector performs the keystroke half of clipboard-paste delivery.
type Injector interface {
	Inject(text string) error
}

// ClipboardWriter places text on the system clipboard.
type ClipboardWriter func(text string) error

// Immediate reproduces upstream's immediate-mode behavior: echo the text to
// stdout, stage it on the clipboard, then paste it into the focused window.
type Immediate struct {
	clipboard ClipboardWriter
	injector  Injector
	stdout    io.Writer
	log       *slog.Logger
}

// NewImmediate builds the immediate-mode delivery path. A nil injector skips
// the paste keystroke, leaving the text on the clipboard; a nil stdout skips
// the echo.
func NewImmediate(clipboard ClipboardWriter, injector Injector, stdout io.Writer, log *slog.Logger) *Immediate {
	return &Immediate{clipboard: clipboard, injector: injector, stdout: stdout, log: log}
}

// Deliver stages text on the clipboard and pastes it. Clipboard failures are
// reported but still attempt the paste, matching upstream behavior.
func (d *Immediate) Deliver(text string) error {
	if d.stdout != nil {
		fmt.Fprintln(d.stdout, text)
	}

	if d.clipboard != nil {
		if err := d.clipboard(text); err != nil {
			d.log.Error("Failed to write to clipboard", "error", err)
		}
	}

	// A nil injector is normal on hosts where keystroke synthesis is
	// unavailable; the clipboard still carries the text.
	if d.injector == nil {
		return nil
	}
	if err := d.injector.Inject(text); err != nil {
		d.log.Error("Failed to inject text", "error", err)
		return err
	}
	return nil
}
