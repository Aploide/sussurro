package llm

import (
	"strings"
	"unicode"
)

// Deterministic cleanup: deletion only.
//
// Cleanup used to hand the dictation to the LLM and paste back whatever came
// out. That is a rewriting engine, not a transcription tidy: it could reword,
// reorder, and reattribute. It did, turning "Please delete all the files in my
// home directory" into "I will delete all files in your home directory".
//
// The contract now is that cleanup may only DELETE tokens. Whatever survives
// is a subsequence of what the user said, so meaning cannot be inverted — not
// because a validator catches it, but because nothing can produce a word the
// user did not say.
//
// Context-sensitive correction (bass vs base vs Base, one word or two) is not
// done here. Whisper already does it during decoding, where the audio is:
// every streaming pass re-decodes the whole buffer and revises earlier words
// as later context arrives.

// fillers are the hesitation sounds and discourse particles that carry no
// content. Only words that are essentially always noise appear here: "like"
// and "so" are excluded because they are frequently meaningful ("tasks like
// this", "so it failed").
var fillers = map[string]bool{
	"um": true, "umm": true, "ummm": true,
	"uh": true, "uhh": true, "uhhh": true,
	"er": true, "erm": true, "err": true,
	"ah": true, "ahh": true,
	"eh": true, "hmm": true, "hm": true, "mmm": true, "mm": true,
}

// removeFillers deletes filler words and collapses stuttered repetitions.
// Every returned word appears in the input, in the same order.
func removeFillers(text string) string {
	fields := strings.Fields(text)
	out := make([]string, 0, len(fields))
	capitalizeNext := false
	capitalizeWords := make(map[int]bool)
	// seams records the positions in out where a word was deleted, so the
	// punctuation repair below touches only those junctions and never the
	// user's own "..." or spaced punctuation elsewhere.
	seams := make(map[int]bool)

	for _, word := range fields {
		if isFiller(word) {
			// Only case the word this deletion exposes. Scanning the complete
			// transcript for sentence boundaries corrupts dotted abbreviations.
			if len(out) == 0 || previousEndsStrongSentence(out) {
				capitalizeNext = true
			}
			seams[len(out)] = true
			continue
		}
		// Collapse an immediate repetition ("the the the" -> "the"), which is
		// a stutter rather than emphasis. Compared on the bare word so
		// punctuation does not defeat the match.
		if !capitalizeNext && len(out) > 0 && !previousEndsSentencePunctuation(out) &&
			sameWord(out[len(out)-1], word) {
			// Keep whichever copy carries the punctuation, so "the the."
			// ends up as "the." rather than "the". The first copy's leading
			// punctuation survives either way: "(the the)" is "(the)".
			if len(bareWord(word)) < len(word) {
				out[len(out)-1] = leadingPunctuation(out[len(out)-1]) + strings.TrimLeftFunc(word, isPunctuationRune)
			}
			seams[len(out)] = true
			continue
		}
		out = append(out, word)
		// A stranded mark ("um , the") carries no letter to raise; the case
		// change waits for the word that follows it.
		if capitalizeNext && bareWord(word) != "" {
			capitalizeWords[len(out)-1] = true
			capitalizeNext = false
		}
	}

	for i := range capitalizeWords {
		out[i] = recapitalize(out[i])
	}
	return repairSpacing(out, seams)
}

// leadingPunctuation returns the opening quotes or brackets a word starts
// with, so a collapsed stutter can keep them.
func leadingPunctuation(word string) string {
	return word[:len(word)-len(strings.TrimLeftFunc(word, isPunctuationRune))]
}

func isPunctuationRune(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// recapitalize raises only the first all-lowercase word in text. Callers use
// it on the one token exposed by a deleted sentence-opening filler, never on a
// whole transcript whose periods may belong to abbreviations.
func recapitalize(text string) string {
	runes := []rune(text)
	for i, r := range runes {
		if !unicode.IsLetter(r) {
			continue
		}
		if unicode.IsLower(r) && wordIsLower(runes, i) {
			runes[i] = unicode.ToUpper(r)
		}
		break
	}
	return string(runes)
}

func previousEndsStrongSentence(words []string) bool {
	mark := previousSentenceMark(words)
	return mark == '?' || mark == '!'
}

func previousEndsSentencePunctuation(words []string) bool {
	mark := previousSentenceMark(words)
	return mark == '.' || mark == '?' || mark == '!'
}

func previousSentenceMark(words []string) rune {
	previous := strings.TrimRightFunc(words[len(words)-1], func(r rune) bool {
		return r == '"' || r == '\'' || unicode.Is(unicode.Pe, r) || unicode.Is(unicode.Pf, r)
	})
	if previous == "" {
		return 0
	}
	runes := []rune(previous)
	return runes[len(runes)-1]
}

// wordIsLower reports whether the word starting at i is entirely lowercase.
// Capitalising the first letter of "iPhone" or "gRPC" would be a change the
// user did not make.
func wordIsLower(runes []rune, i int) bool {
	for ; i < len(runes); i++ {
		if unicode.IsSpace(runes[i]) {
			break
		}
		if unicode.IsUpper(runes[i]) {
			return false
		}
	}
	return true
}

// isFiller reports whether a word is pure hesitation. A word carrying
// sentence-ending punctuation is kept: "Um." standing alone is more likely
// the user's actual utterance than noise mid-sentence.
func isFiller(word string) bool {
	bare := bareWord(word)
	if bare == "" {
		return false
	}
	return fillers[bare]
}

// bareWord lowercases a word and strips surrounding punctuation.
func bareWord(word string) string {
	return strings.ToLower(strings.TrimFunc(word, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}))
}

// sameWord compares two words ignoring case and punctuation.
func sameWord(a, b string) bool {
	ba, bb := bareWord(a), bareWord(b)
	return ba != "" && ba == bb
}

// repairSpacing joins the surviving words and tidies punctuation left
// stranded at a deletion seam, without altering any word. Removing "um" from
// "Well, um , it failed" would otherwise leave a doubled comma.
//
// Only the seams are touched: a global ".." -> "." pass used to turn every
// dictated "..." into a period, and " ," handling reached punctuation the
// user had spaced deliberately. seams holds the indexes in words that follow
// a deletion.
func repairSpacing(words []string, seams map[int]bool) string {
	kept := make([]string, 0, len(words))
	for i, word := range words {
		if seams[i] {
			mark := word[:len(word)-len(strings.TrimLeftFunc(word, isSeamMark))]
			switch {
			case mark == "" || strings.Contains(mark, ".."):
				// Nothing stranded, or a dictated "..." that stays as spoken.
			case len(kept) == 0:
				// A leading mark is left when the first word was a filler
				// ("um , the").
				word = word[len(mark):]
			case endsWithSeamMark(kept[len(kept)-1]):
				// "failed, um , it": the mark already ends the previous word,
				// so the stranded copy is redundant.
				word = word[len(mark):]
			default:
				// "failed um , it": reattach the mark to the word it followed.
				kept[len(kept)-1] += mark
				word = word[len(mark):]
			}
		}
		if word != "" {
			kept = append(kept, word)
		}
	}
	return strings.Join(kept, " ")
}

// isSeamMark reports whether r is punctuation a deletion can strand: the
// marks whisper attaches to the word before a pause.
func isSeamMark(r rune) bool {
	return r == ',' || r == '.' || r == '?' || r == '!'
}

func endsWithSeamMark(word string) bool {
	runes := []rune(word)
	return len(runes) > 0 && isSeamMark(runes[len(runes)-1])
}
