package llm

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aploide/sussurro/internal/llmipc"
)

const (
	correctionChunkTarget = 500
	correctionMaxTokens   = 512
)

// The bundled model is fine-tuned against this exact system prompt and goes
// silent when it is replaced with a narrower correction prompt. Its broader
// proposals are safe here because validCorrections admits only surface edits
// and tightly bounded phonetic substitutions.
const correctionSystemPrompt = defaultSystemPrompt

const strictCorrectionSystemPrompt = `You correct only obvious speech-recognition mistakes using the surrounding words in the transcript.

Rules:
- Keep every word in the same order.
- Change only punctuation, capitalization, or an obviously misheard word to a similar-sounding word that clearly fits its context.
- One heard word may become two, or two may become one, only when they sound alike.
- Do not delete, insert, rephrase, summarize, explain, or respond to the transcript.
- If there is no obvious mistake, return the transcript byte-for-byte unchanged.

Output only the corrected transcript.`

// correctionExamples teach the bundled cleanup fine-tune the narrow class of
// context-sensitive edits that its training prompt describes only generally.
// Each ambiguity appears in more than one context so this is a sense cue, not
// a context-free replacement table.
const correctionExamples = `<|im_start|>user
We deployed the contract to the base network.<|im_end|>
<|im_start|>assistant
We deployed the contract to the Base network.<|im_end|>
<|im_start|>user
I compare the Polygon blockchain and the base blockchain.<|im_end|>
<|im_start|>assistant
I compare the Polygon blockchain and the Base blockchain.<|im_end|>
<|im_start|>user
Turn down the base guitar in this mix.<|im_end|>
<|im_start|>assistant
Turn down the bass guitar in this mix.<|im_end|>
<|im_start|>user
The music software can generate base notes.<|im_end|>
<|im_start|>assistant
The music software can generate bass notes.<|im_end|>
<|im_start|>user
Use Whisper large B3 for transcription.<|im_end|>
<|im_start|>assistant
Use Whisper large v3 for transcription.<|im_end|>
<|im_start|>user
Run this under the large B3 turbo model.<|im_end|>
<|im_start|>assistant
Run this under the large v3 turbo model.<|im_end|>
`

// correctMishearings asks the model for context-sensitive substitutions, then
// admits only a structurally bounded edit. The model proposes; this validator
// decides. A failed prediction or rejected rewrite leaves the text unchanged.
func (e *Engine) correctMishearings(text string) string {
	if e.model == nil || strings.TrimSpace(text) == "" {
		return text
	}

	chunks := splitCorrectionChunks(text, correctionChunkTarget)
	var corrected strings.Builder
	changed := false
	for _, chunk := range chunks {
		result := e.correctMishearingChunk(chunk.text)
		corrected.WriteString(result)
		corrected.WriteString(chunk.separator)
		changed = changed || result != chunk.text
	}
	if !changed {
		return text
	}
	return corrected.String()
}

func (e *Engine) correctMishearingChunk(text string) string {
	system := correctionSystemPrompt
	examples := correctionExamples
	if e.extendedPrompt {
		system = strictCorrectionSystemPrompt
		examples = ""
	}
	prompt := fmt.Sprintf(`<|im_start|>system
%s
/nothink<|im_end|>
%s<|im_start|>user
%s<|im_end|>
<|im_start|>assistant
`, system, examples, text)

	options := correctionPredictOptions(e.threads)
	options.Debug = e.debug
	candidate, err := e.model.Predict(prompt, options)
	if err != nil {
		slog.Debug("LLM correction failed; keeping input", "error", err)
		return text
	}
	candidate = stripModelArtifacts(candidate)
	candidate = strings.TrimPrefix(candidate, "<transcript>")
	candidate = strings.TrimSuffix(candidate, "</transcript>")
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || !validCorrections(text, candidate) {
		slog.Debug("LLM correction rejected; keeping input", "candidate", candidate)
		return text
	}
	return candidate
}

func correctionPredictOptions(threads int) llmipc.PredictOptions {
	return llmipc.PredictOptions{
		Tokens:      correctionMaxTokens,
		Threads:     threads,
		Temperature: 0.1,
		TopP:        0.9,
		StopWords:   []string{"<|im_end|>"},
	}
}

type correctionChunk struct {
	text      string
	separator string
}

