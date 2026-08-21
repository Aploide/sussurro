package delivery

import (
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordedCommand is one external tool invocation.
type recordedCommand struct {
	name string
	args []string
}

// fakeRunner records invocations instead of executing anything.
type fakeRunner struct {
	mu       sync.Mutex
	commands []recordedCommand
	err      error
}

func (f *fakeRunner) run(name string, args ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, recordedCommand{name: name, args: append([]string(nil), args...)})
	return f.err
}

func (f *fakeRunner) recorded() []recordedCommand {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedCommand(nil), f.commands...)
}

// pathWith reports the named tools as installed and everything else as absent.
func pathWith(tools ...string) lookPath {
	installed := make(map[string]bool, len(tools))
	for _, tool := range tools {
		installed[tool] = true
	}
	return func(name string) (string, error) {
		if installed[name] {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
}

// fakeBackend records what a delivery asked of the backend.
type fakeBackend struct {
	mu        sync.Mutex
	typed     []string
	submits   int
	typeErr   error
	submitErr error
}

func (f *fakeBackend) Name() string { return "fake" }

func (f *fakeBackend) Type(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typed = append(f.typed, text)
	return f.typeErr
}

func (f *fakeBackend) Submit() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submits++
	return f.submitErr
}

func (f *fakeBackend) stats() (typed []string, submits int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.typed...), f.submits
}

// countingWaiter records how often delivery waited for input release.
type countingWaiter struct {
	mu     sync.Mutex
	waits  int
	before func()
}

func (w *countingWaiter) WaitForRelease() {
	w.mu.Lock()
	w.waits++
	hook := w.before
	w.mu.Unlock()
	if hook != nil {
		hook()
	}
}

func (w *countingWaiter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.waits
}

func TestActionValues(t *testing.T) {
	if !ActionDeliver.Valid() || ActionDeliver.String() != "deliver" || ActionDeliver.Submits() {
		t.Errorf("ActionDeliver misdescribed: %s", ActionDeliver)
	}
	if !ActionDeliverAndSubmit.Valid() || ActionDeliverAndSubmit.String() != "deliver-and-submit" ||
		!ActionDeliverAndSubmit.Submits() {
		t.Errorf("ActionDeliverAndSubmit misdescribed: %s", ActionDeliverAndSubmit)
	}
	invalid := Action(actionCount)
	if invalid.Valid() || invalid.String() != "invalid" {
		t.Errorf("out-of-range action misdescribed: %s", invalid)
	}
}

func TestDeliverInsertsExactTextWithoutEnter(t *testing.T) {
	backend := &fakeBackend{}
	waiter := &countingWaiter{}
	d := NewDeliverer(backend, waiter)

	const text = "the exact text"
	if err := d.Do(ActionDeliver, text); err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	typed, submits := backend.stats()
	if len(typed) != 1 || typed[0] != text {
		t.Errorf("typed %v, want exactly %q with no added space", typed, text)
	}
	if submits != 0 {
		t.Errorf("submits = %d, want 0 for a plain deliver", submits)
	}
}

func TestDeliverAndSubmitSendsEnterAfterText(t *testing.T) {
	backend := &fakeBackend{}
	d := NewDeliverer(backend, &countingWaiter{})

	if err := d.Do(ActionDeliverAndSubmit, "text"); err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	typed, submits := backend.stats()
	if len(typed) != 1 || typed[0] != "text" {
		t.Errorf("typed %v, want exactly the text", typed)
	}
	if submits != 1 {
		t.Errorf("submits = %d, want 1", submits)
	}
}

func TestDeliveryWaitsForInputReleaseBeforeTyping(t *testing.T) {
	backend := &fakeBackend{}
	var order []string
	waiter := &countingWaiter{before: func() { order = append(order, "wait") }}
	d := NewDeliverer(backend, waiter)

	// Record ordering by having the backend append after the waiter.
	if err := d.Do(ActionDeliverAndSubmit, "text"); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	order = append(order, "typed")

	if waiter.count() != 1 {
		t.Errorf("waits = %d, want exactly 1 per delivery", waiter.count())
	}
	if len(order) != 2 || order[0] != "wait" {
		t.Errorf("order = %v, want the release wait before typing", order)
	}
}

