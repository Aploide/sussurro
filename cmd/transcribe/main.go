package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"

	"github.com/aploide/sussurro/internal/asr"
	"github.com/aploide/sussurro/internal/config"
	"github.com/aploide/sussurro/internal/llm"
	"github.com/aploide/sussurro/internal/logger"
	"github.com/aploide/sussurro/internal/setup"
	"github.com/aploide/sussurro/internal/version"
)

func main() {
	inputFile := flag.String("i", "", "Input audio file (any format ffmpeg supports)")
	outputFile := flag.String("o", "", "Output text file (default: stdout)")
	configPath := flag.String("config", "", "Path to configuration file")
	clean := flag.Bool("clean", false, "Run LLM cleanup on transcription")
	language := flag.String("lang", "", "Override ASR language (e.g. en, auto)")
	debug := flag.Bool("debug", false, "Enable debug output")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("sussurro-transcribe %s\n", version.Version)
		return
	}

	if *inputFile == "" {
		fmt.Fprintln(os.Stderr, "Usage: sussurro-transcribe -i <audio-file> [-o output.txt] [-clean] [-lang en] [-config path]")
		os.Exit(1)
	}

	if _, err := os.Stat(*inputFile); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: input file not found: %s\n", *inputFile)
		os.Exit(1)
	}

	// Load config
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to load config: %v\n", err)
		os.Exit(1)
	}

	if *debug {
		logger.Init("debug")
	} else {
		logger.Init(cfg.App.LogLevel)
	}

	vadPath := cfg.Models.ASR.ResolvedVADPath()
	if err := setup.EnsureVADModel(vadPath, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to provision voice activity model: %v\n", err)
		os.Exit(1)
	}

	// Convert audio to 16kHz mono f32le PCM via ffmpeg
	samples, err := audioToSamples(*inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to decode audio: %v\n", err)
		os.Exit(1)
	}

	if len(samples) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no audio samples decoded")
		os.Exit(1)
	}

	// Determine language
	lang := cfg.Models.ASR.Language
	if *language != "" {
		lang = *language
	}

	// Initialize ASR engine
	asrEngine, err := asr.NewEngine(cfg.Models.ASR.Path, cfg.Models.ASR.Threads, lang, *debug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to initialize ASR engine: %v\n", err)
		os.Exit(1)
	}
	defer asrEngine.Close()
	if err := asrEngine.EnableVAD(vadPath, cfg.Models.ASR.VADThreshold); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to initialize voice activity detection: %v\n", err)
		os.Exit(1)
	}

	// Transcribe
	text, err := transcribeWithDictionary(asrEngine, cfg.App.Dictionary, samples)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: transcription failed: %v\n", err)
		os.Exit(1)
	}

	// Optional LLM cleanup
	if *clean {
		llmEngine, err := llm.NewEngine(cfg.Models.LLM.Path, cfg.Models.LLM.Threads, cfg.Models.LLM.ContextSize, cfg.Models.LLM.GpuLayers, *debug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to initialize LLM engine: %v\n", err)
			os.Exit(1)
		}
		defer llmEngine.Close()
		llmEngine.SetDictionary(cfg.App.Dictionary)
		llmEngine.SetExtendedPrompt(cfg.Models.LLM.ExtendedPrompt)

		cleaned, err := llmEngine.CleanupText(text)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: LLM cleanup failed, using raw transcription: %v\n", err)
		} else {
			text = cleaned
		}
	}

	// Output
	if *outputFile != "" {
		if err := os.WriteFile(*outputFile, []byte(text+"\n"), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to write output file: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Transcription written to %s\n", *outputFile)
	} else {
		fmt.Println(text)
	}
}

// audioToSamples converts any audio file to 16kHz mono float32 samples using ffmpeg.
type dictionaryTranscriber interface {
	SetDictionary([]string)
	Transcribe([]float32) (string, error)
}

func transcribeWithDictionary(engine dictionaryTranscriber, dictionary []string, samples []float32) (string, error) {
	engine.SetDictionary(dictionary)
	return engine.Transcribe(samples)
}

func audioToSamples(path string) ([]float32, error) {
	cmd := exec.Command("ffmpeg",
		"-i", path,
		"-f", "f32le",
		"-ar", "16000",
		"-ac", "1",
		"-loglevel", "error",
		"pipe:1",
	)
	cmd.Stderr = os.Stderr

	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w (is ffmpeg installed?)", err)
	}

	numSamples := len(raw) / 4
	samples := make([]float32, numSamples)
	for i := range samples {
		bits := binary.LittleEndian.Uint32(raw[i*4:])
		samples[i] = math.Float32frombits(bits)
	}

	return samples, nil
}
