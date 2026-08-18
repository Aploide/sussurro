package delivery

import (
	"fmt"
	"os/exec"
)

// Capabilities describes what a host can do, so backend selection is testable
// without depending on the machine the tests run on.
type Capabilities struct {
	// LookPath resolves executables. Nil uses exec.LookPath.
	LookPath lookPath
	// Run executes a delivery tool. Nil uses the real runner.
	Run commandRunner
	// Clipboard is the portable fallback, used when no direct-typing tool is
	// available. Nil means the host cannot paste either.
	Clipboard Backend
}

// resolve fills in the real implementations for unset fields.
func (c Capabilities) resolve() Capabilities {
	if c.LookPath == nil {
		c.LookPath = exec.LookPath
	}
	if c.Run == nil {
		c.Run = runCommand
	}
	return c
}

// SelectBackend chooses the delivery backend for the configured name. auto
// prefers a direct-typing tool when one is installed and otherwise falls back
// to clipboard-paste, which is what an X11 host without wtype or ydotool gets.
// An explicitly named backend that is unavailable is an error rather than a
// silent downgrade, so a misconfigured host is diagnosable.
func SelectBackend(name BackendName, capabilities Capabilities) (Backend, error) {
	capabilities = capabilities.resolve()

	switch name {
	case BackendClipboardPaste:
		if capabilities.Clipboard == nil {
			return nil, fmt.Errorf("delivery backend %q is unavailable on this host", name)
		}
		return capabilities.Clipboard, nil

	case BackendWtype:
		if !available(capabilities.LookPath, "wtype") {
			return nil, fmt.Errorf("delivery backend %q requires wtype on PATH", name)
		}
		return newWtypeBackend(capabilities.Run), nil

	case BackendYdotool:
		if !available(capabilities.LookPath, "ydotool") {
			return nil, fmt.Errorf("delivery backend %q requires ydotool on PATH", name)
		}
		return newYdotoolBackend(capabilities.Run), nil

	case BackendAuto, "":
		// ydotool works under any compositor including Wayland sessions that
		// lack the virtual keyboard protocol, so it is preferred over wtype.
		if available(capabilities.LookPath, "ydotool") {
			return newYdotoolBackend(capabilities.Run), nil
		}
		if available(capabilities.LookPath, "wtype") {
			return newWtypeBackend(capabilities.Run), nil
		}
		if capabilities.Clipboard != nil {
			return capabilities.Clipboard, nil
		}
		return nil, fmt.Errorf("no delivery backend is available on this host")

	default:
		return nil, fmt.Errorf("unknown delivery backend %q", name)
	}
}