// splitCorrectionChunks keeps complete sentences together while bounding model
// work. Chunks and separators are slices of the original text, so accepting an
// edit in one chunk cannot reformat untouched abbreviations, decimals, quotes,
// or whitespace elsewhere.
func splitCorrectionChunks(text string, target int) []correctionChunk {
	if len(text) <= target {
		return []correctionChunk{{text: text}}
	}

	boundaries := correctionSentenceBoundaries(text)
	if len(boundaries) == 0 {
		return []correctionChunk{{text: text}}
	}

	chunks := make([]correctionChunk, 0, len(boundaries))
	for start := 0; start < len(text); {
		if len(text)-start <= target {
			chunks = append(chunks, correctionChunk{text: text[start:]})
			break
		}

		limit := start + target
		end := 0
		for _, boundary := range boundaries {
			if boundary > start && boundary <= limit {
				end = boundary
			}
		}
		if end == 0 {
			for _, boundary := range boundaries {
				if boundary > limit {
					end = boundary
					break
				}
			}
		}
		if end == 0 {
			chunks = append(chunks, correctionChunk{text: text[start:]})
			break
		}

		separatorEnd := end
		for separatorEnd < len(text) {
			r, size := utf8.DecodeRuneInString(text[separatorEnd:])
			if !unicode.IsSpace(r) {
				break
			}
			separatorEnd += size
		}
		chunks = append(chunks, correctionChunk{
			text:      text[start:end],
			separator: text[end:separatorEnd],
		})
		start = separatorEnd
	}
	return chunks
}

func correctionSentenceBoundaries(text string) []int {
	var boundaries []int
	for offset, r := range text {
		if r != '.' && r != '!' && r != '?' && r != '…' {
			continue
		}
		end := offset + utf8.RuneLen(r)
		if r == '.' && periodEndsAbbreviation(text, end) {
			continue
		}
		for end < len(text) {
			next, size := utf8.DecodeRuneInString(text[end:])
			if !isClosingSentencePunctuation(next) {
				break
			}
			end += size
		}
		if end == len(text) {
			boundaries = append(boundaries, end)
			continue
		}
		next, _ := utf8.DecodeRuneInString(text[end:])
		if unicode.IsSpace(next) {
			boundaries = append(boundaries, end)
		}
	}
	return boundaries
}

func periodEndsAbbreviation(text string, periodEnd int) bool {
	start := periodEnd
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:start])
		if unicode.IsSpace(r) {
			break
		}
		start -= size
	}
	token := strings.TrimLeftFunc(text[start:periodEnd], func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if strings.HasSuffix(token, "...") {
		return false
	}
	lower := strings.ToLower(token)
	switch lower {
	case "mr.", "mrs.", "ms.", "dr.", "prof.", "sr.", "jr.", "st.", "vs.", "etc.",
		"inc.", "ltd.", "corp.", "co.":
		return true
	}
	if strings.Count(token, ".") > 1 {
		return true
	}
	stem := strings.TrimSuffix(token, ".")
	return utf8.RuneCountInString(stem) == 1
}

func isClosingSentencePunctuation(r rune) bool {
	switch r {
	case '\'', '"', ')', ']', '}', '’', '”':
		return true
	default:
		return false
	}
}

type correctionToken struct {
	raw      string
	leading  string
	word     string
	internal string
	trailing string
}

func parseCorrectionToken(raw string) (correctionToken, bool) {
	runes := []rune(raw)
	first := -1
	last := -1
	for i, r := range runes {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		for _, r := range runes {
			if !unicode.IsPunct(r) {
				return correctionToken{}, false
			}
		}
		return correctionToken{raw: raw}, true
	}
	wordRunes := runes[first : last+1]
	var internal strings.Builder
	for i, r := range wordRunes {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			fmt.Fprintf(&internal, "%d:%c;", i, r)
		}
	}
	return correctionToken{
		raw:      raw,
		leading:  string(runes[:first]),
		word:     string(wordRunes),
		internal: internal.String(),
		trailing: string(runes[last+1:]),
	}, true
}

