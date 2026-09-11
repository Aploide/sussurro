package trigger

import (
	"path/filepath"
	"strings"
	"testing"
)

// `sussurro --settings` relies on the client and server agreeing on the
// socket and the framing, so the Go client is exercised against a real
// server rather than a hand-rolled dial.
func TestSendReachesTheRunningInstance(t *testing.T) {
	runtimeDir := socketDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	dispatch := &fakeDispatcher{}
	server := newTestServer(dispatch, &fakeHandler{})
	server.socket = SocketPath()
	ui := &fakeUI{}
	server.SetUI(ui)
	if err := server.Start(dispatch); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(server.Stop)

	if server.socket != filepath.Join(runtimeDir, "sussurro.sock") {
		t.Fatalf("socket = %s, want it under XDG_RUNTIME_DIR", server.socket)
	}

	reply, err := Send(CommandSettings)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if reply != "SETTINGS" {
		t.Errorf("reply = %q, want SETTINGS", reply)
	}
	if ui.shows() != 1 {
		t.Errorf("ToggleSettings called %d times, want 1", ui.shows())
	}
}

func TestSendReportsWhenNotRunning(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", socketDir(t))

	_, err := Send(CommandSettings)
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("Send() error = %v, want a not-running message naming the socket", err)
	}
}
