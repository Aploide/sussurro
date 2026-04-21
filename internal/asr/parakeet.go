package asr

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	parakeetEncoderFile  = "encoder-model.int8.onnx"
	parakeetDecoderFile  = "decoder_joint-model.int8.onnx"
	parakeetEncoderAlt   = "encoder-model.onnx"
	parakeetDecoderAlt   = "decoder_joint-model.onnx"
	parakeetVocabFile    = "vocab.txt"
	parakeetBlankToken   = "<blk>"
	parakeetMaxTokenStep = 10
	parakeetMinAudioSecs = 2
	parakeetFeatureSize  = 128
)

var (
	ortMu   sync.Mutex
	ortRefs int
)

type ParakeetEngine struct {
	encoder      *ort.DynamicAdvancedSession
	decoder      *ort.DynamicAdvancedSession
	vocab        map[int]string
	blankID      int
	startID      int
	promptIDs    []int
	decoderInEnc []int64
	state1Shape  []int64
	state2Shape  []int64
	vocabSize    int
	closed       bool
	transcribeMu sync.Mutex
}

type encoderLayout int

const (
	encoderLayoutDimTime encoderLayout = iota // [1, dim, time]
	encoderLayoutTimeDim                      // [1, time, dim]
)

func NewParakeetEngine(modelPath string, debug bool) (*ParakeetEngine, error) {
	_ = debug
	modelDir := filepath.Dir(modelPath)
	encoderPath := firstExistingPath(modelDir, parakeetEncoderFile, parakeetEncoderAlt)
	decoderPath := firstExistingPath(modelDir, parakeetDecoderFile, parakeetDecoderAlt)
	vocabPath := filepath.Join(modelDir, parakeetVocabFile)

	for _, p := range []string{encoderPath, decoderPath, vocabPath} {
		if p == "" {
			return nil, fmt.Errorf("required parakeet files are missing in %s", modelDir)
		}
		if st, err := os.Stat(p); err != nil || st.Size() == 0 {
			return nil, fmt.Errorf("required parakeet file missing or empty: %s", p)
		}
	}

	if err := acquireOrtEnvironment(); err != nil {
		return nil, err
	}

	cleanupORT := true
	defer func() {
		if cleanupORT {
			releaseOrtEnvironment()
		}
	}()

	encInputs := []string{"audio_signal", "length"}
	encOutputs := []string{"outputs", "encoded_lengths"}
	encoder, err := ort.NewDynamicAdvancedSession(encoderPath, encInputs, encOutputs, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to load parakeet encoder: %w", err)
	}

	decInputs := []string{"encoder_outputs", "targets", "target_length", "input_states_1", "input_states_2"}
	decOutputs := []string{"outputs", "output_states_1", "output_states_2"}
	decoder, err := ort.NewDynamicAdvancedSession(decoderPath, decInputs, decOutputs, nil)
	if err != nil {
		_ = encoder.Destroy()
		return nil, fmt.Errorf("failed to load parakeet decoder: %w", err)
	}

	inputsInfo, _, err := ort.GetInputOutputInfo(decoderPath)
	if err != nil {
		_ = encoder.Destroy()
		_ = decoder.Destroy()
		return nil, fmt.Errorf("failed to inspect parakeet decoder input shapes: %w", err)
	}
	state1Shape, state2Shape, decoderInEnc, err := parseDecoderInputShapes(inputsInfo)
	if err != nil {
		_ = encoder.Destroy()
		_ = decoder.Destroy()
		return nil, err
	}

	vocabRaw, err := os.ReadFile(vocabPath)
	if err != nil {
		_ = encoder.Destroy()
		_ = decoder.Destroy()
		return nil, fmt.Errorf("failed to read parakeet vocab: %w", err)
	}
	vocab := ParseParakeetVocab(string(vocabRaw))
	if len(vocab) == 0 {
		_ = encoder.Destroy()
		_ = decoder.Destroy()
		return nil, fmt.Errorf("empty parakeet vocab")
	}

	blankID := -1
	startID := -1
	maxID := -1
	for id, token := range vocab {
		if token == parakeetBlankToken {
			blankID = id
		}
		if token == "<|startoftranscript|>" {
			startID = id
		}
		if id > maxID {
			maxID = id
		}
	}
	if blankID < 0 {
		_ = encoder.Destroy()
		_ = decoder.Destroy()
		return nil, fmt.Errorf("parakeet vocab does not include %q token", parakeetBlankToken)
	}
	promptIDs := buildPromptIDs(vocab)
	if len(promptIDs) == 0 && startID >= 0 {
		promptIDs = []int{startID}
	}

	cleanupORT = false
	return &ParakeetEngine{
		encoder:      encoder,
		decoder:      decoder,
		vocab:        vocab,
		blankID:      blankID,
		startID:      startID,
		promptIDs:    promptIDs,
		decoderInEnc: decoderInEnc,
		state1Shape:  state1Shape,
		state2Shape:  state2Shape,
		vocabSize:    maxID + 1,
	}, nil
}

