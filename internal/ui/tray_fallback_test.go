package ui

import (
	"bytes"
	"image"
	"image/png"
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

// Every published view model reaches updateTrayIcon, including one per partial
// transcription. On macOS each push hops to the main thread and waits for it,
// so repeating the icon already on screen stalls the update goroutine behind
// AppKit for no visible result.
func TestTrayIconIsPushedOnlyWhenItChanges(t *testing.T) {
	m := &Manager{}

	if !m.trayIconChanged(false) {
		t.Fatal("the first idle state pushed no icon; the tray would start with none")
	}
	if m.trayIconChanged(false) {
		t.Error("a repeated idle state pushed the icon again")
	}

	if !m.trayIconChanged(true) {
		t.Fatal("entering recording pushed no icon")
	}
	if m.trayIconChanged(true) {
		t.Error("a repeated recording state pushed the icon again")
	}

	if !m.trayIconChanged(false) {
		t.Error("leaving recording pushed no icon")
	}
	if m.trayIconChanged(false) {
		t.Error("idle after recording pushed the icon again")
	}
}

// The idle glyph is solid white, so on macOS it must be pushed as a template
// image or it disappears against a light menu bar. Only the alpha channel
// survives that, which is why the red recording glyph must not be one.
func TestTrayIconArtworkSuitsItsRendering(t *testing.T) {
	idle, err := png.Decode(bytes.NewReader(trayIcon))
	if err != nil {
		t.Fatalf("decoding the idle tray icon: %v", err)
	}
	if !isMonochrome(idle) {
		t.Error("the idle tray icon is no longer monochrome; as a macOS template image only its alpha survives")
	}

	recording, err := png.Decode(bytes.NewReader(trayIconRec))
	if err != nil {
		t.Fatalf("decoding the recording tray icon: %v", err)
	}
	if isMonochrome(recording) {
		t.Error("the recording tray icon lost its colour; it is pushed plain precisely to keep it distinguishable")
	}
}

// isMonochrome reports whether every visible pixel is a shade of grey, so the
// image carries its meaning in shape and alpha rather than in hue.
func isMonochrome(img image.Image) bool {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a < 0x1000 {
				continue
			}
			if r != g || g != b {
				return false
			}
		}
	}
	return true
}
