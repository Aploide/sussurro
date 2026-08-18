package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/aploide/sussurro/internal/asr"
	"github.com/aploide/sussurro/internal/audio"
	"github.com/aploide/sussurro/internal/clipboard"
	"github.com/aploide/sussurro/internal/config"
	"github.com/aploide/sussurro/internal/context"
	"github.com/aploide/sussurro/internal/delivery"
	"github.com/aploide/sussurro/internal/hotkey"
	"github.com/aploide/sussurro/internal/injection"
	"github.com/aploide/sussurro/internal/llm"
	"github.com/aploide/sussurro/internal/logger"
	"github.com/aploide/sussurro/internal/pipeline"
	"github.com/aploide/sussurro/internal/session"
	"github.com/aploide/sussurro/internal/setup"
	"github.com/aploide/sussurro/internal/trigger"
	"github.com/aploide/sussurro/internal/ui"
	"github.com/aploide/sussurro/internal/version"

	"golang.design/x/hotkey/mainthread"
)

func main() {
	// Peek at --no-ui before deciding whether we need mainthread.Init.
	// mainthread.Init is needed for golang.design/x/hotkey on X11/macOS in CLI mode.
	noUI := false
	for _, arg := range os.Args[1:] {
		if arg == "--no-ui" || arg == "-no-ui" {
			noUI = true
			break
		}
	}

	if noUI {
		// CLI / headless mode: keep the existing mainthread.Init wrapper so
		// that golang.design/x/hotkey works correctly on X11 and macOS.
		mainthread.Init(run)
	} else {
		// UI mode: gtk_main() / [NSApp run] owns the main thread.
		// Hotkeys on X11 are handled via GDK XGrabKey (no mainthread.Init needed).
		run()
	}
}

