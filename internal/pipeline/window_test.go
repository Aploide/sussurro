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

// wordySegment builds one long segment carrying per-word timings, one word per
// second, with no sentence punctuation at all. This is the shape whisper
// returns for an unbroken stretch of speech: few segments, each running for
// tens of seconds.
func wordySegment(prefix string, start time.Duration, n int) asr.Segment {
	return sentencedSegment(prefix, start, n, 0)
}

// sentencedSegment is wordySegment with a full stop every `every` words, so the
// window has boundaries to cut at. An `every` of zero produces none.
//
// Punctuation has to be part of the fixture: the window is cut at sentence
// ends, so a fixture without them exercises only the no-boundary fallback and
// silently tests nothing of the real path.
func sentencedSegment(prefix string, start time.Duration, n, every int) asr.Segment {
	seg := asr.Segment{Start: start, End: start + time.Duration(n)*time.Second}
	var text string
	for i := 0; i < n; i++ {
		at := start + time.Duration(i)*time.Second
		w := " " + prefix + itoa(i)
		if every > 0 && (i+1)%every == 0 {
			w += "."
		}
		seg.Words = append(seg.Words, asr.Word{Text: w, Start: at, End: at + time.Second})
		text += w
	}
	seg.Text = text[1:]
	return seg
}

// sentenceEverySegments is how many of simulate's segments make up a sentence.
// Whisper punctuates ordinary dictation every ten seconds or so; at one segment
// per second that is roughly this.
const sentenceEverySegments = 8

// simulate walks a dictation pass by pass exactly as runPass does, and reports
// the settled transcript plus the window span seen on each pass.
//
// It calls the production functions rather than restating their arithmetic: a
// test that recomputes the formula agrees with itself, not with the code.
func simulate(t *testing.T, upTo time.Duration, sentences int, secsPerSeg time.Duration) (string, []time.Duration) {
	t.Helper()

	settled, settledUntil := "", time.Duration(0)
	var prevSegs []asr.Segment
	var prevStart time.Duration
	var spans []time.Duration

	for total := time.Second; total <= upTo; total += 750 * time.Millisecond {
		windowStart := windowStartFor(settledUntil, prevSegs, prevStart, total, sentences)

		if windowStart < settledUntil {
			t.Fatalf("at total=%v: window start %v is behind the settled point %v: "+
				"settled text would be decoded again and duplicate", total, windowStart, settledUntil)
		}
		spans = append(spans, total-windowStart)

		// Whisper reports one segment per secsPerSeg of window audio, each
		// labelled for the absolute second it covers so duplicates are visible,
		// and every sentenceEverySegments'th ending a sentence so the window has
		// somewhere to cut.
		var segs []asr.Segment
		for at := time.Duration(0); at < total-windowStart; at += secsPerSeg {
			second := int((windowStart + at) / time.Second)
			text := "s" + itoa(second)
			if (second+1)%sentenceEverySegments == 0 {
				text += "."
			}
			segs = append(segs, segmentAt(text, at, at+secsPerSeg))
		}

		nextStart := windowStartFor(settledUntil, segs, windowStart, total, sentences)
		if text, until, ok := settleSegments(segs, windowStart, nextStart, sentences); ok {
			settled = joinTranscript(settled, text)
			settledUntil = until
		}
		prevSegs, prevStart = segs, windowStart
	}
	return settled, spans
}

// The defect that sank the offset-only attempt: audio between the settled
// point and the window start was never decoded at all. Settling is driven by
// where the next window begins, so the settled point cannot outrun it.
//
// simulate fails the test itself if the invariant ever breaks.
func TestSettledPointNeverOutrunsTheWindow(t *testing.T) {
	simulate(t, 90*time.Second, defaultRevisionSentences, time.Second)
}

// Settled text must cover each span exactly once: a word frozen twice appears
// twice in the transcript.
func TestSettlingDoesNotDuplicateText(t *testing.T) {
	settled, _ := simulate(t, 60*time.Second, defaultRevisionSentences, time.Second)

	seen := map[string]bool{}
	for _, w := range strings.Fields(settled) {
		if seen[w] {
			t.Errorf("segment %q settled more than once: %q", w, settled)
		}
		seen[w] = true
	}
}

