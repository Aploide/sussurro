package review

import (
	"log/slog"

	"github.com/aploide/sussurro/internal/session"
)

// TextEditor revises text from a spoken instruction. The LLM engine satisfies
// it, and tests substitute a fake.
type TextEditor interface {
	EditText(original, instruction string) (string, error)
}

// Editor adapts a TextEditor to the review controller's Editor interface,
// running the revision off the controller's goroutine so a slow model never
// blocks input handling.
type Editor struct {
	editor TextEditor
	// onEdited returns the revised text to the controller. The controller
	// drops the result if the session has since been cancelled.
	onEdited func(id session.SessionID, text string)
	log      *slog.Logger
}

// NewEditor builds the adapter.
func NewEditor(editor TextEditor, onEdited func(id session.SessionID, text string), log *slog.Logger) *Editor {
	return &Editor{editor: editor, onEdited: onEdited, log: log}
}

// ApplyEdit implements session.Editor. EditText returns the original text on
// any failure, so the controller always receives something deliverable.
func (e *Editor) ApplyEdit(id session.SessionID, text, instruction string) {
	go e.apply(id, text, instruction)
}

// apply performs the revision and reports it back.
func (e *Editor) apply(id session.SessionID, text, instruction string) {
	defer func() {
		if r := recover(); r != nil {
			e.log.Error("Recovered from panic while applying an edit", "error", r)
		}
	}()

	edited, err := e.editor.EditText(text, instruction)
	if err != nil {
		// EditText returns the original alongside the error, so the reviewed
		// text survives a failed revision.
		e.log.Error("Voice edit failed", "error", err)
	}
	if e.onEdited != nil {
		e.onEdited(id, edited)
	}
}
