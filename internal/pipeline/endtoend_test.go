package pipeline

import (
	"strings"
	"testing"
	"time"

	"github.com/aploide/sussurro/internal/asr"
)

// spokenWords is the script the fake engine "hears", one word per second.
//
// Punctuated into sentences of varying length, because the window is cut at
// sentence boundaries: a script with no . ? or ! would never window at
// all and would test nothing. Long enough to hold well more than
// defaultRevisionSentences sentences.
var spokenWords = strings.Fields(
	"the quick brown fox jumps over the lazy dog. " +
		"it then keeps running across the field until it reaches the wood. " +
		"the path turns sharply left and climbs between two old stone walls. " +
		"beyond them a wide meadow opens out. " +
		"tall grass and wild flowers stretch towards a line of dark trees. " +
		"the ground falls steeply into a narrow valley. " +
		"a shallow stream runs over flat grey stones down to the village bridge. " +
		"the road leads eventually back to where this walk began.")

// scriptedEngine answers like whisper does for a growing recording: it returns
// the words whose audio lies inside the window it was handed, with per-word
// timings relative to that window.
//
// The point of driving the real streamer with this rather than calling
// settleSegments directly is that the unit tests all fed idealised segments
// that I built myself, so they agreed with my model of whisper instead of with
// whisper. Every bug in sussurro-fkd lived in that gap.
type scriptedEngine struct {
	sampleRate int
	// spokenSoFar is how many words have been uttered at the current point in
	// the simulated dictation, advanced by the test as it steps forward.
	spokenSoFar int
	// prompts records the preceding text handed to each call, so a test can
	// assert the decoder is conditioned rather than re-transcribing.
	prompts []string
}

func (e *scriptedEngine) Transcribe(samples []float32) (string, error) {
	segs, err := e.SegmentsWithContext(samples, "")
	if err != nil {
		return "", err
	}
	return asr.JoinSegments(segs), nil
}

// SegmentsWithContext reports the window's words. windowStart is implied by the
// caller trimming the audio, so the words are whichever fall in the final
// len(samples) of the script.
func (e *scriptedEngine) SegmentsWithContext(samples []float32, preceding string) ([]asr.Segment, error) {
	e.prompts = append(e.prompts, preceding)

	windowLen := samplesDuration(len(samples), e.sampleRate)
	count := int(windowLen / time.Second)
	if count > len(spokenWords) {
		count = len(spokenWords)
	}
	if count <= 0 {
		return nil, nil
	}

	// The window always ends at "now", so it holds the last `count` words of
	// however much has been spoken. The caller trims from the front, so index
	// zero of this window is `count` words back from the end of the script.
	spoken := e.spokenSoFar
	if spoken > len(spokenWords) {
		spoken = len(spokenWords)
	}
	from := spoken - count
	if from < 0 {
		from = 0
	}

	seg := asr.Segment{}
	for i := from; i < spoken; i++ {
		at := time.Duration(i-from) * time.Second
		w := " " + spokenWords[i]
		seg.Words = append(seg.Words, asr.Word{Text: w, Start: at, End: at + time.Second})
	}
	if len(seg.Words) == 0 {
		return nil, nil
	}
	seg.Start = 0
	seg.End = time.Duration(len(seg.Words)) * time.Second
	seg.Text = strings.TrimSpace(strings.Join(wordTexts(seg.Words), ""))
	return []asr.Segment{seg}, nil
}

// longestSentence returns the duration of the longest sentence in the script,
// at one word per second. Tests derive their bounds from this so that changing
// the script or the sentence budget cannot silently make them vacuous.
func longestSentence() time.Duration {
	longest, current := 0, 0
	for _, w := range spokenWords {
		current++
		if sentenceEnd(w) {
			if current > longest {
				longest = current
			}
			current = 0
		}
	}
	if current > longest {
		longest = current
	}
	return time.Duration(longest) * time.Second
}

// wordTexts extracts the verbatim token texts, which carry their own spacing.
func wordTexts(words []asr.Word) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = w.Text
	}
	return out
}