func TestEmptyTextIsNeverDelivered(t *testing.T) {
	for _, action := range []Action{ActionDeliver, ActionDeliverAndSubmit} {
		t.Run(action.String(), func(t *testing.T) {
			backend := &fakeBackend{}
			waiter := &countingWaiter{}
			d := NewDeliverer(backend, waiter)

			if err := d.Do(action, ""); err == nil {
				t.Fatal("Do() error = nil, want a refusal for empty text")
			}
			typed, submits := backend.stats()
			if len(typed) != 0 || submits != 0 {
				t.Errorf("backend touched (typed=%v submits=%d), want nothing", typed, submits)
			}
			// A bare Enter must never reach a window the user never dictated into.
			if waiter.count() != 0 {
				t.Errorf("waits = %d, want 0 when nothing is delivered", waiter.count())
			}
		})
	}
}

func TestTypeFailureSkipsSubmit(t *testing.T) {
	backend := &fakeBackend{typeErr: errors.New("no window focused")}
	d := NewDeliverer(backend, &countingWaiter{})

	err := d.Do(ActionDeliverAndSubmit, "text")
	if err == nil {
		t.Fatal("Do() error = nil, want the backend failure")
	}
	if !strings.Contains(err.Error(), "fake") {
		t.Errorf("error %q does not name the backend", err)
	}
	if _, submits := backend.stats(); submits != 0 {
		t.Errorf("submits = %d after a failed insert, want 0", submits)
	}
}

func TestSubmitFailureReported(t *testing.T) {
	backend := &fakeBackend{submitErr: errors.New("key failed")}
	d := NewDeliverer(backend, &countingWaiter{})

	if err := d.Do(ActionDeliverAndSubmit, "text"); err == nil {
		t.Fatal("Do() error = nil, want the submit failure")
	}
}

func TestInvalidActionRejected(t *testing.T) {
	backend := &fakeBackend{}
	d := NewDeliverer(backend, &countingWaiter{})

	if err := d.Do(Action(actionCount), "text"); err == nil {
		t.Fatal("Do() error = nil, want a rejection")
	}
	if typed, _ := backend.stats(); len(typed) != 0 {
		t.Errorf("typed %v for an invalid action, want nothing", typed)
	}
}

func TestDelivererImplementsControllerInterface(t *testing.T) {
	backend := &fakeBackend{}
	d := NewDeliverer(backend, &countingWaiter{})

	if err := d.Deliver("text", false); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if _, submits := backend.stats(); submits != 0 {
		t.Errorf("submits = %d for submit=false, want 0", submits)
	}
	if err := d.Deliver("text", true); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if _, submits := backend.stats(); submits != 1 {
		t.Errorf("submits = %d for submit=true, want 1", submits)
	}
}

func TestAutoSelectsClipboardWhenNoTypingToolsInstalled(t *testing.T) {
	clipboard := &fakeBackend{}
	// This is the primary development host: X11, no wtype, no ydotool.
	backend, err := SelectBackend(BackendAuto, Capabilities{
		LookPath:  pathWith(),
		Clipboard: clipboard,
	})
	if err != nil {
		t.Fatalf("SelectBackend() error = %v", err)
	}
	if backend != Backend(clipboard) {
		t.Errorf("selected %s, want the clipboard fallback", backend.Name())
	}
}

func TestAutoPrefersDirectTypingTools(t *testing.T) {
	tests := []struct {
		name      string
		installed []string
		want      BackendName
	}{
		{name: "ydotool only", installed: []string{"ydotool"}, want: BackendYdotool},
		{name: "wtype only", installed: []string{"wtype"}, want: BackendWtype},
		{name: "both installed", installed: []string{"wtype", "ydotool"}, want: BackendYdotool},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, err := SelectBackend(BackendAuto, Capabilities{
				LookPath:  pathWith(tt.installed...),
				Clipboard: &fakeBackend{},
			})
			if err != nil {
				t.Fatalf("SelectBackend() error = %v", err)
			}
			if backend.Name() != string(tt.want) {
				t.Errorf("selected %s, want %s", backend.Name(), tt.want)
			}
		})
	}
}

func TestAutoFailsWhenNothingIsAvailable(t *testing.T) {
	if _, err := SelectBackend(BackendAuto, Capabilities{LookPath: pathWith()}); err == nil {
		t.Fatal("SelectBackend() error = nil, want a failure with no backend at all")
	}
}