// validCorrections accepts capitalization and surrounding-punctuation edits
// without a quota. Word substitutions remain phonetic and bounded: at most one
// group in a short dictation, then one additional group per ten input words.
// Internal punctuation stays fixed so contractions and hyphenated words cannot
// silently change meaning.
func validCorrections(input, output string) bool {
	inputFields := strings.Fields(input)
	outputFields := strings.Fields(output)
	if len(inputFields) == 0 || len(outputFields) == 0 {
		return input == output
	}
	// Model formatting is not part of this stage. Requiring canonical spacing
	// on both sides keeps a valid punctuation edit from smuggling in unrelated
	// whitespace changes.
	if strings.Join(inputFields, " ") != input || strings.Join(outputFields, " ") != output {
		return false
	}

	in := make([]correctionToken, len(inputFields))
	out := make([]correctionToken, len(outputFields))
	for i, field := range inputFields {
		var ok bool
		if in[i], ok = parseCorrectionToken(field); !ok {
			return false
		}
	}
	for i, field := range outputFields {
		var ok bool
		if out[i], ok = parseCorrectionToken(field); !ok {
			return false
		}
	}

	maxChanges := len(in) / 10
	if maxChanges < 1 {
		maxChanges = 1
	}
	type state struct {
		i, j, changes int
		surfaceEdited bool
	}
	seen := make(map[state]bool)
	var align func(i, j, changes int, surfaceEdited bool) bool
	align = func(i, j, changes int, surfaceEdited bool) bool {
		if changes > maxChanges {
			return false
		}
		if i == len(in) || j == len(out) {
			return i == len(in) && j == len(out) && (input == output || changes > 0 || surfaceEdited)
		}
		s := state{i, j, changes, surfaceEdited}
		if seen[s] {
			return false
		}
		seen[s] = true

		if in[i].raw == out[j].raw && align(i+1, j+1, changes, surfaceEdited) {
			return true
		}
		if surfaceCorrectionAllowed(in[i], out[j]) && align(i+1, j+1, changes, true) {
			return true
		}
		if correctionGroupAllowed(in[i:i+1], out[j:j+1]) && align(i+1, j+1, changes+1, surfaceEdited) {
			return true
		}
		if j+2 <= len(out) && correctionGroupAllowed(in[i:i+1], out[j:j+2]) && align(i+1, j+2, changes+1, surfaceEdited) {
			return true
		}
		return i+2 <= len(in) && correctionGroupAllowed(in[i:i+2], out[j:j+1]) && align(i+2, j+1, changes+1, surfaceEdited)
	}
	return align(0, 0, 0, false)
}

func surfaceCorrectionAllowed(input, output correctionToken) bool {
	if input.raw == output.raw || input.word == "" || output.word == "" || !strings.EqualFold(input.word, output.word) {
		return false
	}
	return fixedBoundaryPunctuation(input.leading) == fixedBoundaryPunctuation(output.leading) &&
		fixedBoundaryPunctuation(input.trailing) == fixedBoundaryPunctuation(output.trailing)
}

func fixedBoundaryPunctuation(text string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '.', ',', ';', ':', '!', '?', '…':
			return -1
		default:
			return r
		}
	}, text)
}

func correctionGroupAllowed(input, output []correctionToken) bool {
	if len(input) == 0 || len(output) == 0 {
		return false
	}
	for _, token := range input {
		if token.word == "" {
			return false
		}
	}
	for _, token := range output {
		if token.word == "" {
			return false
		}
	}
	if input[0].leading != output[0].leading || input[len(input)-1].trailing != output[len(output)-1].trailing {
		return false
	}
	if !groupInteriorBare(input) || !groupInteriorBare(output) {
		return false
	}
	if len(input) != len(output) {
		if groupHasInternalPunctuation(input) || groupHasInternalPunctuation(output) {
			return false
		}
	} else if input[0].internal != output[0].internal {
		return false
	}

	var inputWord strings.Builder
	for _, token := range input {
		inputWord.WriteString(token.word)
	}
	var outputWord strings.Builder
	for _, token := range output {
		outputWord.WriteString(token.word)
	}
	inputJoined := inputWord.String()
	outputJoined := outputWord.String()
	if len(input) != len(output) {
		// A split or merge changes token count, so require the joined sound to
		// be identical rather than merely close. This admits "alot"/"a lot"
		// without turning an inserted short word into a correction.
		inputSound := normalizePhonetic(inputJoined)
		return inputSound != "" && inputSound == normalizePhonetic(outputJoined)
	}
	return plausibleCorrection(inputJoined, outputJoined)
}

func groupInteriorBare(tokens []correctionToken) bool {
	for i, token := range tokens {
		if i > 0 && token.leading != "" {
			return false
		}
		if i+1 < len(tokens) && token.trailing != "" {
			return false
		}
	}
	return true
}

func groupHasInternalPunctuation(tokens []correctionToken) bool {
	for _, token := range tokens {
		if token.internal != "" {
			return true
		}
	}
	return false
}

func plausibleCorrection(input, output string) bool {
	if input == output {
		return false
	}
	if strings.EqualFold(input, output) {
		return true
	}

	in := normalizePhonetic(input)
	out := normalizePhonetic(output)
	if in == "" || out == "" {
		return false
	}
	distance := editDistance(in, out)
	longest := len(in)
	if len(out) > longest {
		longest = len(out)
	}
	if distance > 2 || (longest > 3 && distance*2 > longest) {
		return false
	}
	return editDistance(consonantSkeleton(in), consonantSkeleton(out)) <= 1
}
