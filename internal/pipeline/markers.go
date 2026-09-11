package pipeline

import (
	"regexp"
	"strings"
)

// nonSpeechMarker matches Whisper's annotations for non-speech audio, such as
// [BLANK_AUDIO], [MUSIC], (laughter) or *coughs*.
//
// These are descriptions of what the model heard, not transcriptions of what
// the user said, so they are never dictated text. Two shapes are recognised:
//
//   - an upper-case bracketed token of one word, or two joined by a space,
//     hyphen or underscore, which is how Whisper writes its stock markers
//     ([BLANK_AUDIO], [BLANK AUDIO], [MUSIC]); and
//   - a bracketed or asterisked token from the known list of lower-case
//     annotations it also emits ((laughter), *coughs*, [inaudible]).
//
// An earlier version matched any short bracketed span regardless of case,
// which also swallowed dictated words such as "(optional)" and "*really*".
// Restricting the lower-case form to a known list means a new marker has to
// be added here when it turns up, but a user's own parenthetical is never
// stripped for merely being short.
var nonSpeechMarker = regexp.MustCompile(
	`[\[(*]\s*(?:` +
		`[A-Z]{2,}(?:[ _-][A-Z]{2,})?` +
		`|(?i:` + knownNonSpeechMarkers + `)` +
		`)\s*[\])*]`)

// knownNonSpeechMarkers lists the lower-case annotations Whisper is known to
// emit, as regexp alternatives. Matched case-insensitively.
const knownNonSpeechMarkers = `blank[ _-]?audio|music|applause|laughter|laughs|laughing|chuckles?|giggles?` +
	`|coughs?|coughing|sneezes?|sniffs?|sighs?|breathing|breathes` +
	`|inaudible|unintelligible|indistinct|silence|silent|noise|sound|static` +
	`|cross[ _-]?talk|clears? throat|clicking|beeping|typing`

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