func run() {
	// Parse command line flags
	configPath := flag.String("config", "", "Path to configuration file")
	noUIFlag := flag.Bool("no-ui", false, "Run in headless CLI mode (no overlay or tray)")
	whisperFlag := flag.Bool("whisper", false, "Switch Whisper ASR model")
	wspFlag := flag.Bool("wsp", false, "Switch Whisper ASR model (alias for --whisper)")
	flag.Parse()

	// Ensure Setup (First Run Experience)
	if err := setup.EnsureSetup(); err != nil {
		fmt.Printf("Setup failed: %v\n", err)
		os.Exit(1)
	}

	// Handle Whisper model switch: show interactive menu and exit
	if *whisperFlag || *wspFlag {
		if err := setup.SwitchWhisperModel(); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Load Configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize Logger
	log := logger.Init(cfg.App.LogLevel)
	log.Info("Starting Sussurro", "version", version.Version, "ui", !*noUIFlag)

	// Check if models exist
	if _, err := os.Stat(cfg.Models.ASR.Path); os.IsNotExist(err) {
		log.Error("ASR model missing", "path", cfg.Models.ASR.Path)
		fmt.Printf("Error: ASR model not found at %s. Please ensure models are downloaded.\n", cfg.Models.ASR.Path)
		os.Exit(1)
	}
	if _, err := os.Stat(cfg.Models.LLM.Path); os.IsNotExist(err) {
		log.Error("LLM model missing", "path", cfg.Models.LLM.Path)
		fmt.Printf("Error: LLM model not found at %s. Please ensure models are downloaded.\n", cfg.Models.LLM.Path)
		os.Exit(1)
	}

	// Initialize Context Provider
	ctxProvider := context.NewProvider()
	defer ctxProvider.Close()

	// Initialize Audio Capture
	audioEngine, err := audio.NewCaptureEngine(cfg.Audio.SampleRate, cfg.Audio.Channels)
	if err != nil {
		log.Error("Failed to initialize audio engine", "error", err)
		os.Exit(1)
	}
	defer audioEngine.Close()

	// Initialize ASR Engine
	asrEngine, err := asr.NewEngine(cfg.Models.ASR.Path, cfg.Models.ASR.Threads, cfg.Models.ASR.Language, cfg.App.Debug)
	if err != nil {
		log.Error("Failed to initialize ASR engine", "error", err)
		os.Exit(1)
	}
	defer asrEngine.Close()

	// Initialize LLM Engine
	llmEngine, err := llm.NewEngine(cfg.Models.LLM.Path, cfg.Models.LLM.Threads, cfg.Models.LLM.ContextSize, cfg.Models.LLM.GpuLayers, cfg.App.Debug)
	if err != nil {
		log.Error("Failed to initialize LLM engine", "error", err)
		os.Exit(1)
	}
	defer llmEngine.Close()
	llmEngine.SetDictionary(cfg.App.Dictionary)
	llmEngine.SetExtendedPrompt(cfg.Models.LLM.ExtendedPrompt)

	// Initialize Injector
	injector, err := injection.NewInjector()
	if err != nil {
		log.Error("Failed to initialize injector", "error", err)
	}

	// Initialize and Start Pipeline
	pipe := pipeline.NewPipeline(audioEngine, asrEngine, llmEngine, ctxProvider, log, cfg.Audio.SampleRate, cfg.Audio.MaxDuration)

	// Immediate mode: deliver every recognition result as soon as it is
	// published. Review mode will install a session controller here instead.
	// A failed injector must stay out of the interface, or the typed nil would
	// read as a usable backend and panic on the first paste.
	var pasteBackend delivery.Injector
	if injector != nil {
		pasteBackend = injector
	}
	immediate := delivery.NewImmediate(clipboard.Write, pasteBackend, os.Stdout, log)
	pipe.SetResultConsumer(pipeline.ResultConsumerFunc(func(result pipeline.Result) {
		if result.Empty() {
			return
		}
		if err := immediate.Deliver(result.Text); err != nil {
			log.Error("Immediate delivery failed", "error", err)
		}
	}))

	// Streaming stays off unless the config opts in. Partial text is logged
	// for now; overlay presentation arrives with the review UI work.
	if cfg.Workflow.Streaming.Enabled {
		pipe.SetStreamer(pipeline.NewStreamer(
			asrEngine,
			pipe.SnapshotRecording,
			func(generation uint64, text string) {
				log.Debug("Partial transcription", "generation", generation, "text", text)
			},
			cfg.Workflow.StreamingInterval(),
			log,
		))
		log.Info("Partial transcription enabled", "interval", cfg.Workflow.StreamingInterval())
	}

	pipe.SetLowercaseOutput(cfg.App.LowercaseOutput)
	pipe.SetSkipLLMCleanup(cfg.App.SkipLLMCleanup)

	pipe.SetOnCompletion(func() {
		log.Debug("Pipeline processing completed")
	})

	if err := pipe.Start(); err != nil {
		log.Error("Failed to start pipeline", "error", err)
		os.Exit(1)
	}
	defer pipe.Stop()

	// ---- UI mode ----
	if !*noUIFlag {
		uiMgr, err := ui.NewManager(cfg)
		if err != nil {
			log.Error("Failed to initialize UI manager", "error", err)
			os.Exit(1)
		}

		pipe.SetUINotifier(uiMgr)
		uiMgr.SetLowercaseOutputCallback(func(v bool) { pipe.SetLowercaseOutput(v) })
		uiMgr.SetSkipLLMCleanupCallback(func(v bool) { pipe.SetSkipLLMCleanup(v) })

		// buildHotkeyCallbacks returns the right onDown/onUp pair for the given mode.
		buildHotkeyCallbacks := func(mode string) (onDown func(), onUp func()) {
			if mode == "toggle" {
				return func() {
					if session.DispatchImmediateInput(pipe, session.InputToggle) {
						log.Info("Transcribing...")
					} else {
						log.Info("Listening...")
					}
				}, func() {}
			}
			// Default: push-to-talk
			return func() { log.Info("Listening..."); session.DispatchImmediateInput(pipe, session.InputPress) },
				func() { log.Info("Transcribing..."); session.DispatchImmediateInput(pipe, session.InputRelease) }
		}
		uiMgr.SetHotkeyCallbackFactory(buildHotkeyCallbacks)

		// Set up input handler before entering the UI main loop.
		if hotkey.IsWayland() {
			log.Debug("Wayland detected - using trigger server")
			triggerServer, err := trigger.NewServer(log)
			if err != nil {
				log.Error("Failed to initialize trigger server", "error", err)
				os.Exit(1)
			}
			defer triggerServer.Stop()
			if err := triggerServer.Start(
				func() {
					log.Debug("Trigger: Starting recording")
					session.DispatchImmediateInput(pipe, session.InputPress)
				},
				func() {
					log.Debug("Trigger: Stopping recording")
					session.DispatchImmediateInput(pipe, session.InputRelease)
				},
			); err != nil {
				log.Error("Failed to start trigger server", "error", err)
				os.Exit(1)
			}
			log.Warn("Wayland: configure keyboard shortcut (see docs/wayland.md)")
		} else {
			log.Info("Using overlay hotkey")
			onDown, onUp := buildHotkeyCallbacks(cfg.Hotkey.Mode)
			uiMgr.InstallHotkey(cfg.Hotkey.Trigger, onDown, onUp)
		}

		log.Info("Sussurro UI running")
		uiMgr.Run() // blocks until Quit()
		return
	}

	// ---- Headless / CLI mode (--no-ui) ----
	log.Info("Headless mode — no overlay")

	if hotkey.IsWayland() {
		log.Debug("Wayland detected - using trigger server")

		triggerServer, err := trigger.NewServer(log)
		if err != nil {
			log.Error("Failed to initialize trigger server", "error", err)
			os.Exit(1)
		}
		defer triggerServer.Stop()

		if err := triggerServer.Start(
			func() {
				log.Debug("Trigger: Starting recording")
				session.DispatchImmediateInput(pipe, session.InputPress)
			},
			func() {
				log.Debug("Trigger: Stopping recording")
				session.DispatchImmediateInput(pipe, session.InputRelease)
			},
		); err != nil {
			log.Error("Failed to start trigger server", "error", err)
			os.Exit(1)
		}
		log.Warn("Wayland detected: Configure keyboard shortcut (see docs/wayland.md)")
	} else {
		log.Info("Using global hotkeys (X11 / macOS)")

		var onDown, onUp func()
		if cfg.Hotkey.Mode == "toggle" {
			onDown = func() {
				if session.DispatchImmediateInput(pipe, session.InputToggle) {
					log.Info("Transcribing...")
				} else {
					log.Info("Listening...")
				}
			}
			onUp = func() {}
		} else {
			onDown = func() { log.Info("Listening..."); session.DispatchImmediateInput(pipe, session.InputPress) }
			onUp = func() { log.Info("Transcribing..."); session.DispatchImmediateInput(pipe, session.InputRelease) }
		}

		hkHandler, err := hotkey.NewHandler(cfg.Hotkey.Trigger, log)
		if err != nil {
			log.Error("Failed to initialize hotkey handler", "error", err)
			os.Exit(1)
		}
		defer hkHandler.Unregister()

		if err := hkHandler.Register(onDown, onUp); err != nil {
			log.Error("Failed to register hotkey", "error", err)
			os.Exit(1)
		}
	}

	log.Info("Sussurro running. Press Ctrl+C to exit.")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan
	log.Info("Received signal, shutting down...", "signal", sig)
}
