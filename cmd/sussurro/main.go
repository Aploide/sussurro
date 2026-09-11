package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/aploide/sussurro/internal/asr"
	"github.com/aploide/sussurro/internal/audio"
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
		// Native windows and their event loops must stay on the OS thread that
		// created them. Hotkeys on X11 use GDK XGrabKey, so the CLI wrapper is
		// unnecessary in UI mode.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		run()
	}
}

func run() {
	// Parse command line flags
	configPath := flag.String("config", "", "Path to configuration file")
	noUIFlag := flag.Bool("no-ui", false, "Run in headless CLI mode (no overlay or tray)")
	whisperFlag := flag.Bool("whisper", false, "Switch Whisper ASR model")
	wspFlag := flag.Bool("wsp", false, "Switch Whisper ASR model (alias for --whisper)")
	settingsFlag := flag.Bool("settings", false, "Open the Settings window of the running instance and exit")
	flag.Parse()

	// The overlay hides when idle wherever a system tray shows the icon, and
	// not every desktop that hosts a tray puts it somewhere the user looks —
	// so a route to Settings that needs neither is required. This works on
	// every display server, without nc or socat, and is what to bind to a
	// shortcut or a launcher entry.
	if *settingsFlag {
		reply, err := trigger.Send(trigger.CommandSettings)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if reply != "SETTINGS" {
			fmt.Fprintf(os.Stderr, "Sussurro refused to open Settings: %s (is it running with --no-ui?)\n", reply)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Ensure Setup (First Run Experience)
	if err := setup.EnsureSetup(*configPath); err != nil {
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
	if err := cfg.Audio.ValidateLiveCapture(); err != nil {
		fmt.Printf("Invalid live audio configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize Logger
	log := logger.Init(cfg.App.LogLevel)
	log.Info("Starting Sussurro", "version", version.Version, "ui", !*noUIFlag)
	maxDuration, minDuration := logEffectiveConfiguration(log, cfg)

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
	if err := asrEngine.EnableVAD(cfg.Models.ASR.ResolvedVADPath(), cfg.Models.ASR.VADThreshold); err != nil {
		log.Error("Failed to initialize voice activity detection", "error", err)
		os.Exit(1)
	}

	// Initialize LLM Engine
	llmEngine, err := llm.NewEngine(cfg.Models.LLM.Path, cfg.Models.LLM.Threads, cfg.Models.LLM.ContextSize, cfg.Models.LLM.GpuLayers, cfg.App.Debug)
	if err != nil {
		log.Error("Failed to initialize LLM engine", "error", err)
		os.Exit(1)
	}
	defer llmEngine.Close()

	// Prime Whisper for recognition and retain deterministic normalization for
	// its output. Prompting is intentionally optimized for a correctly selected
	// speech input; non-speech sources such as an output monitor can echo the
	// vocabulary because Whisper treats an initial prompt as prior transcript.
	dictionary := dictionaryFanout{asrEngine, llmEngine}
	dictionary.SetDictionary(cfg.App.Dictionary)
	llmEngine.SetExtendedPrompt(cfg.Models.LLM.ExtendedPrompt)

	// Initialize Injector
	injector, err := injection.NewInjector()
	if err != nil {
		log.Error("Failed to initialize injector", "error", err)
	}

	// Initialize and Start Pipeline
	pipe := pipeline.NewPipeline(audioEngine, asrEngine, llmEngine, ctxProvider, log, cfg.Audio.SampleRate, maxDuration)
	pipe.SetMinDuration(minDuration)

	// A failed injector must stay out of the interface, or the typed nil would
	// read as a usable backend and panic on the first paste.
	var pasteBackend delivery.Injector
	if injector != nil {
		pasteBackend = injector
	}

	// The presentation sink is replaced with the UI manager below when one is
	// running; headless review sessions log their state instead.
	presentModel := func(model ui.ViewModel) {
		log.Debug("Review state", "state", model.Review, "text", model.Transcript)
	}
	presentToUI := &presentSink{present: presentModel}

	// Wire the configured interaction mode. Immediate mode keeps upstream's
	// deliver-on-completion path; review mode installs the session controller
	// as both the result consumer and the input dispatcher.
	flow := buildWorkflow(cfg, pipe, llmEngine, pasteBackend, presentToUI.Present, log)
	input := flow.dispatch

	// Partial text goes to the overlay in both modes. Review mode routes it
	// through the session controller, which owns the workflow state; immediate
	// mode has no controller, so it presents directly. Logging it instead —
	// which is what this did — makes the whole feature invisible.
	//
	// Streaming re-transcribes the audio captured so far on every interval, so
	// it is only worth running where the partial text can be shown: an
	// overlay that draws nothing but the capsule would pay for it invisibly.
	// Headless mode keeps logging partials, as it has no other output.
	switch {
	case !cfg.Workflow.Streaming.Enabled:
	case !*noUIFlag && !ui.OverlayPresents():
		log.Info("Partial transcription disabled: the overlay on this platform does not render text")
	default:
		onPartial := flow.partial
		if onPartial == nil {
			onPartial = func(generation uint64, text string) {
				log.Debug("Partial transcription", "generation", generation, "text", text)
				presentToUI.Present(ui.ViewModel{
					State:      session.StateRecording,
					Transcript: text,
					Partial:    true,
					Status:     "Listening",
					Mode:       ui.ViewExpanded,
				})
			}
		}
		streamer := pipeline.NewStreamer(
			asrEngine, pipe.SnapshotRecording, onPartial,
			cfg.Workflow.StreamingInterval(), cfg.Audio.SampleRate, log,
		)
		streamer.SetRevisionSentences(cfg.Workflow.Streaming.RevisionWindowSentences)
		pipe.SetStreamer(streamer)
		log.Info("Partial transcription enabled",
			"interval", cfg.Workflow.StreamingInterval(),
			"revision_sentences", cfg.Workflow.Streaming.RevisionWindowSentences)
	}

	// Optional evdev input, when explicitly configured. Falls back silently
	// to the default backend so a permission problem never breaks dictation.
	onCancel := func() {}
	if flow.controller != nil {
		onCancel = flow.controller.Cancel
	}
	stopEvdev := startEvdevInput(cfg, input, onCancel, log)
	if stopEvdev != nil {
		defer stopEvdev()
	}

	// The input backend decides which of the two default routes stay up: the
	// trigger socket is a control channel that also carries deliver and
	// cancel, so it is only dropped when the user asked for native hotkeys
	// alone; the native grab is dropped whenever another key source is in
	// charge, or the same key would fire twice.
	useSocket := useTriggerSocket(cfg)
	evdevActive := stopEvdev != nil

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
		// Review mode may have fallen back to immediate above; the Manager
		// only ignores pipeline notifications while a controller presents.
		uiMgr.SetReviewMode(flow.controller != nil)
		// Route review presentation to the overlay now that a UI exists.
		presentToUI.Set(uiMgr.Present)
		uiMgr.SetBufferFillSource(pipe.BufferFill)
		uiMgr.SetLowercaseOutputCallback(func(v bool) { pipe.SetLowercaseOutput(v) })
		uiMgr.SetSkipLLMCleanupCallback(func(v bool) { pipe.SetSkipLLMCleanup(v) })
		uiMgr.SetDictionaryCallback(func(terms []string) {
			pipe.RunWhenIdle(func() { dictionary.SetDictionary(terms) })
		})

		// Each native binding is independent. The dedicated edit binding is only
		// active in review mode, where reviewed text can exist.
		editHotkey := ""
		if cfg.Workflow.ReviewEnabled() {
			editHotkey = cfg.Hotkey.Edit
		}
		bindings := ui.HotkeyBindings{
			PushToTalk:    cfg.Hotkey.PushToTalk,
			Toggle:        cfg.Hotkey.Toggle,
			Edit:          editHotkey,
			OnPress:       func() { input.Dispatch(session.InputPress) },
			OnRelease:     func() { input.Dispatch(session.InputRelease) },
			OnToggle:      func() { input.Dispatch(session.InputToggle) },
			OnEditPress:   func() { input.Dispatch(session.InputEditPress) },
			OnEditRelease: func() { input.Dispatch(session.InputEditRelease) },
		}

		// Set up input handler before entering the UI main loop.
		socketUp := false
		if useSocket {
			if stop := startTriggerServer(flow, input, uiMgr, log); stop != nil {
				defer stop()
				socketUp = true
			}
		}

		if useNativeHotkeys(cfg, evdevActive, socketUp, log) {
			if !cfg.Hotkey.Configured() {
				log.Warn("No hotkey configured; set hotkey.push_to_talk, hotkey.toggle, or hotkey.edit")
			} else {
				log.Info("Using overlay hotkeys",
					"push_to_talk", cfg.Hotkey.PushToTalk,
					"toggle", cfg.Hotkey.Toggle,
					"edit", editHotkey)
			}
			uiMgr.InstallHotkey(bindings)
		}

		log.Info("Sussurro UI running")
		uiMgr.Run() // blocks until Quit()
		return
	}

	// ---- Headless / CLI mode (--no-ui) ----
	log.Info("Headless mode — no overlay")

	// No UI here, so the settings command is refused rather than raising a
	// window that was never created.
	socketUp := false
	if useSocket {
		if stop := startTriggerServer(flow, input, nil, log); stop != nil {
			defer stop()
			socketUp = true
		}
	}

	if useNativeHotkeys(cfg, evdevActive, socketUp, log) {
		log.Info("Using global hotkeys (X11 / macOS)")

		// Headless registers each configured binding separately. Every binding
		// is optional; with none set the trigger socket remains available.
		var handlers []*hotkey.Handler
		register := func(name, trigger string, onDown, onUp func()) {
			if trigger == "" {
				return
			}
			handler, err := hotkey.NewHandler(trigger, log)
			if err != nil {
				log.Error("Failed to create hotkey handler", "binding", name, "error", err)
				os.Exit(1)
			}
			if err := handler.Register(onDown, onUp); err != nil {
				log.Error("Failed to register hotkey", "binding", name, "error", err)
				os.Exit(1)
			}
			handlers = append(handlers, handler)
		}

		register("push_to_talk", cfg.Hotkey.PushToTalk,
			func() { input.Dispatch(session.InputPress) },
			func() { input.Dispatch(session.InputRelease) },
		)
		register("toggle", cfg.Hotkey.Toggle,
			func() { input.Dispatch(session.InputToggle) },
			func() {},
		)
		if cfg.Workflow.ReviewEnabled() {
			register("edit", cfg.Hotkey.Edit,
				func() { input.Dispatch(session.InputEditPress) },
				func() { input.Dispatch(session.InputEditRelease) },
			)
		}

		defer func() {
			for _, handler := range handlers {
				handler.Unregister()
			}
		}()

		if !cfg.Hotkey.Configured() {
			log.Warn("No hotkey configured; set hotkey.push_to_talk, hotkey.toggle, or hotkey.edit")
		}
	}

	log.Info("Sussurro running. Press Ctrl+C to exit.")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan
	log.Info("Received signal, shutting down...", "signal", sig)
}

// isWayland is hotkey.IsWayland, indirected so the routing below is testable
// without a display.
var isWayland = hotkey.IsWayland

// useTriggerSocket reports whether the trigger socket should listen. Only the
// native backend turns it off, and only where a native grab can actually work:
// the socket is the sole route for deliver and cancel, and on Wayland the only
// input route at all, so auto, trigger, and evdev keep it, as does native on
// Wayland.
func useTriggerSocket(cfg *config.Config) bool {
	return cfg.Workflow.Input.Backend != config.InputNative || isWayland()
}

// useNativeHotkeys reports whether the in-process global hotkey grab should be
// installed, logging why not. Wayland cannot grab keys; the trigger backend
// hands key handling to the compositor; a running evdev reader sees the same
// keys from the device, so grabbing them as well would fire every gesture
// twice. A backend that failed to come up — evdev that did not start, or a
// trigger socket that could not listen — has already fallen back, and the
// grab is that fallback.
func useNativeHotkeys(cfg *config.Config, evdevActive, socketUp bool, log *slog.Logger) bool {
	switch {
	case cfg.Workflow.Input.Backend == config.InputTrigger && socketUp:
		log.Info("Native hotkeys disabled: input backend is trigger (see docs/wayland.md)")
		return false
	case cfg.Workflow.Input.Backend == config.InputTrigger:
		log.Error("Trigger socket failed to start; falling back to native hotkeys")
	case evdevActive:
		log.Info("Native hotkeys disabled: input backend is evdev")
		return false
	}
	if isWayland() {
		if socketUp {
			log.Warn("Wayland: configure keyboard shortcut (see docs/wayland.md)")
		} else {
			log.Error("Wayland: no input route is active — native hotkeys cannot grab keys and the trigger socket is not listening")
		}
		return false
	}
	return true
}