func (e *ParakeetEngine) EngineName() string { return "parakeet-v3-onnxruntime" }

func (e *ParakeetEngine) Close() {
	e.transcribeMu.Lock()
	defer e.transcribeMu.Unlock()
	if e.closed {
		return
	}
	e.closed = true
	if e.encoder != nil {
		_ = e.encoder.Destroy()
	}
	if e.decoder != nil {
		_ = e.decoder.Destroy()
	}
	releaseOrtEnvironment()
}

func (e *ParakeetEngine) Transcribe(samples []float32) (string, error) {
	e.transcribeMu.Lock()
	defer e.transcribeMu.Unlock()

	if e.closed {
		return "", fmt.Errorf("parakeet engine is closed")
	}
	if len(samples) == 0 || len(samples) < 16000*parakeetMinAudioSecs {
		return "", nil
	}

	features, featureFrames, err := nemo128Features(samples)
	if err != nil {
		return "", fmt.Errorf("parakeet preprocessor failed: %w", err)
	}

	encInput, err := ort.NewTensor(ort.NewShape(1, parakeetFeatureSize, int64(featureFrames)), features)
	if err != nil {
		return "", fmt.Errorf("failed creating encoder input tensor: %w", err)
	}
	defer encInput.Destroy()

	encLen, err := ort.NewTensor(ort.NewShape(1), []int64{int64(featureFrames)})
	if err != nil {
		return "", fmt.Errorf("failed creating encoder length tensor: %w", err)
	}
	defer encLen.Destroy()

	encOutputs := []ort.Value{nil, nil}
	if err := e.encoder.Run([]ort.Value{encInput, encLen}, encOutputs); err != nil {
		return "", fmt.Errorf("parakeet encoder run failed: %w", err)
	}
	defer destroyOrtValues(encOutputs)

	encData, encShape, err := valueAsFloat32(encOutputs[0])
	if err != nil {
		return "", fmt.Errorf("invalid encoder output tensor: %w", err)
	}
	if len(encShape) != 3 {
		return "", fmt.Errorf("unexpected encoder output shape: %v", encShape)
	}
	encLens, err := valueAsInt64(encOutputs[1])
	if err != nil || len(encLens) == 0 {
		return "", fmt.Errorf("invalid encoder length output")
	}

	dim, steps, layout := inferEncoderLayout(encShape, int(encLens[0]))
	maxSteps := int(encLens[0])
	if maxSteps > steps {
		maxSteps = steps
	}
	if maxSteps <= 0 {
		return "", nil
	}

	state1 := make([]float32, shapeProduct(e.state1Shape))
	state2 := make([]float32, shapeProduct(e.state2Shape))
	tokens, err := e.greedyDecode(encData, dim, steps, maxSteps, layout, state1, state2)
	if err != nil {
		return "", err
	}

	parts := make([]string, 0, len(tokens))
	for _, id := range tokens {
		token, ok := e.vocab[id]
		if !ok || token == parakeetBlankToken || strings.HasPrefix(token, "<|") {
			continue
		}
		parts = append(parts, token)
	}
	text := strings.Join(parts, "")
	replacer := strings.NewReplacer("  ", " ", " ,", ",", " .", ".", " !", "!", " ?", "?")
	for i := 0; i < 3; i++ {
		text = replacer.Replace(text)
	}
	return strings.TrimSpace(text), nil
}

