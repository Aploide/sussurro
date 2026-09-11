package trigger

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SocketPath is where a running instance listens: the user's runtime
// directory, or /tmp without one. The server and every client agree on it.
func SocketPath() string {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = "/tmp"
	}
	return filepath.Join(runtimeDir, "sussurro.sock")
}

// Send delivers one command to the running instance and returns its reply
// line, so a second `sussurro` process — `sussurro --settings` — can reach
// the first without depending on nc or socat being installed.
func Send(command Command) (string, error) {
	conn, err := net.DialTimeout("unix", SocketPath(), 2*time.Second)
	if err != nil {
		return "", fmt.Errorf("sussurro is not running (no socket at %s): %w", SocketPath(), err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(string(command) + "\n")); err != nil {
		return "", fmt.Errorf("sending %s: %w", command, err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && reply == "" {
		return "", fmt.Errorf("no reply to %s: %w", command, err)
	}
	return strings.TrimSpace(reply), nil
}
