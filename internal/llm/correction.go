package llm

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	llama "github.com/AshkanYarmoradi/go-llama.cpp"
	"github.com/aploide/sussurro/internal/logger"
)

const (
	correctionChunkTarget = 500
	correctionMaxTokens   = 512
)

// The bundled model is fine-tuned against this exact system prompt and goes
// silent when it is replaced with a narrower correction prompt. Its broader
// proposals are safe here because validCorrections admits only substitutions.
const correctionSystemPrompt = defaultSystemPrompt

const strictCorrectionSystemPrompt = `You correct only obvious speech-recognition mistakes using the surrounding words in the transcript.

Rules:
- Keep every word in the same order.
- Change only a misheard word to a similar-sounding word that clearly fits its context.
- Capitalization corrections such as "base blockchain" to "Base blockchain" are allowed.
- One heard word may become two, or two may become one, only when they sound alike.
- Do not change punctuation.
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
	corrected := make([]string, 0, len(chunks))
	changed := false
	for _, chunk := range chunks {
		result := e.correctMishearingChunk(chunk)
		corrected = append(corrected, result)
		changed = changed || result != chunk
	}
	if !changed {
		return text
	}
	return strings.Join(corrected, " ")
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

	if !e.debug {
		restore := logger.SuppressStderr()
		defer restore()
	}

	candidate, err := e.model.Predict(prompt, correctionPredictOptions(e.threads)...)
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

func correctionPredictOptions(threads int) []llama.PredictOption {
	return []llama.PredictOption{
		llama.SetTokens(correctionMaxTokens),
		llama.SetThreads(threads),
		llama.SetTemperature(0.1),
		llama.SetTopP(0.9),
		llama.SetStopWords("<|im_end|>"),
	}
}

// splitCorrectionChunks keeps sentence context together while bounding model
// work. A single overlong sentence stays intact rather than being cut where a
// homophone may depend on words across the cut.
func splitCorrectionChunks(text string, target int) []string {
	text = strings.TrimSpace(text)
	if len(text) <= target {
		return []string{text}
	}
	sentences := splitSentences(text)
	if len(sentences) < 2 {
		return []string{text}
	}

	var chunks []string
	var current strings.Builder
	for _, sentence := range sentences {
		if current.Len() > 0 && current.Len()+1+len(sentence) > target {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteByte(' ')
		}
		current.WriteString(sentence)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
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
		return correctionToken{}, false
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

// validCorrections accepts only aligned one-token substitutions and phonetic
// one-to-two/two-to-one equivalents. At most one group may change in a short
// dictation, then one additional group per ten input words.
func validCorrections(input, output string) bool {
	inputFields := strings.Fields(input)
	outputFields := strings.Fields(output)
	if len(inputFields) == 0 || len(outputFields) == 0 {
		return input == output
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
	type state struct{ i, j, changes int }
	seen := make(map[state]bool)
	var align func(i, j, changes int) bool
	align = func(i, j, changes int) bool {
		if changes > maxChanges {
			return false
		}
		if i == len(in) || j == len(out) {
			return i == len(in) && j == len(out) && (input == output || changes > 0)
		}
		s := state{i, j, changes}
		if seen[s] {
			return false
		}
		seen[s] = true

		if in[i].raw == out[j].raw && align(i+1, j+1, changes) {
			return true
		}
		if correctionGroupAllowed(in[i:i+1], out[j:j+1]) && align(i+1, j+1, changes+1) {
			return true
		}
		if j+2 <= len(out) && correctionGroupAllowed(in[i:i+1], out[j:j+2]) && align(i+1, j+2, changes+1) {
			return true
		}
		return i+2 <= len(in) && correctionGroupAllowed(in[i:i+2], out[j:j+1]) && align(i+2, j+1, changes+1)
	}
	return align(0, 0, 0)
}

func correctionGroupAllowed(input, output []correctionToken) bool {
	if len(input) == 0 || len(output) == 0 {
		return false
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