// The point of the window: recent speech stays in the decoded audio, so later
// audio can still correct it. Freezing text on sight is what made
// sussurro-fkd, and is the regression this guards.
func TestRecentSpeechStaysRevisable(t *testing.T) {
	const sentences = 2
	settled, _ := simulate(t, 80*time.Second, sentences, time.Second)

	if countWords(settled) == 0 {
		t.Fatal("nothing ever settled: the window is not advancing")
	}

	// One segment per second and a sentence every sentenceEverySegments, so
	// holding two sentences open means the settled text must lag the end of the
	// recording by at least that much speech.
	revisable := 80 - countWords(settled)
	if want := sentences * sentenceEverySegments; revisable < want {
		t.Errorf("settled %d of 80 segments, leaving only %d revisable; "+
			"%d sentences is about %d segments and all of it should still be "+
			"decoded\nsettled: %q",
			countWords(settled), revisable, sentences, want, settled)
	}
}

// The ceiling bounds cost when speech is halting, but must never do so by
// skipping audio.
//
// An earlier version clamped the window start to the ceiling unconditionally.
// Only decoded audio can settle, so everything the clamp skipped was
// transcribed by nobody and whole sentences vanished from long dictations
// (sussurro-fkd). This asserts coverage, which the previous span-only check
// could not see.
func TestCeilingNeverSkipsAudio(t *testing.T) {
	// Segments five seconds apart, so ten words span fifty seconds — well past
	// the ceiling, which must therefore yield rather than orphan the audio.
	// simulate fails the test itself if the window ever outruns the settled
	// point, which is exactly what dropping audio looks like.
	simulate(t, 120*time.Second, defaultRevisionSentences, 5*time.Second)
}

// Cost stays bounded whenever the ceiling can be honoured without skipping
// audio, which is the case as soon as settling keeps up.
func TestWindowStaysBoundedWhenSettlingKeepsUp(t *testing.T) {
	_, spans := simulate(t, 120*time.Second, defaultRevisionSentences, time.Second)

	for i, span := range spans {
		if span > maxWindowDuration {
			t.Fatalf("pass %d decoded %v of audio, over the %v ceiling: "+
				"per-pass cost is unbounded again", i, span, maxWindowDuration)
		}
	}
}

// The window must open exactly at a sentence start, never mid-phrase. Cutting
// mid-phrase is what made whisper guess at the join and freeze "years" for
// "sentences" (sussurro-k6w).
func TestWindowOpensAtASentenceStart(t *testing.T) {
	const sentences = 2

	// Twenty words, a sentence every five: boundaries at words 5, 10, 15, 20.
	seg := sentencedSegment("w", 0, 20, 5)

	start := windowStartFor(0, []asr.Segment{seg}, 0, 20*time.Second, sentences)

	// Two sentences back from the end means opening at word 10, which is the
	// word after the full stop on word 9.
	if want := 10 * time.Second; start != want {
		t.Errorf("window opens at %v, want %v (the word after a full stop)", start, want)
	}

	// Whatever the arithmetic, the word the window opens on must be the first
	// of a sentence: the one before it must end with . ? or !
	for i, w := range seg.Words {
		if w.Start != start {
			continue
		}
		if i == 0 {
			break // Opening at the very beginning is a sentence start.
		}
		if !sentenceEnd(seg.Words[i-1].Text) {
			t.Errorf("window opens mid-sentence: %q follows %q, which does not "+
				"end a sentence", w.Text, seg.Words[i-1].Text)
		}
		break
	}
}