func (e *ParakeetEngine) greedyDecode(encData []float32, dim, steps, maxSteps int, layout encoderLayout, state1, state2 []float32) ([]int, error) {
	tokens := make([]int, 0, 256)
	if len(e.promptIDs) > 0 {
		tokens = append(tokens, e.promptIDs...)
	}
	t := 0
	emitted := 0

	for t < maxSteps {
		encStep := make([]float32, dim)
		for d := 0; d < dim; d++ {
			if layout == encoderLayoutTimeDim {
				encStep[d] = encData[t*dim+d]
				continue
			}
			encStep[d] = encData[d*steps+t]
		}
		lastToken := e.blankID
		if len(tokens) == 0 && e.startID >= 0 {
			lastToken = e.startID
		}
		if len(tokens) > 0 {
			lastToken = tokens[len(tokens)-1]
		}

		encTensor, err := ort.NewTensor(ort.NewShape(decoderStepShape(e.decoderInEnc, dim)...), encStep)
		if err != nil {
			return nil, fmt.Errorf("failed creating decoder encoder_outputs tensor: %w", err)
		}
		targetTensor, err := ort.NewTensor(ort.NewShape(1, 1), []int32{int32(lastToken)})
		if err != nil {
			encTensor.Destroy()
			return nil, fmt.Errorf("failed creating decoder targets tensor: %w", err)
		}
		targetLen, err := ort.NewTensor(ort.NewShape(1), []int32{1})
		if err != nil {
			encTensor.Destroy()
			targetTensor.Destroy()
			return nil, fmt.Errorf("failed creating decoder target_length tensor: %w", err)
		}
		state1Tensor, err := ort.NewTensor(ort.NewShape(e.state1Shape...), state1)
		if err != nil {
			encTensor.Destroy()
			targetTensor.Destroy()
			targetLen.Destroy()
			return nil, fmt.Errorf("failed creating decoder state1 tensor: %w", err)
		}
		state2Tensor, err := ort.NewTensor(ort.NewShape(e.state2Shape...), state2)
		if err != nil {
			encTensor.Destroy()
			targetTensor.Destroy()
			targetLen.Destroy()
			state1Tensor.Destroy()
			return nil, fmt.Errorf("failed creating decoder state2 tensor: %w", err)
		}

		decOut := []ort.Value{nil, nil, nil}
		runErr := e.decoder.Run([]ort.Value{encTensor, targetTensor, targetLen, state1Tensor, state2Tensor}, decOut)
		encTensor.Destroy()
		targetTensor.Destroy()
		targetLen.Destroy()
		state1Tensor.Destroy()
		state2Tensor.Destroy()
		if runErr != nil {
			destroyOrtValues(decOut)
			return nil, fmt.Errorf("parakeet decoder run failed: %w", runErr)
		}

		logits, _, err := valueAsFloat32(decOut[0])
		if err != nil {
			destroyOrtValues(decOut)
			return nil, fmt.Errorf("invalid decoder logits: %w", err)
		}
		nextState1, _, err := valueAsFloat32(decOut[1])
		if err != nil {
			destroyOrtValues(decOut)
			return nil, fmt.Errorf("invalid decoder state1: %w", err)
		}
		nextState2, _, err := valueAsFloat32(decOut[2])
		if err != nil {
			destroyOrtValues(decOut)
			return nil, fmt.Errorf("invalid decoder state2: %w", err)
		}
		destroyOrtValues(decOut)

		if len(logits) <= e.vocabSize {
			return nil, fmt.Errorf("unexpected decoder logits size: %d", len(logits))
		}
		copy(state1, nextState1)
		copy(state2, nextState2)

		token := argmax(logits[:e.vocabSize])
		step := argmax(logits[e.vocabSize:])
		if token != e.blankID {
			tokens = append(tokens, token)
			emitted++
		}

		if step > 0 {
			t += step
			emitted = 0
		} else if token == e.blankID || emitted == parakeetMaxTokenStep {
			t++
			emitted = 0
		}
	}
	return tokens, nil
}

