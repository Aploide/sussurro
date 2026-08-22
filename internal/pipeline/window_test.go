package pipeline

import (
	"strings"
	"testing"
	"time"

	"github.com/aploide/sussurro/internal/asr"
)

// segmentAt builds a segment covering [start, end) within the window.
func segmentAt(text string, start, end time.Duration) asr.Segment {
	return asr.Segment{Text: text, Start: start, End: end}
}

// The defect that sank the offset-only attempt: audio between the settled
// point and the window start was never transcribed at all. Settling is now
// driven by segment timestamps, so the settled point must never run ahead of
// where the next window begins.
func TestSettledPointNeverOutrunsTheWindow(t *testing.T) {
	const rate = 16000
	settledUntil := time.Duration(0)

	// Walk a long dictation in ticks, as the streamer does.
	for total := 1 * time.Second; total <= 90*time.Second; total += 750 * time.Millisecond {
		windowStart := windowStartFor(settledUntil)

		if windowStart < settledUntil {
			t.Fatalf("window start %v is behind the settled point %v: text would duplicate",
				windowStart, settledUntil)
		}
		if gap := windowStart - settledUntil; gap > 0 {
			t.Fatalf("at total=%v: %v of audio between settled point %v and window start %v "+
				"would never be transcribed", total, gap, settledUntil, windowStart)
		}

		// One segment per second of window, as whisper would report them.
		var segs []asr.Segment
		for at := time.Duration(0); at < total-windowStart; at += time.Second {
			segs = append(segs, segmentAt("word", at, at+time.Second))
		}

		if _, until, ok := settleSegments(segs, windowStart, total-partialWindow, rate); ok {
			settledUntil = until
		}
	}
}

// Settled text must cover each span exactly once: a word frozen twice appears
// twice in the transcript.
func TestSettlingDoesNotDuplicateText(t *testing.T) {
	const rate = 16000
	settledUntil := time.Duration(0)
	settled := ""
	counter := 0

	for total := 1 * time.Second; total <= 60*time.Second; total += 750 * time.Millisecond {
		windowStart := windowStartFor(settledUntil)

		var segs []asr.Segment
		for at := time.Duration(0); at < total-windowStart; at += time.Second {
			// Name each segment for the absolute second it covers, so a
			// duplicate is visible as a repeated label.
			counter++
			segs = append(segs, segmentAt(
				"s"+itoa(int((windowStart+at)/time.Second)), at, at+time.Second))
		}

		if text, until, ok := settleSegments(segs, windowStart, total-partialWindow, rate); ok {
			settled = joinTranscript(settled, text)
			settledUntil = until
		}
	}
	_ = counter

	seen := map[string]bool{}
	for _, w := range strings.Fields(settled) {
		if seen[w] {
			t.Errorf("segment %q settled more than once: %q", w, settled)
		}
		seen[w] = true
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Nothing settles before there is more audio than one window: everything is
// still inside it and may yet be revised.
func TestNothingSettlesWithinTheFirstWindow(t *testing.T) {
	segs := []asr.Segment{segmentAt("hello", 0, time.Second)}
	if _, _, ok := settleSegments(segs, 0, 5*time.Second-partialWindow, 16000); ok {
		t.Error("text settled while still inside the first window")
	}
}

// A segment ending near the boundary stays unsettled, since whisper may report
// it differently on the next pass.
func TestSegmentsNearTheBoundaryDoNotSettle(t *testing.T) {
	cutoff := 10 * time.Second
	// Ends just inside the margin, so it must not settle.
	segs := []asr.Segment{segmentAt("edge", 0, cutoff-settleMargin+100*time.Millisecond)}
	if _, _, ok := settleSegments(segs, 0, cutoff, 16000); ok {
		t.Error("a segment inside the settle margin was frozen")
	}
}
