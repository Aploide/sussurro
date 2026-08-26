package trigger

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aploide/sussurro/internal/session"
)

// Command is a trigger-socket instruction. Compositors that can only fire a
// single action bind toggle; those with press/release bindings drive the review
// workflow with the explicit commands.
type Command string

const (
	// CommandToggle starts a recording, or stops one already running. This is
	// what the shipped script has always sent, so it must keep working.
	CommandToggle Command = "toggle"
	// CommandPress begins recording, or begins an edit instruction in review.
	CommandPress Command = "press"
	// CommandRelease ends the recording started by press.
	CommandRelease Command = "release"
	// CommandCancel abandons the session and discards any held text.
	CommandCancel Command = "cancel"
	// CommandDeliver inserts the reviewed text.
	CommandDeliver Command = "deliver"
	// CommandSubmit inserts the reviewed text and then sends Enter.
	CommandSubmit Command = "submit"
	// CommandSettings raises the settings window. Alone among the commands it
	// drives no recording state, so a desktop binding can send it purely to
	// reach the UI of an instance that is already running.
	CommandSettings Command = "settings"
)

// commands lists every accepted command, for validation and diagnostics.
var commands = []Command{
	CommandToggle,
	CommandPress,
	CommandRelease,
	CommandCancel,
	CommandDeliver,
	CommandSubmit,
	CommandSettings,
}

// ParseCommand interprets one line of socket input. Surrounding whitespace and
// letter case are ignored, so shell pipelines that add a trailing newline work
// unchanged. An empty line is treated as toggle: the original protocol carried
// no command at all, and existing bindings must keep working.
func ParseCommand(raw string) (Command, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return CommandToggle, nil
	}

	// Only the first line is significant; a client may send more.
	if idx := strings.IndexAny(trimmed, "\r\n"); idx != -1 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}

	for _, command := range commands {
		if Command(trimmed) == command {
			return command, nil
		}
	}
	return "", fmt.Errorf("unknown command %q; expected one of: %s", trimmed, commandList())
}

// commandList renders the accepted commands for error messages.
func commandList() string {
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, string(command))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// InputEvent maps a command onto a platform-neutral recording gesture, and
// reports whether the command is a gesture at all. Cancel, deliver, and submit
// are workflow actions rather than gestures.
func (command Command) InputEvent() (session.InputEvent, bool) {
	switch command {
	case CommandToggle:
		return session.InputToggle, true
	case CommandPress:
		return session.InputPress, true
	case CommandRelease:
		return session.InputRelease, true
	default:
		return 0, false
	}
}

// Handler performs the workflow actions the gesture commands cannot express.
// Immediate mode leaves it unset, so those commands are refused rather than
// silently ignored.
type Handler interface {
	// Cancel abandons the current session.
	Cancel()
	// Deliver inserts the reviewed text, optionally followed by Enter.
	Deliver(submit bool) error
}

// UI reaches the parts of the running interface that are not workflow actions.
// It is kept separate from Handler because the two are unset independently:
// Handler is absent in immediate mode, where review has no meaning, while the
// UI is absent under --no-ui. Folding settings into Handler would have made it
// unreachable in immediate mode for no reason.
type UI interface {
	// ToggleSettings shows the settings window, or hides it when it is already
	// visible. Implementations must be safe to call from the socket goroutine.
	ToggleSettings()
}