// A smaller sentence count settles text sooner; a larger one keeps more open.
// The setting has to actually do something.
func TestRevisionSentencesControlsHowMuchStaysOpen(t *testing.T) {
	few, _ := simulate(t, 80*time.Second, 1, time.Second)
	many, _ := simulate(t, 80*time.Second, 4, time.Second)

	if countWords(few) <= countWords(many) {
		t.Errorf("a 1-sentence window settled %d segments and a 4-sentence "+
			"window %d; the smaller window should settle more",
			countWords(few), countWords(many))
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

// A segment still inside the next window must not settle: later audio can
// still revise it, which is the whole point of the window.
func TestSegmentsStillInsideTheWindowDoNotSettle(t *testing.T) {
	next := 10 * time.Second
	// Ends after the next window begins, so the next pass decodes it again.
	segs := []asr.Segment{segmentAt("inside", 0, next+time.Second)}
	if _, _, ok := settleSegments(segs, 0, next, defaultRevisionSentences); ok {
		t.Error("a segment still inside the window was frozen")
	}
}

// The segment that has just left the window settles immediately. Holding it
// back by a safety margin froze nothing at all, since the window advances by
// whole segments and this is the only candidate on any given pass.
func TestTheDepartedSegmentSettles(t *testing.T) {
	next := 10 * time.Second
	segs := []asr.Segment{
		segmentAt("gone", 0, next),         // ends exactly at the boundary
		segmentAt("staying", next, next*2), // still inside
	}
	text, until, ok := settleSegments(segs, 0, next, defaultRevisionSentences)
	if !ok {
		t.Fatal("the segment that left the window did not settle")
	}
	if text != "gone" {
		t.Errorf("settled %q, want only %q", text, "gone")
	}
	if until != next {
		t.Errorf("settled up to %v, want %v", until, next)
	}
}

// The defect measured on real speech: whisper returned 17 words in one long
// segment, so a whole-segment window could not shrink, settling stalled at
// 4.06s for 34.5s of dictation, and only the ceiling bounded cost.
//
// The window must therefore cut *inside* a segment, at a sentence end, rather
// than only between segments.
func TestWindowShrinksInsideALongSegment(t *testing.T) {
	const sentences = 2
	// One 30s segment holding 30 words and a sentence every six, exactly the
	// shape that stalled: several sentences inside a single long segment.
	seg := sentencedSegment("w", 0, 30, 6)

	start := windowStartFor(0, []asr.Segment{seg}, 0, 30*time.Second, sentences)

	if start == 0 {
		t.Fatal("window did not shrink inside a long segment: settling would stall")
	}
	// Sentences end at words 5, 11, 17, 23, 29 (zero-based). The stop on word
	// 29 is the last word and is skipped, since it closes the sentence just
	// spoken. Counting two back from there reaches the stop on word 17, so the
	// window opens on word 18 — leaving sentences 18-23 and 24-29 revisable.
	if want := 18 * time.Second; start != want {
		t.Errorf("window starts at %v, want %v for a %d-sentence window",
			start, want, sentences)
	}
}

// Settling must be word-granular too: a segment straddling the boundary has to
// settle the part that has left, or the settled point stalls exactly as it did
// with a whole-segment window.
func TestLongSegmentSettlesWordByWord(t *testing.T) {
	seg := wordySegment("w", 0, 30)
	next := 10 * time.Second

	text, until, ok := settleSegments([]asr.Segment{seg}, 0, next, defaultRevisionSentences)
	if !ok {
		t.Fatal("nothing settled from a segment straddling the boundary")
	}
	if until != next {
		t.Errorf("settled up to %v, want %v", until, next)
	}
	// Words 0..9 end at or before 10s; word 10 ends at 11s and must not settle.
	if got, want := countWords(text), 10; got != want {
		t.Errorf("settled %d words (%q), want %d", got, text, want)
	}
	if strings.Contains(text, "w10") {
		t.Errorf("settled a word still inside the window: %q", text)
	}
}

// Token text carries its own leading spaces, so joining must not insert more
// or words run together or gain doubled spacing.
func TestSettledWordsKeepTheirSpacing(t *testing.T) {
	seg := wordySegment("w", 0, 5)
	text, _, ok := settleSegments([]asr.Segment{seg}, 0, 10*time.Second, defaultRevisionSentences)
	if !ok {
		t.Fatal("nothing settled")
	}
	if text != "w0 w1 w2 w3 w4" {
		t.Errorf("settled %q, want %q", text, "w0 w1 w2 w3 w4")
	}
}

// Whisper reports every token at -10ms when token timestamps are disabled.
// That drove the next window's start to zero, so every word tested as settled
// and each pass froze the entire transcript and appended it to the previous
// one — the overlay filled with the same sentence repeated (sussurro-fkd).
//
// Timings that do not advance must settle nothing at all.
func TestNothingSettlesWhenTheWindowHasNotMoved(t *testing.T) {
	seg := wordySegment("w", 0, 10)

	// nextStart equal to windowStart: the next pass decodes the same audio.
	if text, _, ok := settleSegments([]asr.Segment{seg}, 5*time.Second, 5*time.Second, defaultRevisionSentences); ok {
		t.Errorf("settled %q though the window had not moved", text)
	}
	// And behind it, which is what unusable timestamps actually produced.
	if text, _, ok := settleSegments([]asr.Segment{seg}, 5*time.Second, 0, defaultRevisionSentences); ok {
		t.Errorf("settled %q though the window went backwards", text)
	}
}

// The runaway in full: with a window that never advances, repeated passes must
// not accumulate the same text over and over.
func TestStalledWindowDoesNotAccumulateText(t *testing.T) {
	settled := ""
	for pass := 0; pass < 20; pass++ {
		seg := wordySegment("w", 0, 10)
		// A window pinned at zero, as unusable timestamps produced.
		if text, _, ok := settleSegments([]asr.Segment{seg}, 0, 0, defaultRevisionSentences); ok {
			settled = joinTranscript(settled, text)
		}
	}
	if settled != "" {
		t.Errorf("a stalled window accumulated %d words: %q", countWords(settled), settled)
	}
}

// Before the budget is reached, everything spoken is still inside the window.
//
// The walk back used to move the boundary on every step, so with a single word
// recognised it landed *after* that word: the word fell outside the window and
// settled at 420ms, freezing whisper's first guess at the opening of every
// dictation. Whisper revises heavily as context arrives, so those opening words
// came out mangled and could never be corrected (sussurro-fkd).
func TestWindowKeepsEverythingUntilTheBudgetIsReached(t *testing.T) {
	const budget = 10

	for _, spoken := range []int{1, 2, 3, 9} {
		var seg asr.Segment
		for i := 0; i < spoken; i++ {
			// Speech starts a little after zero, as a real dictation does.
			at := 420*time.Millisecond + time.Duration(i)*time.Second
			seg.Words = append(seg.Words, asr.Word{
				Text: " w" + itoa(i), Start: at, End: at + time.Second,
			})
		}
		seg.Start = seg.Words[0].Start
		seg.End = seg.Words[len(seg.Words)-1].End

		total := seg.End
		start := windowStartFor(0, []asr.Segment{seg}, 0, total, budget)

		if start != 0 {
			t.Errorf("with %d of %d words spoken the window starts at %v; "+
				"it must stay at 0 so every word is still revisable",
				spoken, budget, start)
		}
		// The real consequence: nothing may settle yet. The same budget is
		// passed here as to the window above; using a different one lets the
		// budget guard mask the very off-by-one this is testing.
		next := windowStartFor(0, []asr.Segment{seg}, 0, total, budget)
		if text, _, ok := settleSegments([]asr.Segment{seg}, 0, next, budget); ok {
			t.Errorf("with %d of %d words spoken, %q settled and can never be corrected",
				spoken, budget, text)
		}
	}
}

// Settling must always leave the configured number of sentences revisable,
// whatever an over-eager nextStart says.
//
// Measured failure: settling froze five words at once, settledUntil jumped to
// 7.9s, and the next window collapsed to 1.85s. Whisper then re-decoded that
// fragment behind a confident prompt and returned "Really" where the previous
// pass had correctly heard "Surely", freezing the corruption (sussurro-igk).
func TestSettlingLeavesTheBudgetRevisable(t *testing.T) {
	const sentences = 2

	// Twenty words, a sentence every five: stops on words 4, 9, 14, 19.
	seg := sentencedSegment("w", 0, 20, 5)

	// A nextStart far ahead, as an over-eager settle would produce.
	text, until, ok := settleSegments([]asr.Segment{seg}, 0, 18*time.Second, sentences)
	if !ok {
		t.Fatal("nothing settled at all")
	}

	// Two sentences back from the end starts at word 10, so nothing at or after
	// 10s may be frozen.
	if until > 10*time.Second {
		t.Errorf("settled up to %v; the last %d sentences begin at 10s and must "+
			"stay revisable\nsettled: %q", until, sentences, text)
	}
	if strings.Contains(text, "w10") {
		t.Errorf("settled a word inside the revision window: %q", text)
	}
}
