package delivery

import (
	"os/exec"
	"testing"
)

// TestOptionalToolsAreNotRequired is the guard behind the release claim that
// no optional Linux dependency became mandatory. The development host has
// neither wtype nor ydotool installed, and CI runners do not install them, so
// this test only means something where they are genuinely absent.
func TestOptionalToolsAreNotRequired(t *testing.T) {
	for _, tool := range []string{"wtype", "ydotool"} {
		if _, err := exec.LookPath(tool); err == nil {
			t.Skipf("%s is installed on this host, so absence cannot be tested here", tool)
		}
	}

	clipboard := &fakeBackend{}
	backend, err := SelectBackend(BackendAuto, Capabilities{Clipboard: clipboard})
	if err != nil {
		t.Fatalf("auto selection failed with no optional tools installed: %v", err)
	}
	if backend.Name() != string(BackendClipboardPaste) && backend != Backend(clipboard) {
		t.Errorf("auto selected %s with no optional tools installed, want the portable fallback",
			backend.Name())
	}

	// Delivery must work end to end on such a host.
	if err := NewDeliverer(backend, ReleaseWaiterFunc(func() {})).Do(ActionDeliver, "text"); err != nil {
		t.Errorf("delivery failed on a host with no optional tools: %v", err)
	}
}
