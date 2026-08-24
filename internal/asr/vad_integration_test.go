package asr

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestVADWithRealModel is opt-in because it loads a Whisper model. The checked-
// in JFK recording provides actual speech; silence and a key-release-like
// transient are appended in memory so the regression does not depend on text
// blocklists or synthetic speech.
func TestVADWithRealModel(t *testing.T) {
	modelPath := os.Getenv("SUSSURRO_ASR_TEST_MODEL")
	if modelPath == "" {
		t.Skip("set SUSSURRO_ASR_TEST_MODEL to run the real-model VAD test")
	}
	vadPath := filepath.Join("..", "..", "third_party", "whisper.cpp", "models", "for-tests-silero-v6.2.0-ggml.bin")
	engine, err := NewEngine(modelPath, 4, "en", false)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	t.Cleanup(func() { engine.Close() })
	if err := engine.EnableVAD(vadPath); err != nil {
		t.Fatalf("EnableVAD() error = %v", err)
	}

	speech := readPCM16WAV(t, filepath.Join("..", "..", "third_party", "whisper.cpp", "samples", "jfk.wav"))
	want, err := engine.Transcribe(speech)
	if err != nil {
		t.Fatalf("Transcribe(speech) error = %v", err)
	}
	if strings.TrimSpace(want) == "" {
		t.Fatal("speech recording produced no transcript")
	}

	withSilence := append(append([]float32(nil), speech...), make([]float32, 8*16000)...)
	got, err := engine.Transcribe(withSilence)
	if err != nil {
		t.Fatalf("Transcribe(speech + silence) error = %v", err)
	}
	if got != want {
		t.Errorf("trailing silence changed transcript:\n  speech: %q\n  padded: %q", want, got)
	}

	withClick := append(append([]float32(nil), speech...), make([]float32, 5*16000)...)
	withClick = append(withClick, make([]float32, 1600)...)
	for i := len(withClick) - 1600; i < len(withClick)-1280; i++ {
		withClick[i] = 0.5
	}
	got, err = engine.Transcribe(withClick)
	if err != nil {
		t.Fatalf("Transcribe(speech + silence + click) error = %v", err)
	}
	if got != want {
		t.Errorf("trailing silence and transient changed transcript:\n  speech: %q\n  padded: %q", want, got)
	}

	got, err = engine.Transcribe(make([]float32, 8*16000))
	if err != nil {
		t.Fatalf("Transcribe(silence) error = %v", err)
	}
	if strings.TrimSpace(got) != "" {
		t.Errorf("silent recording produced %q", got)
	}

	withLeadingSilence := append(make([]float32, 5*16000), speech...)
	segments, err := engine.SegmentsWithContext(withLeadingSilence, "")
	if err != nil {
		t.Fatalf("SegmentsWithContext(leading silence + speech) error = %v", err)
	}
	if len(segments) == 0 || len(segments[0].Words) == 0 {
		t.Fatal("leading-silence recording produced no timed words")
	}
	if got := segments[0].Words[0].Start; got < 4*time.Second {
		t.Errorf("VAD compressed token timestamp to %s; want original-audio time after 4s", got)
	}

	withInteriorSilence := append(append([]float32(nil), speech...), make([]float32, 5*16000)...)
	withInteriorSilence = append(withInteriorSilence, speech...)
	segments, err = engine.SegmentsWithContext(withInteriorSilence, "")
	if err != nil {
		t.Fatalf("SegmentsWithContext(interior silence) error = %v", err)
	}
	if len(segments) == 0 || len(segments[len(segments)-1].Words) == 0 {
		t.Fatal("interior-silence recording produced no final timed words")
	}
	lastWords := segments[len(segments)-1].Words
	if got := lastWords[len(lastWords)-1].Start; got < 16*time.Second {
		t.Errorf("VAD compressed post-silence token timestamp to %s; want original-audio time after 16s", got)
	}
}

func TestEnableVADRejectsMissingOrIncompleteModel(t *testing.T) {
	engine := &Engine{}
	modelPath := filepath.Join(t.TempDir(), "missing.bin")
	if err := engine.EnableVAD(modelPath); err == nil {
		t.Fatal("EnableVAD() accepted a missing model")
	}
	if err := os.WriteFile(modelPath, []byte("partial download"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := engine.EnableVAD(modelPath); err == nil {
		t.Fatal("EnableVAD() accepted an incomplete model")
	}
}

func readPCM16WAV(t *testing.T, path string) []float32 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatalf("%s is not a RIFF/WAVE file", path)
	}
	for pos := 12; pos+8 <= len(data); {
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		start, end := pos+8, pos+8+size
		if end > len(data) {
			t.Fatalf("invalid WAV chunk at byte %d", pos)
		}
		if string(data[pos:pos+4]) == "data" {
			samples := make([]float32, size/2)
			for i := range samples {
				pcm := int16(binary.LittleEndian.Uint16(data[start+i*2:]))
				samples[i] = float32(pcm) / 32768
			}
			return samples
		}
		pos = end + size%2
	}
	t.Fatalf("no data chunk in %s", path)
	return nil
}