func valueAsFloat32(v ort.Value) ([]float32, []int64, error) {
	t, ok := v.(*ort.Tensor[float32])
	if !ok {
		return nil, nil, fmt.Errorf("expected float32 tensor, got %T", v)
	}
	return t.GetData(), []int64(t.GetShape()), nil
}

func valueAsInt64(v ort.Value) ([]int64, error) {
	if t, ok := v.(*ort.Tensor[int64]); ok {
		return t.GetData(), nil
	}
	if t, ok := v.(*ort.Tensor[int32]); ok {
		src := t.GetData()
		out := make([]int64, len(src))
		for i := range src {
			out[i] = int64(src[i])
		}
		return out, nil
	}
	return nil, fmt.Errorf("expected int64/int32 tensor, got %T", v)
}

func destroyOrtValues(values []ort.Value) {
	for _, v := range values {
		if v != nil {
			_ = v.Destroy()
		}
	}
}

func parseDecoderInputShapes(inputs []ort.InputOutputInfo) ([]int64, []int64, []int64, error) {
	var s1, s2 []int64
	var encIn []int64
	for _, in := range inputs {
		switch in.Name {
		case "encoder_outputs":
			encIn = append([]int64(nil), in.Dimensions...)
		case "input_states_1":
			s1 = sanitizeShape(in.Dimensions)
		case "input_states_2":
			s2 = sanitizeShape(in.Dimensions)
		}
	}
	if len(encIn) == 0 || len(s1) == 0 || len(s2) == 0 {
		return nil, nil, nil, fmt.Errorf("could not discover decoder input shapes")
	}
	return s1, s2, encIn, nil
}

func sanitizeShape(shape []int64) []int64 {
	out := make([]int64, len(shape))
	for i, d := range shape {
		if d <= 0 {
			out[i] = 1
		} else {
			out[i] = d
		}
	}
	return out
}

func shapeProduct(shape []int64) int {
	n := 1
	for _, d := range shape {
		n *= int(d)
	}
	return n
}

func inferEncoderLayout(shape []int64, encodedLen int) (dim int, steps int, layout encoderLayout) {
	if len(shape) != 3 {
		return 0, 0, encoderLayoutDimTime
	}

	// Candidate A: [1, dim, time]
	aDim, aSteps := int(shape[1]), int(shape[2])
	// Candidate B: [1, time, dim]
	bDim, bSteps := int(shape[2]), int(shape[1])

	aValid := encodedLen > 0 && aSteps >= encodedLen
	bValid := encodedLen > 0 && bSteps >= encodedLen

	if aValid && !bValid {
		return aDim, aSteps, encoderLayoutDimTime
	}
	if bValid && !aValid {
		return bDim, bSteps, encoderLayoutTimeDim
	}
	if aValid && bValid {
		if absInt(aSteps-encodedLen) <= absInt(bSteps-encodedLen) {
			return aDim, aSteps, encoderLayoutDimTime
		}
		return bDim, bSteps, encoderLayoutTimeDim
	}

	// Fallback when lengths are unavailable/unexpected: prefer larger feature dim.
	if aDim >= bDim {
		return aDim, aSteps, encoderLayoutDimTime
	}
	return bDim, bSteps, encoderLayoutTimeDim
}