// The end-to-end property that matters and that no unit test asserted: the
// words the user spoke come back, in order, exactly once.
//
// Every defect in sussurro-fkd violated this while the unit tests stayed green
// — text lost when the ceiling skipped audio, text repeated when a stalled
// window re-settled the whole transcript, text repeated again when the decoder
// prompt leaked into the final pass.
func TestSpokenWordsSurviveTheWindowing(t *testing.T) {
	const rate = 16000
	engine := &scriptedEngine{sampleRate: rate}

	settled, settledUntil := "", time.Duration(0)
	var prevSegs []asr.Segment
	var prevStart time.Duration
	var overlay string

	for spoken := 1; spoken <= len(spokenWords); spoken++ {
		engine.spokenSoFar = spoken
		total := time.Duration(spoken) * time.Second

		windowStart := windowStartFor(settledUntil, prevSegs, prevStart, total, defaultRevisionSentences)
		if windowStart < settledUntil {
			t.Fatalf("after %d words: window start %v is behind the settled point %v",
				spoken, windowStart, settledUntil)
		}

		windowSamples := durationSamples(total-windowStart, rate)
		segs, err := engine.SegmentsWithContext(make([]float32, windowSamples), settled)
		if err != nil {
			t.Fatal(err)
		}

		overlay = joinTranscript(settled, asr.JoinSegments(segs))

		nextStart := windowStartFor(settledUntil, segs, windowStart, total, defaultRevisionSentences)
		if text, until, ok := settleSegments(segs, windowStart, nextStart, defaultRevisionSentences); ok {
			settled = joinTranscript(settled, text)
			settledUntil = until
		}
		prevSegs, prevStart = segs, windowStart
	}

	if got, want := overlay, strings.Join(spokenWords, " "); got != want {
		t.Errorf("transcript does not match what was spoken.\n got: %q\nwant: %q", got, want)
	}
}

// The window must stay bounded, or the cost this whole mechanism exists to
// control is back. Asserted on the same run as the correctness property, since
// a fix for one has repeatedly broken the other.
func TestWindowStaysBoundedAcrossARealDictation(t *testing.T) {
	const rate = 16000
	engine := &scriptedEngine{sampleRate: rate}

	settledUntil := time.Duration(0)
	var prevSegs []asr.Segment
	var prevStart time.Duration
	var widest time.Duration

	for spoken := 1; spoken <= len(spokenWords); spoken++ {
		engine.spokenSoFar = spoken
		total := time.Duration(spoken) * time.Second

		windowStart := windowStartFor(settledUntil, prevSegs, prevStart, total, defaultRevisionSentences)
		if span := total - windowStart; span > widest {
			widest = span
		}

		segs, err := engine.SegmentsWithContext(
			make([]float32, durationSamples(total-windowStart, rate)), "")
		if err != nil {
			t.Fatal(err)
		}

		nextStart := windowStartFor(settledUntil, segs, windowStart, total, defaultRevisionSentences)
		if _, until, ok := settleSegments(segs, windowStart, nextStart, defaultRevisionSentences); ok {
			settledUntil = until
		}
		prevSegs, prevStart = segs, windowStart
	}

	// Derived from the script rather than hardcoded, so the bound follows
	// defaultRevisionSentences instead of silently going slack when it changes.
	// The window holds that many complete sentences plus the one in progress,
	// so allow one sentence of slack over the longest the script contains.
	dictation := time.Duration(len(spokenWords)) * time.Second
	bound := time.Duration(defaultRevisionSentences+1) * longestSentence()

	if widest > bound {
		t.Errorf("widest window was %v over a %v dictation, past the %v a "+
			"%d-sentence window should need: the window is tracking the "+
			"recording rather than the last sentences",
			widest, dictation, bound, defaultRevisionSentences)
	}
	// The bound is only meaningful if it is well short of the dictation: a
	// window as wide as the recording would "pass" while bounding nothing.
	if bound >= dictation {
		t.Fatalf("test is vacuous: bound %v is not shorter than the %v "+
			"dictation, so it cannot detect an unbounded window", bound, dictation)
	}
}

