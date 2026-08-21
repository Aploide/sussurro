package pipeline

import (
	"regexp"
	"strings"
)

// nonSpeechMarker matches Whisper's annotations for non-speech audio, such as
// [BLANK_AUDIO], [MUSIC], (laughter) or *coughs*.
//
// These are descriptions of what the model heard, not transcriptions of what
// the user said, so they are never dictated text. Whisper emits an open-ended
// family of them, and filtering known literals one at a time has to be redone
// for every new one that shows up in a bug report, so this matches the shape
// instead: a short bracketed or asterisked span containing no sentence-ending
// punctuation.
//
// What keeps genuine speech safe is the word count, not the length. Whisper
// writes these as a single annotation token — one word, or two joined by a
// space, hyphen or underscore ([BLANK_AUDIO], [BLANK AUDIO], (cross-talk)).
// Dictated parentheticals are phrases: "(a plus b)" is only eight characters
// but three words, so a length bound alone would strip it. Requiring at most
// two words leaves real speech intact.
var nonSpeechMarker = regexp.MustCompile(`(?i)[\[(*]\s*[a-z]+(?:[ _-][a-z]+)?\s*[\])*]`)

// StripNonSpeechMarkers removes Whisper's non-speech annotations from text and
// tidies the whitespace they leave behind.
//
// Markers are stripped rather than merely hidden: they must not reach the
// clipboard or a paste target either, and text that is nothing but markers is
// no transcription at all, so it reduces to the empty string.
func StripNonSpeechMarkers(text string) string {
	if text == "" {
		return ""
	}

	stripped := nonSpeechMarker.ReplaceAllString(text, " ")
	if stripped == text {
		return text
	}

	return collapseSpaces(stripped)
}

// collapseSpaces normalises the runs of whitespace and the stranded space
// before punctuation that removing an inline marker leaves behind.
func collapseSpaces(text string) string {
	var b strings.Builder
	b.Grow(len(text))

	space := false
	for _, r := range text {
		if r == ' ' || r == '\t' {
			space = true
			continue
		}
		if b.Len() > 0 && space {
			// A marker removed from before a comma or full stop would
			// otherwise leave the punctuation orphaned by a space.
			if !strings.ContainsRune(",.;:!?", r) {
				b.WriteByte(' ')
			}
		}
		space = false
		b.WriteRune(r)
	}

	return strings.TrimSpace(b.String())
}
