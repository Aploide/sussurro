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

	for _, word := range fields {
		if isFiller(word) {
			continue
		}
		// Collapse an immediate repetition ("the the the" -> "the"), which is
		// a stutter rather than emphasis. Compared on the bare word so
		// punctuation does not defeat the match.
		if len(out) > 0 && sameWord(out[len(out)-1], word) {
			// Keep whichever copy carries the punctuation, so "the the."
			// ends up as "the." rather than "the".
			if len(bareWord(word)) < len(word) {
				out[len(out)-1] = word
			}
			continue
		}
		out = append(out, word)
	}

	return recapitalize(repairSpacing(strings.Join(out, " ")))
}

// recapitalize restores a capital letter where deleting a filler exposed a
// lowercase word at a sentence start: "Hello? Ah, no, ..." loses its
// capitalised opener and leaves "Hello? no, ...".
//
// This changes case only, never which word was said, so it stays inside the
// no-rewording contract. A word that is not all-lowercase is left alone, so
// deliberate capitalisation and acronyms survive untouched.
func recapitalize(text string) string {
	runes := []rune(text)
	atSentenceStart := true

	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if atSentenceStart && unicode.IsLetter(r) {
			if unicode.IsLower(r) && wordIsLower(runes, i) {
				runes[i] = unicode.ToUpper(r)
			}
			atSentenceStart = false
			continue
		}

		// A terminator opens the next sentence, once past any closing quote
		// or bracket that belongs to the one just ended.
		if r == '.' || r == '?' || r == '!' {
			atSentenceStart = true
		} else if !unicode.IsSpace(r) && r != '"' && r != '\'' && r != ')' && r != ']' {
			atSentenceStart = false
		}
	}

	return string(runes)
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

// repairSpacing tidies punctuation left stranded by deletions, without
// altering any word. Removing "um" from "Well, um, it failed" would otherwise
// leave a doubled comma.
func repairSpacing(text string) string {
	text = strings.TrimSpace(text)

	// Collapse punctuation runs produced by removing the word between them.
	for _, pair := range []struct{ from, to string }{
		{", ,", ","}, {",,", ","}, {". .", "."}, {"..", "."},
		{" ,", ","}, {" .", "."}, {" ?", "?"}, {" !", "!"},
	} {
		for strings.Contains(text, pair.from) {
			text = strings.ReplaceAll(text, pair.from, pair.to)
		}
	}

	// A leading comma is left when the first word was a filler.
	text = strings.TrimLeft(text, ", ")

	return strings.TrimSpace(text)
}