func TestExplicitBackendIsNotSilentlyDowngraded(t *testing.T) {
	for _, name := range []BackendName{BackendWtype, BackendYdotool} {
		t.Run(string(name), func(t *testing.T) {
			_, err := SelectBackend(name, Capabilities{
				LookPath:  pathWith(),
				Clipboard: &fakeBackend{},
			})
			if err == nil {
				t.Fatalf("SelectBackend(%s) error = nil, want a failure rather than a silent fallback", name)
			}
			if !strings.Contains(err.Error(), string(name)) {
				t.Errorf("error %q does not name the requested backend", err)
			}
		})
	}
}

func TestUnknownBackendRejected(t *testing.T) {
	if _, err := SelectBackend("telepathy", Capabilities{}); err == nil {
		t.Fatal("SelectBackend() error = nil, want a rejection")
	}
}

func TestWtypeCommandArguments(t *testing.T) {
	runner := &fakeRunner{}
	backend, err := SelectBackend(BackendWtype, Capabilities{
		LookPath: pathWith("wtype"),
		Run:      runner.run,
	})
	if err != nil {
		t.Fatalf("SelectBackend() error = %v", err)
	}

	// Text starting with a dash must reach the tool as text, not as flags.
	const text = "--not-a-flag"
	if err := NewDeliverer(backend, &countingWaiter{}).Do(ActionDeliverAndSubmit, text); err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	commands := runner.recorded()
	if len(commands) != 2 {
		t.Fatalf("ran %d commands, want type then submit", len(commands))
	}
	wantType := []string{"-d", "2", "--", text}
	if commands[0].name != "wtype" || !equalArgs(commands[0].args, wantType) {
		t.Errorf("type command = %s %v, want wtype %v", commands[0].name, commands[0].args, wantType)
	}
	wantSubmit := []string{"-k", "Return"}
	if commands[1].name != "wtype" || !equalArgs(commands[1].args, wantSubmit) {
		t.Errorf("submit command = %s %v, want wtype %v", commands[1].name, commands[1].args, wantSubmit)
	}
}

func TestYdotoolCommandArguments(t *testing.T) {
	runner := &fakeRunner{}
	backend, err := SelectBackend(BackendYdotool, Capabilities{
		LookPath: pathWith("ydotool"),
		Run:      runner.run,
	})
	if err != nil {
		t.Fatalf("SelectBackend() error = %v", err)
	}

	if err := NewDeliverer(backend, &countingWaiter{}).Do(ActionDeliverAndSubmit, "hello"); err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	commands := runner.recorded()
	if len(commands) != 2 {
		t.Fatalf("ran %d commands, want type then submit", len(commands))
	}
	wantType := []string{"type", "--key-delay", "2", "--", "hello"}
	if !equalArgs(commands[0].args, wantType) {
		t.Errorf("type args = %v, want %v", commands[0].args, wantType)
	}
	// ydotool uses evdev key names, so Enter is "enter" not X11's "Return".
	wantSubmit := []string{"key", "enter"}
	if !equalArgs(commands[1].args, wantSubmit) {
		t.Errorf("submit args = %v, want %v", commands[1].args, wantSubmit)
	}
}

func TestCommandBackendFailurePropagates(t *testing.T) {
	runner := &fakeRunner{err: errors.New("wtype: compositor does not support the protocol")}
	backend, err := SelectBackend(BackendWtype, Capabilities{
		LookPath: pathWith("wtype"),
		Run:      runner.run,
	})
	if err != nil {
		t.Fatalf("SelectBackend() error = %v", err)
	}

	if err := NewDeliverer(backend, &countingWaiter{}).Do(ActionDeliver, "text"); err == nil {
		t.Fatal("Do() error = nil, want the tool failure")
	}
}

func TestClipboardBackendStagesThenPastes(t *testing.T) {
	var staged []string
	injector := &stubInjector{}
	backend := NewClipboardBackend(func(text string) error {
		staged = append(staged, text)
		return nil
	}, injector, nil)

	if err := backend.Type("hello"); err != nil {
		t.Fatalf("Type() error = %v", err)
	}
	if len(staged) != 1 || staged[0] != "hello" {
		t.Errorf("staged %v, want one %q", staged, "hello")
	}
	if len(injector.texts) != 1 {
		t.Errorf("pasted %d times, want 1", len(injector.texts))
	}
}

