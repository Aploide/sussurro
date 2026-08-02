package setup

import (
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/aploide/sussurro/internal/config"
)

// A first run on Windows fills the template with backslash paths; the result
// has to parse, or the app cannot start at all.
func TestDefaultConfigTemplateWithWindowsPaths(t *testing.T) {
	asr := `C:\Users\carlo\.sussurro\models\ggml-small.bin`
	llm := `C:\Users\carlo\.sussurro\models\qwen3-sussurro-q4_k_m.gguf`

	rendered := strings.ReplaceAll(defaultConfigTemplate, "{{ASR_PATH}}", config.YAMLPathLiteral(asr))
	rendered = strings.ReplaceAll(rendered, "{{LLM_PATH}}", config.YAMLPathLiteral(llm))

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(rendered)); err != nil {
		t.Fatalf("generated config does not parse:\n%v\n\n%s", err, rendered)
	}

	if got := v.GetString("models.asr.path"); got != asr {
		t.Errorf("models.asr.path = %q, want %q", got, asr)
	}
	if got := v.GetString("models.llm.path"); got != llm {
		t.Errorf("models.llm.path = %q, want %q", got, llm)
	}
	if got := v.GetString("hotkey.trigger"); got != "ctrl+shift+space" {
		t.Errorf("hotkey.trigger = %q", got)
	}
}
