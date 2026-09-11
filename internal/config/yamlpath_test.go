package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// The path a first run produces on Windows. Written as a double-quoted YAML
// scalar it is unparseable, which is what broke every Windows install.
const windowsModelPath = `C:\Users\carlo\.sussurro\models\ggml-small.bin`

func TestYAMLPathLiteralParsesBack(t *testing.T) {
	for _, p := range []string{
		windowsModelPath,
		`/home/carlo/.sussurro/models/ggml-small.bin`,
		`C:\Users\O'Brien\.sussurro\models\ggml-small.bin`,
	} {
		v := viper.New()
		v.SetConfigType("yaml")
		if err := v.ReadConfig(strings.NewReader("models:\n  asr:\n    path: " + YAMLPathLiteral(p) + "\n")); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if got := v.GetString("models.asr.path"); got != p {
			t.Errorf("round-trip of %q gave %q", p, got)
		}
	}
}

func TestRepairConfigPaths(t *testing.T) {
	broken := "models:\n" +
		"  asr:\n" +
		`    path: "` + windowsModelPath + `"` + "\n" +
		"    threads: 4\n" +
		"  llm:\n" +
		`    path: "C:\Users\carlo\.sussurro\models\qwen3.gguf" # the LLM` + "\n"

	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte(broken), 0644); err != nil {
		t.Fatal(err)
	}

	repaired, err := repairConfigPaths(file)
	if err != nil || !repaired {
		t.Fatalf("repair: repaired=%v err=%v", repaired, err)
	}

	v := viper.New()
	v.SetConfigFile(file)
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("repaired file still does not parse: %v", err)
	}
	if got := v.GetString("models.asr.path"); got != windowsModelPath {
		t.Errorf("asr path = %q, want %q", got, windowsModelPath)
	}
	if got := v.GetString("models.llm.path"); got != `C:\Users\carlo\.sussurro\models\qwen3.gguf` {
		t.Errorf("llm path = %q", got)
	}

	// The comment survives, and a second pass has nothing left to do.
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "# the LLM") {
		t.Errorf("trailing comment was dropped:\n%s", content)
	}
	if again, err := repairConfigPaths(file); again || err != nil {
		t.Errorf("second pass: repaired=%v err=%v, want false/nil", again, err)
	}
}

func TestRepairConfigPathsLeavesValidFilesAlone(t *testing.T) {
	// A Unix path, and one someone escaped by hand — both already parse.
	valid := "models:\n" +
		"  asr:\n" +
		`    path: "/home/carlo/.sussurro/models/ggml-small.bin"` + "\r\n" +
		"  llm:\n" +
		`    path: "C:\\Users\\carlo\\.sussurro\\models\\qwen3.gguf"` + "\r\n"

	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte(valid), 0644); err != nil {
		t.Fatal(err)
	}

	repaired, err := repairConfigPaths(file)
	if err != nil {
		t.Fatal(err)
	}
	if repaired {
		t.Error("rewrote a config that already parsed")
	}

	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != valid {
		t.Errorf("file changed:\n%q", content)
	}
}

// An install broken by an older version has to recover on the next launch:
// the config is not parseable, so nothing else can fix it first.
func TestLoadConfigRepairsBrokenWindowsConfig(t *testing.T) {
	broken := "app:\n" +
		`  log_level: "info"` + "\n" +
		"models:\n" +
		"  asr:\n" +
		`    path: "` + windowsModelPath + `"` + "\n" +
		"hotkey:\n" +
		`  trigger: "ctrl+shift+space"` + "\n"

	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte(broken), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(file)
	if err != nil {
		t.Fatalf("LoadConfig did not recover: %v", err)
	}
	if cfg.Models.ASR.Path != windowsModelPath {
		t.Errorf("asr path = %q, want %q", cfg.Models.ASR.Path, windowsModelPath)
	}
	if cfg.Hotkey.Trigger != "ctrl+shift+space" {
		t.Errorf("hotkey trigger = %q", cfg.Hotkey.Trigger)
	}
}

func TestReplacePathInYAML(t *testing.T) {
	oldPath := `C:\Users\O'Brien\.sussurro\models\ggml-small.bin`
	newPath := `C:\Users\O'Brien\.sussurro\models\ggml-large-v3-turbo.bin`

	// Single-quoted (apostrophe doubled) — what current versions write.
	quoted := "    path: " + YAMLPathLiteral(oldPath) + "\n"
	if got := ReplacePathInYAML(quoted, oldPath, newPath); got != "    path: "+YAMLPathLiteral(newPath)+"\n" {
		t.Errorf("single-quoted form not swapped: %q", got)
	}

	// Verbatim — what older versions and every Unix install wrote.
	verbatim := `    path: "` + oldPath + `"` + "\n"
	if got := ReplacePathInYAML(verbatim, oldPath, newPath); !strings.Contains(got, newPath) {
		t.Errorf("verbatim form not swapped: %q", got)
	}
}
