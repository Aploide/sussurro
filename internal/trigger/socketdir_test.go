package trigger

import (
	"os"
	"testing"
)

// socketDir returns a temporary directory short enough to hold a Unix socket.
//
// t.TempDir() names the directory after the test, which on macOS produces
// something like
// /var/folders/.../TestNewServerReplacesAStaleSocket3283291269/001 — well past
// the 104-byte sockaddr_un.sun_path limit, so every bind() in this package
// failed with "invalid argument". The limit is a property of the platform, not
// of the test, so the fix is a short path rather than a shorter test name.
func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sus")
	if err != nil {
		t.Fatalf("creating socket directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
