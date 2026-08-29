package setup

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/aploide/sussurro/internal/config"
)

// A first run on Windows fills the template with backslash paths; the result
// has to parse, or the app cannot start at all.
func TestConfiguredVADPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("models:\n  asr:\n    vad_path: /custom/silero.bin\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := configuredVADPath(configPath, "/default/silero.bin"); got != "/custom/silero.bin" {
		t.Errorf("configuredVADPath() = %q, want explicit path", got)
	}

	if err := os.WriteFile(configPath, []byte("models:\n  asr:\n    path: /custom/whisper.bin\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := configuredVADPath(configPath, "/default/silero.bin"); got != "/default/silero.bin" {
		t.Errorf("configuredVADPath() = %q, want migration default", got)
	}

	t.Setenv("SUSSURRO_MODELS_ASR_VAD_PATH", "/env/silero.bin")
	if got := configuredVADPath(configPath, "/default/silero.bin"); got != "/env/silero.bin" {
		t.Errorf("configuredVADPath() = %q, want environment override", got)
	}
}

func TestDownloadFileWritesProgressToSelectedWriter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("model data"))
	}))
	t.Cleanup(server.Close)

	destPath := filepath.Join(t.TempDir(), "model.bin")
	var output bytes.Buffer
	if err := downloadFileToWriter(server.URL, destPath, "test model", &output); err != nil {
		t.Fatalf("downloadFileToWriter() error = %v", err)
	}
	if !strings.Contains(output.String(), "Downloading test model") {
		t.Errorf("progress output = %q", output.String())
	}
	if got, err := os.ReadFile(destPath); err != nil || string(got) != "model data" {
		t.Fatalf("downloaded data = %q, err = %v", got, err)
	}
}

func TestEnsureVADModelKeepsCompleteModel(t *testing.T) {
	modelPath := filepath.Join(t.TempDir(), "silero.bin")
	if err := os.WriteFile(modelPath, make([]byte, config.MinimumVADModelSize), 0600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureVADModel(modelPath); err != nil {
		t.Fatalf("EnsureVADModel() error = %v", err)
	}
}

func TestDefaultConfigTemplateWithWindowsPaths(t *testing.T) {
	asr := `C:\Users\carlo\.sussurro\models\ggml-small.bin`
	vad := `C:\Users\carlo\.sussurro\models\ggml-silero-v6.2.0.bin`
	llm := `C:\Users\carlo\.sussurro\models\qwen3-sussurro-q4_k_m.gguf`

	rendered := strings.ReplaceAll(defaultConfigTemplate, "{{ASR_PATH}}", config.YAMLPathLiteral(asr))
	rendered = strings.ReplaceAll(rendered, "{{VAD_PATH}}", config.YAMLPathLiteral(vad))
	rendered = strings.ReplaceAll(rendered, "{{LLM_PATH}}", config.YAMLPathLiteral(llm))

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(rendered)); err != nil {
		t.Fatalf("generated config does not parse:\n%v\n\n%s", err, rendered)
	}

	if got := v.GetString("models.asr.path"); got != asr {
		t.Errorf("models.asr.path = %q, want %q", got, asr)
	}
	if got := v.GetString("models.asr.vad_path"); got != vad {
		t.Errorf("models.asr.vad_path = %q, want %q", got, vad)
	}
	if got := v.GetString("models.llm.path"); got != llm {
		t.Errorf("models.llm.path = %q, want %q", got, llm)
	}
	if got := v.GetInt("models.llm.context_size"); got != 4096 {
		t.Errorf("models.llm.context_size = %d, want 4096", got)
	}
	if got := v.GetInt("models.llm.gpu_layers"); got != 99 {
		t.Errorf("models.llm.gpu_layers = %d, want 99", got)
	}
	if got := v.GetString("hotkey.push_to_talk"); got != "ctrl+shift+space" {
		t.Errorf("hotkey.push_to_talk = %q", got)
	}
	if got := v.GetString("hotkey.toggle"); got != "" {
		t.Errorf("hotkey.toggle = %q, want empty", got)
	}
	if got := v.GetString("hotkey.edit"); got != "" {
		t.Errorf("hotkey.edit = %q, want empty", got)
	}
}
