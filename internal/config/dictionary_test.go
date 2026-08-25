package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestNormalizeDictionary(t *testing.T) {
	got, err := NormalizeDictionary([]string{"  Sussurro  ", "whisper.cpp"})
	if err != nil {
		t.Fatalf("NormalizeDictionary() error = %v", err)
	}
	if want := []string{"Sussurro", "whisper.cpp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("NormalizeDictionary() = %#v, want %#v", got, want)
	}
}

func TestNormalizeDictionaryRejectsBlankAndDuplicateTerms(t *testing.T) {
	for _, tt := range []struct {
		name  string
		terms []string
		want  string
	}{
		{name: "blank", terms: []string{"Sussurro", "  "}, want: "term 2 is blank"},
		{name: "case-insensitive duplicate", terms: []string{"Sussurro", "sussurro"}, want: `"sussurro" duplicates "Sussurro"`},
		{name: "Unicode case-fold duplicate", terms: []string{"Σ", "ς"}, want: `"ς" duplicates "Σ"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeDictionary(tt.terms)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NormalizeDictionary() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestSaveDictionaryReplacesBlockSequenceInLoadedConfig(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	path := filepath.Join(t.TempDir(), "custom.yaml")
	body := `app:
  name: "Sussurro"
  dictionary:
    - "Old term"
    - "Other old term"
  log_level: "info"
audio:
  sample_rate: 16000
models:
  asr:
    path: "model.bin"
  llm:
    path: "llm.gguf"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	got, err := SaveDictionary(cfg, []string{"  Sussurro  ", "Kubernetes"})
	if err != nil {
		t.Fatalf("SaveDictionary() error = %v", err)
	}
	if want := []string{"Sussurro", "Kubernetes"}; !reflect.DeepEqual(got, want) {
		t.Errorf("SaveDictionary() = %#v, want %#v", got, want)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	if strings.Contains(text, "Old term") || strings.Contains(text, "Other old term") {
		t.Errorf("old block entries remain after save:\n%s", text)
	}
	if !strings.Contains(text, `dictionary: ["Sussurro","Kubernetes"]`) {
		t.Errorf("saved dictionary missing from config:\n%s", text)
	}
	if !strings.Contains(text, `log_level: "info"`) {
		t.Errorf("unrelated app setting was lost:\n%s", text)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("config permissions = %o, want 600", got)
	}

	viper.Reset()
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if !reflect.DeepEqual(reloaded.App.Dictionary, got) {
		t.Errorf("reloaded dictionary = %#v, want %#v", reloaded.App.Dictionary, got)
	}
}

func TestSaveDictionaryKeepsLoadedConfigSymlink(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	dir := t.TempDir()
	target := filepath.Join(dir, "managed-config.yaml")
	link := filepath.Join(dir, "config.yaml")
	body := `app:
  dictionary: ["Old term"]
models:
  asr:
    path: "model.bin"
  llm:
    path: "llm.gguf"
`
	if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create config symlink: %v", err)
	}
	cfg, err := LoadConfig(link)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if _, err := SaveDictionary(cfg, []string{"Sussurro"}); err != nil {
		t.Fatalf("SaveDictionary() error = %v", err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("saving replaced the config symlink")
	}
	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `dictionary: ["Sussurro"]`) {
		t.Errorf("symlink target was not updated:\n%s", written)
	}
}

func TestSaveDictionaryReplacesCommentedBlockSequence(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `app:
  dictionary: # personal terms
    - "Old term"
  log_level: "info"
models:
  asr:
    path: "model.bin"
  llm:
    path: "llm.gguf"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if _, err := SaveDictionary(cfg, []string{"Sussurro"}); err != nil {
		t.Fatalf("SaveDictionary() error = %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "Old term") {
		t.Errorf("old commented block entry remains after save:\n%s", written)
	}
	if !strings.Contains(string(written), `dictionary: ["Sussurro"] # personal terms`) {
		t.Errorf("inline dictionary comment was not preserved:\n%s", written)
	}

	viper.Reset()
	if _, err := LoadConfig(path); err != nil {
		t.Fatalf("saved config is invalid: %v\n%s", err, written)
	}
}

func TestSaveDictionaryReplacesIndentationlessSequenceWithoutEatingComments(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `app:
  dictionary:
  - "Old term"
  # Keep this explanation with log_level.
  log_level: "info"
models:
  asr:
    path: "model.bin"
  llm:
    path: "llm.gguf"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, err := SaveDictionary(cfg, []string{"Sussurro"}); err != nil {
		t.Fatalf("SaveDictionary() error = %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	if strings.Contains(text, "Old term") {
		t.Errorf("old indentationless entry remains after save:\n%s", written)
	}
	if !strings.Contains(text, "# Keep this explanation with log_level.") {
		t.Errorf("comment before the next setting was removed:\n%s", written)
	}

	viper.Reset()
	if _, err := LoadConfig(path); err != nil {
		t.Fatalf("saved config is invalid: %v\n%s", err, written)
	}
}

func TestSaveDictionaryReplacesAnchoredBlockSequence(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `app:
  dictionary: &terms
    - "Old term"
shared:
  vocabulary: *terms
models:
  asr:
    path: "model.bin"
  llm:
    path: "llm.gguf"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, err := SaveDictionary(cfg, []string{"Sussurro"}); err != nil {
		t.Fatalf("SaveDictionary() error = %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "Old term") {
		t.Errorf("old anchored block entry remains after save:\n%s", written)
	}
	if !strings.Contains(string(written), `dictionary: &terms ["Sussurro"]`) {
		t.Errorf("dictionary anchor was not preserved:\n%s", written)
	}
	viper.Reset()
	if _, err := LoadConfig(path); err != nil {
		t.Fatalf("saved config is invalid: %v\n%s", err, written)
	}
}

func TestSaveDictionaryCanInsertMissingAppSection(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `models:
  asr:
    path: "model.bin"
  llm:
    path: "llm.gguf"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if _, err := SaveDictionary(cfg, []string{"Sussurro"}); err != nil {
		t.Fatalf("SaveDictionary() error = %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "app:\n  dictionary:") {
		t.Errorf("missing app section was not inserted:\n%s", written)
	}

	viper.Reset()
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("reloading config: %v\n%s", err, written)
	}
	if !reflect.DeepEqual(reloaded.App.Dictionary, []string{"Sussurro"}) {
		t.Errorf("reloaded dictionary = %#v", reloaded.App.Dictionary)
	}
}

func TestSaveDictionaryCanInsertAndClearList(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `app:
  name: "Sussurro"
models:
  asr:
    path: "model.bin"
  llm:
    path: "llm.gguf"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, err := SaveDictionary(cfg, []string{"Sussurro"}); err != nil {
		t.Fatalf("inserting dictionary: %v", err)
	}
	if _, err := SaveDictionary(cfg, nil); err != nil {
		t.Fatalf("clearing dictionary: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "dictionary: []") {
		t.Errorf("cleared dictionary was not persisted:\n%s", written)
	}
}
