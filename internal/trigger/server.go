package trigger

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/aploide/sussurro/internal/session"
)

// Server listens for trigger events via UNIX socket.
type Server struct {
	socket   string
	listener net.Listener
	log      *slog.Logger
	done     chan struct{}
	doneOnce sync.Once

	// dispatch routes recording gestures into the active workflow.
	dispatch session.InputDispatcher
	// handler performs cancel and delivery. Nil in immediate mode, where
	// those commands have no meaning.
	handler Handler

	// notify sends a desktop notification. Replaced in tests.
	notify func(summary, body string)
}

// NewServer creates a new trigger server.
func NewServer(log *slog.Logger) (*Server, error) {
	// Create socket in user's runtime directory
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = "/tmp"
	}

	socketPath := filepath.Join(runtimeDir, "sussurro.sock")

	// A leftover socket from a crashed run must be cleared, but a socket a
	// live instance is listening on must not: removing it would silently
	// steal the trigger channel from the running process, and both would
	// appear to work while only one received commands.
	if err := removeStaleSocket(socketPath); err != nil {
		return nil, err
	}

	return &Server{
		socket: socketPath,
		log:    log,
		done:   make(chan struct{}),
		notify: notifySend,
	}, nil
}

// removeStaleSocket clears a socket path left behind by a previous run,
// refusing if another instance is currently listening on it.
func removeStaleSocket(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("checking trigger socket %s: %w", path, err)
	}

	// Connecting is the only reliable test: the file exists either way, so
	// its presence says nothing about whether anyone is accepting on it.
	conn, err := net.DialTimeout("unix", path, staleSocketTimeout)
	if err == nil {
		conn.Close()
		return fmt.Errorf("another sussurro instance is already listening on %s", path)
	}

	// Nobody accepted, so the socket is stale and safe to replace.
	return os.Remove(path)
}

// staleSocketTimeout bounds the liveness probe above. A live server accepts
// immediately; anything slower is treated as stale rather than hanging
// startup.
const staleSocketTimeout = 200 * time.Millisecond

// SetHandler installs the review-mode action handler. Must be called before
// Start. Leaving it unset refuses cancel, deliver, and submit, which is
// correct for immediate mode where they have no meaning.
func (s *Server) SetHandler(handler Handler) {
	s.handler = handler
}

// Start begins listening. The dispatcher receives every recording gesture, so
// the server needs no knowledge of the interaction mode.
func (s *Server) Start(dispatch session.InputDispatcher) error {
	if dispatch == nil {
		return fmt.Errorf("trigger: a dispatcher is required")
	}
	s.dispatch = dispatch

	listener, err := net.Listen("unix", s.socket)
	if err != nil {
		return fmt.Errorf("failed to create socket: %w", err)
	}
	s.listener = listener

	// Set permissions so user can access it
	os.Chmod(s.socket, 0600)

	s.log.Debug("Trigger server started", "socket", s.socket)

	go s.listen()

	return nil
}

// Stop stops the server. Safe to call more than once.
func (s *Server) Stop() {
	s.doneOnce.Do(func() { close(s.done) })
	if s.listener != nil {
		s.listener.Close()
	}
	os.Remove(s.socket)
}

func (s *Server) listen() {
	for {
		select {
		case <-s.done:
			return
		default:
			conn, err := s.listener.Accept()
			if err != nil {
				select {
				case <-s.done:
					return
				default:
					s.log.Error("Failed to accept connection", "error", err)
					continue
				}
			}

			go s.handleConnection(conn)
		}
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && line == "" {
		return
	}

	reply := s.Execute(line)
	if _, err := conn.Write([]byte(reply + "\n")); err != nil {
		s.log.Debug("Failed to reply to trigger client", "error", err)
	}
}

// Execute parses and performs one command, returning the line to send back.
// It is exported so the protocol can be tested without a socket.
func (s *Server) Execute(raw string) string {
	command, err := ParseCommand(raw)
	if err != nil {
		// An unknown command must change no state at all.
		s.log.Warn("Rejected trigger command", "error", err)
		return "ERROR " + err.Error()
	}

	s.log.Debug("Received trigger command", "command", command)

	if event, isGesture := command.InputEvent(); isGesture {
		return s.gesture(command, event)
	}

	if s.handler == nil {
		s.log.Warn("Trigger command requires review mode", "command", command)
		return fmt.Sprintf("ERROR %s requires review mode", command)
	}

	switch command {
	case CommandCancel:
		s.handler.Cancel()
		return "CANCELLED"
	case CommandDeliver, CommandSubmit:
		err := s.handler.Deliver(command == CommandSubmit)
		switch {
		case errors.Is(err, session.ErrNothingToDeliver):
			// Reporting DELIVERED here would tell a script the text went out
			// when nothing was ready.
			return "IDLE"
		case err != nil:
			s.log.Error("Trigger delivery failed", "command", command, "error", err)
			return "ERROR " + err.Error()
		default:
			return "DELIVERED"
		}
	default:
		// Unreachable: every command is either a gesture or handled above.
		return fmt.Sprintf("ERROR unhandled command %s", command)
	}
}

// gesture applies a recording gesture and reports the resulting state.
func (s *Server) gesture(command Command, event session.InputEvent) string {
	if s.dispatch.Dispatch(event) {
		s.notify("Sussurro", "Processing your speech...")
		return "STOPPED"
	}

	if command == CommandRelease {
		// A release with nothing recording is a no-op, not a new recording.
		return "IDLE"
	}
	return "RECORDING"
}

// notifySend posts a desktop notification, ignoring absence of notify-send.
func notifySend(summary, body string) {
	exec.Command("notify-send", "-t", "2000", summary, body).Start()
}

// GetSocketPath returns the socket path for external triggering
func (s *Server) GetSocketPath() string {
	return s.socket
}
