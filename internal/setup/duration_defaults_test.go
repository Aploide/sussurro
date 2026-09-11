package setup

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestRecordingDurationDefaultsMatch(t *testing.T) {
	files := map[string]string{
		"first-run template": strings.NewReplacer(
			"{{ASR_PATH}}", `"/tmp/asr.bin"`,
			"{{VAD_PATH}}", `"/tmp/vad.bin"`,
			"{{LLM_PATH}}", `"/tmp/llm.gguf"`,
		).Replace(defaultConfigTemplate),
	}
	fallback, err := os.ReadFile("../../configs/default.yaml")
	if err != nil {
		t.Fatal(err)
	}
	files["fallback config"] = string(fallback)

	for name, contents := range files {
		t.Run(name, func(t *testing.T) {
			v := viper.New()
			v.SetConfigType("yaml")
			if err := v.ReadConfig(strings.NewReader(contents)); err != nil {
				t.Fatal(err)
			}
			if got := v.GetString("audio.max_duration"); got != "2m" {
				t.Errorf("audio.max_duration = %q, want 2m", got)
			}
		})
	}
}
