# Known Issues

## Platform Support

### Windows caveats
Windows is supported (overlay via a Win32 layered window + GDI+, settings via
WebView2, tray via `fyne.io/systray`, hotkeys via `RegisterHotKey`,
Vulkan-accelerated Whisper). Remaining caveats:

- **Hotkey conflicts**: `RegisterHotKey` fails if another application already
  owns the combination. Sussurro logs an error at startup — pick a different
  trigger in Settings if the hotkey does nothing.
- **Headless (`--no-ui`) toggle mode**: the `--no-ui` code path uses
  `golang.design/x/hotkey`, whose Windows backend can replay keyboard
  autorepeat as phantom presses. Push-to-talk is unaffected in practice
  (phantom recordings are dropped by the short-recording guard), but toggle
  mode may misbehave headless. The normal UI mode uses its own corrected
  hotkey loop and has neither problem.
- **WebView2 runtime** is required for the settings window (preinstalled on
  Windows 11; Windows 10 users may need the Evergreen runtime from Microsoft).
- **LLM cleanup runs on CPU** on Windows (Vulkan is wired up for Whisper
  only; the go-llama.cpp binding has no Vulkan build and a second
  Vulkan-enabled ggml copy would collide at link time).
- **`sussurro-transcribe` needs ffmpeg** on PATH (`winget install Gyan.FFmpeg`).

### Linux release binaries are CPU-only
The published Linux artifacts are built on a runner without the Vulkan SDK, so
neither whisper nor the LLM helper uses the GPU and `models.llm.gpu_layers`
has no effect. Vulkan is a build-time option of a source build: `make build`
enables it when the SDK (`vulkan` pkg-config module and `glslc`) is present,
and the LLM helper additionally needs the `spirv-headers` package — without it
the helper is built for CPU and `make` prints a note saying so.

## Review Workflow

The review workflow is opt-in and off by default; immediate dictation is
unaffected by everything here.

- **Review mode is Linux-only for now**: the Linux overlay renders partial and
  reviewed text, but the Windows and macOS overlays still draw only the
  capsule, and `cancel` reaches the controller only through the Unix trigger
  socket. Settings lists the mode as unavailable there, and the streamer is
  left off so no partials are computed that cannot be shown.
- **evdev requires the `input` group**: `workflow.input.backend: evdev` reads
  Linux input devices directly and fails with an explanatory error without
  membership. `auto` never opens `/dev/input`, so this affects only hosts that
  explicitly select `evdev`. Device discovery and the permission diagnostic are
  verified; live key reading is not, as it needs that membership.
- **`wtype` and `ydotool` delivery are unverified**: neither tool is installed
  on the development host. The command arguments are covered by tests, but the
  backends have not been run against a real compositor. `auto` falls back to
  clipboard paste, which is the tested path.
- **Voice editing quality depends on the model**: the bundled
  `qwen3-sussurro` model is fine-tuned for cleanup, not instruction-following.
  Edits fall back to the original text when the model returns nothing usable,
  so a poor edit is safe but may simply do nothing. A general instruct model
  configured via `models.llm.path` handles instructions better.
- **Live transcription costs CPU**: enabling `workflow.streaming.enabled`
  re-runs Whisper on the audio captured so far. On CPU-only hosts the practical
  update rate may be well below the configured interval. This is by design —
  partial passes are skipped rather than queued — but it means streaming is of
  limited use on slow machines.