// The decoder must be conditioned on settled text rather than re-transcribing
// it: that is what makes the window cheap. Once settling starts, every call
// should carry the settled prefix.
func TestSettledTextIsPassedAsDecoderContext(t *testing.T) {
	const rate = 16000
	engine := &scriptedEngine{sampleRate: rate}
	settled, settledUntil := "", time.Duration(0)
	var prevSegs []asr.Segment
	var prevStart time.Duration

	for spoken := 1; spoken <= len(spokenWords); spoken++ {
		engine.spokenSoFar = spoken
		total := time.Duration(spoken) * time.Second

		windowStart := windowStartFor(settledUntil, prevSegs, prevStart, total, defaultRevisionSentences)
		segs, err := engine.SegmentsWithContext(
			make([]float32, durationSamples(total-windowStart, rate)), settled)
		if err != nil {
			t.Fatal(err)
		}

		nextStart := windowStartFor(settledUntil, segs, windowStart, total, defaultRevisionSentences)
		if text, until, ok := settleSegments(segs, windowStart, nextStart, defaultRevisionSentences); ok {
			settled = joinTranscript(settled, text)
			settledUntil = until
		}
		prevSegs, prevStart = segs, windowStart
	}

	if settled == "" {
		t.Fatal("nothing settled across the whole dictation")
	}
	if last := engine.prompts[len(engine.prompts)-1]; last == "" {
		t.Error("the final pass carried no decoder context, so settled text " +
			"would have to be transcribed again")
	}
}

// The runaway repetition in full, through the real windowing.
//
// With token timestamps disabled whisper reports every token at -10ms. That is
// what actually shipped: the window never advanced, every word tested as
// settled, and each pass appended the whole transcript to the settled text
// until the overlay was a wall of the same sentence (sussurro-fkd).
//
// Timings are enabled now, so this asserts the containment: unusable timings
// must settle nothing rather than everything.
func TestUnusableTimingsDoNotAccumulateText(t *testing.T) {
	const rate = 16000
	engine := &brokenTimingEngine{sampleRate: rate}

	settled, settledUntil := "", time.Duration(0)
	var prevSegs []asr.Segment
	var prevStart time.Duration

	for spoken := 1; spoken <= len(spokenWords); spoken++ {
		engine.spokenSoFar = spoken
		total := time.Duration(spoken) * time.Second

		windowStart := windowStartFor(settledUntil, prevSegs, prevStart, total, defaultRevisionSentences)
		segs, err := engine.SegmentsWithContext(
			make([]float32, durationSamples(total-windowStart, rate)), settled)
		if err != nil {
			t.Fatal(err)
		}

		nextStart := windowStartFor(settledUntil, segs, windowStart, total, defaultRevisionSentences)
		if text, until, ok := settleSegments(segs, windowStart, nextStart, defaultRevisionSentences); ok {
			settled = joinTranscript(settled, text)
			settledUntil = until
		}
		prevSegs, prevStart = segs, windowStart

		// The failure was unbounded growth: settled text ran far past what was
		// actually spoken, the same words over and over.
		if countWords(settled) > spoken {
			t.Fatalf("after %d words spoken, settled text holds %d words: %q",
				spoken, countWords(settled), settled)
		}
	}
}

// brokenTimingEngine reproduces whisper with token timestamps disabled: real
// text, but every timing reported as -10ms.
type brokenTimingEngine struct {
	sampleRate  int
	spokenSoFar int
}

func (e *brokenTimingEngine) Transcribe(samples []float32) (string, error) {
	segs, err := e.SegmentsWithContext(samples, "")
	if err != nil {
		return "", err
	}
	return asr.JoinSegments(segs), nil
}

func (e *brokenTimingEngine) SegmentsWithContext(samples []float32, preceding string) ([]asr.Segment, error) {
	spoken := e.spokenSoFar
	if spoken > len(spokenWords) {
		spoken = len(spokenWords)
	}
	if spoken <= 0 {
		return nil, nil
	}

	const bad = -10 * time.Millisecond
	seg := asr.Segment{Start: bad, End: bad}
	for i := 0; i < spoken; i++ {
		seg.Words = append(seg.Words, asr.Word{
			Text: " " + spokenWords[i], Start: bad, End: bad,
		})
	}
	seg.Text = strings.TrimSpace(strings.Join(wordTexts(seg.Words), ""))
	return []asr.Segment{seg}, nil
}

