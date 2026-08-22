package ui

import (
	"testing"
	"time"

	"github.com/aploide/sussurro/internal/session"
)

// The overlay must not appear at all when a tray registers promptly. Showing
// it first and hiding it on registration is what flashed the overlay on every
// startup (sussurro-xvj.62).
func TestTrayFallbackStaysHiddenWhenTrayArrives(t *testing.T) {
	m := &Manager{stateChangeCh: make(chan ViewModel, 4)}
	m.trayReady.Store(true)

	m.showFallbackIfNoTray()

	select {
	case model := <-m.stateChangeCh:
		t.Errorf("published %v with a tray present, want nothing shown", model.State)
	default:
	}
}

// A desktop that hosts no SNI item still needs the overlay, since its
// right-click menu is then the only route to Settings and Quit.
func TestTrayFallbackShowsWhenNoTrayArrives(t *testing.T) {
	m := &Manager{stateChangeCh: make(chan ViewModel, 4)}

	m.showFallbackIfNoTray()

	select {
	case model := <-m.stateChangeCh:
		if model.State != session.StateIdle {
			t.Errorf("published %v, want the idle fallback", model.State)
		}
	default:
		t.Error("nothing published with no tray; the overlay is the only way in")
	}
}

// The scheduled path must reach the same decision, so the timer is actually
// wired to the fallback rather than the logic only being reachable directly.
func TestScheduledFallbackFiresWithoutATray(t *testing.T) {
	m := &Manager{stateChangeCh: make(chan ViewModel, 4)}
	m.trayGraceOverride = 10 * time.Millisecond

	m.scheduleTrayFallback()

	select {
	case model := <-m.stateChangeCh:
		if model.State != session.StateIdle {
			t.Errorf("published %v, want the idle fallback", model.State)
		}
	case <-time.After(time.Second):
		t.Error("the scheduled fallback never fired")
	}
}

// The grace period has to leave a tray-less desktop usable, and be long enough
// that a working tray reliably wins the race.
func TestTrayGracePeriodIsSane(t *testing.T) {
	if trayGracePeriod < time.Second {
		t.Errorf("grace period %v is too short; a slow tray would still flash", trayGracePeriod)
	}
	if trayGracePeriod > 5*time.Second {
		t.Errorf("grace period %v leaves a tray-less desktop unreachable too long", trayGracePeriod)
	}
}