func TestClipboardBackendAbortsWhenStagingFails(t *testing.T) {
	injector := &stubInjector{}
	backend := NewClipboardBackend(func(string) error {
		return errors.New("no clipboard owner")
	}, injector, nil)

	if err := backend.Type("hello"); err == nil {
		t.Fatal("Type() error = nil, want the staging failure")
	}
	// Pasting now would insert whatever the clipboard held before.
	if len(injector.texts) != 0 {
		t.Errorf("pasted %d times after a staging failure, want 0", len(injector.texts))
	}
}

func TestClipboardBackendSubmitUnsupported(t *testing.T) {
	backend := NewClipboardBackend(func(string) error { return nil }, &stubInjector{}, nil)

	if err := backend.Submit(); err == nil {
		t.Fatal("Submit() error = nil, want a clear unsupported error")
	}
}

func TestClipboardBackendSubmitUsesProvidedKey(t *testing.T) {
	submits := 0
	backend := NewClipboardBackend(func(string) error { return nil }, &stubInjector{}, func() error {
		submits++
		return nil
	})

	if err := backend.Submit(); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if submits != 1 {
		t.Errorf("submits = %d, want 1", submits)
	}
}

func TestSleepReleaseWaiterWaits(t *testing.T) {
	waiter := SleepReleaseWaiter(20 * time.Millisecond)
	start := time.Now()
	waiter.WaitForRelease()
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Errorf("waited %s, want roughly the configured delay", elapsed)
	}
}

// equalArgs compares argument slices.
func equalArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestClipboardOnlyStagesWithoutPasting(t *testing.T) {
	var staged []string
	backend := NewClipboardOnlyBackend(func(text string) error {
		staged = append(staged, text)
		return nil
	}, "clipboard-paste")

	if err := backend.Type("the dictated text"); err != nil {
		t.Fatalf("Type() error = %v", err)
	}
	if len(staged) != 1 || staged[0] != "the dictated text" {
		t.Errorf("staged %v, want one %q", staged, "the dictated text")
	}
}

func TestClipboardOnlyRefusesToSubmit(t *testing.T) {
	backend := NewClipboardOnlyBackend(func(string) error { return nil }, "clipboard-paste")

	// Enter without an insert would land in whatever window has focus.
	if err := backend.Submit(); err == nil {
		t.Fatal("Submit() error = nil, want a refusal")
	}
}

func TestClipboardOnlyReportsStagingFailure(t *testing.T) {
	backend := NewClipboardOnlyBackend(func(string) error {
		return errors.New("no clipboard owner")
	}, "clipboard-paste")

	if err := backend.Type("text"); err == nil {
		t.Fatal("Type() error = nil, want the staging failure")
	}
}

func TestClipboardOnlyNamesTheWrappedBackend(t *testing.T) {
	backend := NewClipboardOnlyBackend(func(string) error { return nil }, "wtype")

	// The chosen backend still shows in diagnostics, since it is what would
	// have been used had pasting been enabled.
	if !strings.Contains(backend.Name(), "wtype") {
		t.Errorf("Name() = %q, want it to name the wrapped backend", backend.Name())
	}
	if !strings.Contains(backend.Name(), "clipboard only") {
		t.Errorf("Name() = %q, want it to say clipboard only", backend.Name())
	}
}

func TestClipboardOnlyDeliversThroughTheDeliverer(t *testing.T) {
	var staged []string
	backend := NewClipboardOnlyBackend(func(text string) error {
		staged = append(staged, text)
		return nil
	}, "clipboard-paste")

	d := NewDeliverer(backend, ReleaseWaiterFunc(func() {}))
	if err := d.Do(ActionDeliver, "text"); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if len(staged) != 1 {
		t.Errorf("staged %d times, want 1", len(staged))
	}

	// DeliverAndSubmit cannot work without an insert, and must say so rather
	// than silently sending Enter.
	if err := d.Do(ActionDeliverAndSubmit, "text"); err == nil {
		t.Fatal("Do(submit) error = nil, want a refusal")
	}
}
