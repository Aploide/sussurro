package delivery

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"testing"
)

type stubInjector struct {
	texts []string
	err   error
}

func (s *stubInjector) Inject(text string) error {
	s.texts = append(s.texts, text)
	return s.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestImmediateDeliversExactlyOnce(t *testing.T) {
	var clipped []string
	injector := &stubInjector{}
	var stdout bytes.Buffer

	d := NewImmediate(func(text string) error {
		clipped = append(clipped, text)
		return nil
	}, injector, &stdout, discardLogger())

	if err := d.Deliver("hello world"); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	if len(clipped) != 1 || clipped[0] != "hello world" {
		t.Errorf("clipboard writes = %v, want one %q", clipped, "hello world")
	}
	if len(injector.texts) != 1 || injector.texts[0] != "hello world" {
		t.Errorf("injections = %v, want one %q", injector.texts, "hello world")
	}
	if got := stdout.String(); got != "hello world\n" {
		t.Errorf("stdout = %q, want %q", got, "hello world\n")
	}
}

func TestImmediatePastesDespiteClipboardFailure(t *testing.T) {
	injector := &stubInjector{}
	d := NewImmediate(func(string) error { return errors.New("no clipboard owner") }, injector, nil, discardLogger())

	if err := d.Deliver("text"); err != nil {
		t.Fatalf("Deliver() error = %v, want nil", err)
	}
	if len(injector.texts) != 1 {
		t.Errorf("injections = %d, want 1 despite clipboard failure", len(injector.texts))
	}
}

func TestImmediateReportsInjectionFailure(t *testing.T) {
	injector := &stubInjector{err: errors.New("no input device")}
	var clipped []string
	d := NewImmediate(func(text string) error {
		clipped = append(clipped, text)
		return nil
	}, injector, nil, discardLogger())

	if err := d.Deliver("text"); err == nil {
		t.Fatal("Deliver() error = nil, want injection failure")
	}
	// The text must still reach the clipboard so it is not lost.
	if len(clipped) != 1 {
		t.Errorf("clipboard writes = %d, want 1", len(clipped))
	}
}

func TestImmediateWithoutInjectorStillStagesClipboard(t *testing.T) {
	var clipped []string
	d := NewImmediate(func(text string) error {
		clipped = append(clipped, text)
		return nil
	}, nil, nil, discardLogger())

	if err := d.Deliver("text"); err != nil {
		t.Fatalf("Deliver() error = %v, want nil", err)
	}
	if len(clipped) != 1 || clipped[0] != "text" {
		t.Errorf("clipboard writes = %v, want one %q", clipped, "text")
	}
}
