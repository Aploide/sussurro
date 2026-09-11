package ui

import (
	"sync"
	"testing"
)

// plainOverlay implements Overlay without FillIndicator, standing in for a
// platform that cannot draw the fill.
type plainOverlay struct{}

func (o *plainOverlay) Show()             {}
func (o *plainOverlay) Hide()             {}
func (o *plainOverlay) SetState(AppState) {}
func (o *plainOverlay) PushRMS(float32)   {}
func (o *plainOverlay) Close()            {}

// fillOverlay additionally implements FillIndicator.
type fillOverlay struct {
	plainOverlay
	mu    sync.Mutex
	fills []float64
}

func (o *fillOverlay) PushBufferFill(fill float64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.fills = append(o.fills, fill)
}

func (o *fillOverlay) recorded() []float64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]float64(nil), o.fills...)
}

func TestPushBufferFillReachesCapablePlatform(t *testing.T) {
	overlay := &fillOverlay{}

	pushBufferFill(overlay, 0.25)
	pushBufferFill(overlay, 0.9)

	got := overlay.recorded()
	want := []float64{0.25, 0.9}
	if len(got) != len(want) {
		t.Fatalf("got %v fills, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("fill %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// A platform without the interface must degrade quietly rather than panic:
// that is the whole point of making the indicator optional.
func TestPushBufferFillSkipsPlatformWithoutIndicator(t *testing.T) {
	pushBufferFill(&plainOverlay{}, 0.5)
}

// An unbounded cap reports bounded=false, and the Manager must then push
// nothing at all: an indicator pinned at zero would misrepresent a buffer
// that has no limit to fill.
func TestUnboundedFillIsNotPushed(t *testing.T) {
	overlay := &fillOverlay{}
	m := &Manager{
		overlay:    overlay,
		fillSource: func() (float64, bool) { return 0, false },
	}

	if fill, bounded := m.fillSource(); bounded {
		pushBufferFill(m.overlay, fill)
	}

	if got := overlay.recorded(); len(got) != 0 {
		t.Errorf("pushed %v for an unbounded cap, want nothing", got)
	}
}