func decoderStepShape(template []int64, dim int) []int64 {
	if len(template) != 3 {
		return []int64{1, int64(dim), 1}
	}

	out := []int64{1, 1, 1}
	axis := 1
	if template[2] > template[1] {
		axis = 2
	}
	if template[1] == int64(dim) {
		axis = 1
	}
	if template[2] == int64(dim) {
		axis = 2
	}
	out[axis] = int64(dim)
	return out
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func firstExistingPath(dir string, names ...string) string {
	for _, n := range names {
		p := filepath.Join(dir, n)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func acquireOrtEnvironment() error {
	ortMu.Lock()
	defer ortMu.Unlock()

	if ortRefs > 0 {
		ortRefs++
		return nil
	}
	libPath, err := detectOnnxRuntimeLibrary()
	if err != nil {
		return err
	}
	ort.SetSharedLibraryPath(libPath)
	if err := ort.InitializeEnvironment(); err != nil {
		return fmt.Errorf("failed to initialize ONNX Runtime from %s: %w", libPath, err)
	}
	ortRefs = 1
	return nil
}

func releaseOrtEnvironment() {
	ortMu.Lock()
	defer ortMu.Unlock()
	if ortRefs <= 0 {
		return
	}
	ortRefs--
	if ortRefs == 0 {
		_ = ort.DestroyEnvironment()
	}
}

func detectOnnxRuntimeLibrary() (string, error) {
	candidates := make([]string, 0, 16)
	if p := strings.TrimSpace(os.Getenv("SUSSURRO_ONNXRUNTIME_LIB")); p != "" {
		candidates = append(candidates, p)
	}
	if p := strings.TrimSpace(os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH")); p != "" {
		candidates = append(candidates, p)
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "linux":
		candidates = append(candidates,
			filepath.Join(home, ".sussurro", "onnxruntime", "libonnxruntime.so.1.24.1"),
			filepath.Join(home, ".sussurro", "onnxruntime", "libonnxruntime.so"),
			"/usr/lib/libonnxruntime.so",
			"/usr/lib/libonnxruntime.so.1",
			"/usr/lib/libonnxruntime.so.1.24.1",
			"/usr/lib/x86_64-linux-gnu/libonnxruntime.so",
			"/usr/lib/x86_64-linux-gnu/libonnxruntime.so.1",
			"/usr/lib/x86_64-linux-gnu/libonnxruntime.so.1.24.1",
			"/usr/local/lib/libonnxruntime.so",
			"/usr/local/lib/libonnxruntime.so.1",
			"/usr/local/lib/libonnxruntime.so.1.24.1",
		)
	case "darwin":
		candidates = append(candidates,
			filepath.Join(home, ".sussurro", "onnxruntime", "libonnxruntime.dylib"),
			"/opt/homebrew/lib/libonnxruntime.dylib",
			"/usr/local/lib/libonnxruntime.dylib",
			"/usr/lib/libonnxruntime.dylib",
		)
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("onnxruntime shared library not found; set SUSSURRO_ONNXRUNTIME_LIB to your onnxruntime library path")
}

func ParseParakeetVocab(content string) map[int]string {
	out := make(map[int]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lastSpace := strings.LastIndexByte(line, ' ')
		if lastSpace <= 0 || lastSpace >= len(line)-1 {
			continue
		}
		token := strings.ReplaceAll(line[:lastSpace], "\u2581", " ")
		var id int
		if _, err := fmt.Sscanf(line[lastSpace+1:], "%d", &id); err != nil {
			continue
		}
		out[id] = token
	}
	return out
}

func buildPromptIDs(vocab map[int]string) []int {
	byToken := make(map[string]int, len(vocab))
	for id, token := range vocab {
		byToken[token] = id
	}
	prompt := make([]int, 0, 4)
	for _, tok := range []string{"<|startoftranscript|>", "<|en|>", "<|pnc|>", "<|itn|>"} {
		if id, ok := byToken[tok]; ok {
			prompt = append(prompt, id)
		}
	}
	return prompt
}

func argmax(v []float32) int {
	if len(v) == 0 {
		return 0
	}
	bestI := 0
	bestV := v[0]
	for i := 1; i < len(v); i++ {
		if v[i] > bestV {
			bestV = v[i]
			bestI = i
		}
	}
	return bestI
}
