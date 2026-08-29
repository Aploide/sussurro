package setup

// ModelKind identifies which configured engine consumes a model.
type ModelKind string

const (
	ModelKindASR ModelKind = "whisper"
	ModelKindLLM ModelKind = "llm"

	qwenRepositoryURL = "https://huggingface.co/cesp99/qwen3-sussurro/resolve/main/"
	defaultLLMURL     = qwenRepositoryURL + "qwen3-sussurro-q4_k_m.gguf"
	defaultLLMSize    = "1.28 GB"
)

// ModelSpec is a model Sussurro knows how to download and use. GGUF files not
// in this catalog are not advertised as compatible with the cleanup prompt.
type ModelSpec struct {
	ID          string
	Name        string
	Description string
	Size        string
	Filename    string
	DownloadURL string
	Kind        ModelKind
}

var supportedModels = []ModelSpec{
	{
		ID:          "whisper-small",
		Name:        "Whisper Small",
		Description: "Faster, lower memory usage",
		Size:        "~488 MB",
		Filename:    fileASRSmall,
		DownloadURL: urlASRSmall,
		Kind:        ModelKindASR,
	},
	{
		ID:          "whisper-large-v3-turbo",
		Name:        "Whisper Large v3 Turbo",
		Description: "Higher accuracy, more memory",
		Size:        "~1.62 GB",
		Filename:    fileASRLarge,
		DownloadURL: urlASRLarge,
		Kind:        ModelKindASR,
	},
	{
		ID:          "qwen3-sussurro-q4-k-m",
		Name:        "Qwen 3 Sussurro Q4_K_M",
		Description: "Balanced quality and memory (recommended)",
		Size:        "~1.28 GB",
		Filename:    "qwen3-sussurro-q4_k_m.gguf",
		DownloadURL: defaultLLMURL,
		Kind:        ModelKindLLM,
	},
	{
		ID:          "qwen3-sussurro-q5-k-m",
		Name:        "Qwen 3 Sussurro Q5_K_M",
		Description: "Higher precision, more memory",
		Size:        "~1.47 GB",
		Filename:    "qwen3-sussurro-q5_k_m.gguf",
		DownloadURL: qwenRepositoryURL + "qwen3-sussurro-q5_k_m.gguf",
		Kind:        ModelKindLLM,
	},
	{
		ID:          "qwen3-sussurro-q8-0",
		Name:        "Qwen 3 Sussurro Q8_0",
		Description: "Near-full precision, higher memory usage",
		Size:        "~2.17 GB",
		Filename:    "qwen3-sussurro-q8_0.gguf",
		DownloadURL: qwenRepositoryURL + "qwen3-sussurro-q8_0.gguf",
		Kind:        ModelKindLLM,
	},
	{
		ID:          "qwen3-sussurro-f16",
		Name:        "Qwen 3 Sussurro F16",
		Description: "Full precision, highest memory usage",
		Size:        "~4.07 GB",
		Filename:    "qwen3-sussurro-f16.gguf",
		DownloadURL: qwenRepositoryURL + "qwen3-sussurro-f16.gguf",
		Kind:        ModelKindLLM,
	},
}

// SupportedModels returns a copy of the compatibility catalog.
func SupportedModels() []ModelSpec {
	return append([]ModelSpec(nil), supportedModels...)
}

// FindModel returns the supported model with id.
func FindModel(id string) (ModelSpec, bool) {
	for _, model := range supportedModels {
		if model.ID == id {
			return model, true
		}
	}
	return ModelSpec{}, false
}

// FindModelByFilename returns the supported model stored under filename.
func FindModelByFilename(filename string) (ModelSpec, bool) {
	for _, model := range supportedModels {
		if model.Filename == filename {
			return model, true
		}
	}
	return ModelSpec{}, false
}