// The ceiling must bound cost without ever skipping audio.
//
// An earlier version clamped the window start to the ceiling unconditionally.
// Only decoded audio can settle, so everything the clamp jumped over was
// transcribed by nobody: whole sentences vanished from long dictations, which
// is what the user saw as "only the last sentence got copied" (sussurro-fkd).
//
// Speech here is slow enough that the word budget spans more than the ceiling,
// which is the only condition under which the clamp fires.
func TestSlowSpeechLosesNoWordsToTheCeiling(t *testing.T) {
	const rate = 16000
	// Four seconds a word, so ten words span 40s against a 30s ceiling.
	const perWord = 4 * time.Second
	engine := &pacedEngine{sampleRate: rate, perWord: perWord}

	settled, settledUntil := "", time.Duration(0)
	var prevSegs []asr.Segment
	var prevStart time.Duration
	var overlay string

	for spoken := 1; spoken <= len(spokenWords); spoken++ {
		engine.spokenSoFar = spoken
		total := time.Duration(spoken) * perWord

		windowStart := windowStartFor(settledUntil, prevSegs, prevStart, total, defaultRevisionSentences)
		if windowStart > settledUntil {
			t.Fatalf("after %d words: window starts at %v but settled text ends at %v; "+
				"the %v of audio between them is transcribed by nobody",
				spoken, windowStart, settledUntil, windowStart-settledUntil)
		}

		segs, err := engine.SegmentsWithContext(
			make([]float32, durationSamples(total-windowStart, rate)), settled)
		if err != nil {
			t.Fatal(err)
		}
		overlay = joinTranscript(settled, asr.JoinSegments(segs))

		nextStart := windowStartFor(settledUntil, segs, windowStart, total, defaultRevisionSentences)
		if text, until, ok := settleSegments(segs, windowStart, nextStart, defaultRevisionSentences); ok {
			settled = joinTranscript(settled, text)
			settledUntil = until
		}
		prevSegs, prevStart = segs, windowStart
	}

	if got, want := overlay, strings.Join(spokenWords, " "); got != want {
		t.Errorf("slow speech lost or repeated words.\n got: %q\nwant: %q", got, want)
	}
}

// pacedEngine is scriptedEngine with a configurable seconds-per-word, so a
// dictation can be made slow enough to exceed the window ceiling.
type pacedEngine struct {
	sampleRate  int
	perWord     time.Duration
	spokenSoFar int
}

func (e *pacedEngine) Transcribe(samples []float32) (string, error) {
	segs, err := e.SegmentsWithContext(samples, "")
	if err != nil {
		return "", err
	}
	return asr.JoinSegments(segs), nil
}

func (e *pacedEngine) SegmentsWithContext(samples []float32, preceding string) ([]asr.Segment, error) {
	windowLen := samplesDuration(len(samples), e.sampleRate)
	count := int(windowLen / e.perWord)
	if count > len(spokenWords) {
		count = len(spokenWords)
	}
	if count <= 0 {
		return nil, nil
	}

	spoken := e.spokenSoFar
	if spoken > len(spokenWords) {
		spoken = len(spokenWords)
	}
	from := spoken - count
	if from < 0 {
		from = 0
	}

	seg := asr.Segment{}
	for i := from; i < spoken; i++ {
		at := time.Duration(i-from) * e.perWord
		seg.Words = append(seg.Words, asr.Word{
			Text: " " + spokenWords[i], Start: at, End: at + e.perWord,
		})
	}
	if len(seg.Words) == 0 {
		return nil, nil
	}
	seg.End = time.Duration(len(seg.Words)) * e.perWord
	seg.Text = strings.TrimSpace(strings.Join(wordTexts(seg.Words), ""))
	return []asr.Segment{seg}, nil
}
