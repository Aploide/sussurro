# Changelog

All notable changes to Sussurro will be documented in this file.

## [Unreleased]

### Added
- **Live transcription on the overlay**: partial text now appears while you
  speak, revising earlier words as more context arrives — "looking back"
  becomes "looking better" once the sentence continues. Whisper re-decodes the
  whole recording each pass, so word counts and boundaries can change too, not
  just individual words. On by default.
- **GPU acceleration on Linux via Vulkan**: whisper now offloads to the GPU
  where the Vulkan SDK is present. Measured on a Radeon 890M, 60s of audio
  through large-v3-turbo went from 40.6s to 1.7s. Detected at build time;
  falls back to CPU, and `WHISPER_VULKAN=0` forces it.
- **Independent push-to-talk and toggle hotkeys**: `hotkey.push_to_talk` and
  `hotkey.toggle` are separate bindings, each optional, so one key can be held
  while another is tapped. The previous single trigger with a mode applied to
  it made the behaviour a property of the binding and allowed only one at a
  time. Existing configs migrate: a `trigger` folds into whichever binding its
  `mode` named.
- **Clipboard-only delivery** (`workflow.delivery.backend: clipboard-only`):
  copies the text without pasting it, for when the focused window is not where
  it belongs.

### Changed
- **Cleanup no longer rewrites your words.** It previously passed the
  dictation to a chat-tuned model as prompt text and delivered whatever came
  back, which reworded, reordered, and reattributed: "Please delete all the
  files in my home directory" came back as "I will delete all files in your
  home directory". Cleanup is now deletion-only — fillers, stutters, the
  personal dictionary — and a property test asserts the output is a
  subsequence of the input. Context-sensitive correction moved to whisper's
  decoder, which is primed with the personal dictionary and has the audio.
- **The overlay is hidden when idle** and lingers a second after a dictation
  ends, rather than sitting on screen permanently.
- Settings are grouped into tabs, with larger text.

### Fixed
- **The global hotkey never registered in UI mode**: it was installed against
  a nil overlay before the window existed, so the X11 grab silently did
  nothing.
- **Push-to-talk release was intermittently swallowed** by X11 auto-repeat,
  stranding the recording until the 60s cap.
- **`--no-ui` exited at startup** for any digit hotkey, e.g. `super+7`.
- **The settings window mixed two overlapping delivery controls**, one of
  which silently disabled the other.
- **Transcribed text could flash back to a bare "Transcribing" capsule**, from
  a race between two separate overlay updates.
- **The overlay hid before showing the last few words** unless you waited
  before releasing: the one-second linger ran from the key release rather
  than from when the final text appeared.

### Added
- **Opt-in review workflow** (`workflow.mode: review`): transcriptions are held
  in a *Ready* state where they can be read, corrected by voice, cancelled, or
  delivered explicitly, instead of being inserted as soon as they are ready.
  Immediate mode is the default and is behaviourally unchanged; a config with
  no `workflow` section behaves exactly as before.
- **Live partial transcription** (`workflow.streaming.enabled`, default off):
  re-transcribes the audio captured so far on a timer. At most one pass runs at
  a time and ticks arriving during one coalesce, so slow inference lowers the
  update rate rather than growing a queue. Stopping never waits for a partial
  pass, and results from a cancelled recording are discarded by generation ID.
- **Voice-directed editing**: holding the hotkey over reviewed text records a
  spoken correction, applied by a separate LLM operation. Prompt fields are
  delimited with fenced markers so dictated quotes cannot break out of a field.
  The original text is preserved on inference failure, empty output, or output
  that fails validation, and one revision back can be undone.
- **Explicit delivery actions and optional backends**
  (`workflow.delivery.backend`): `Deliver` inserts the exact text, and
  `DeliverAndSubmit` follows it with Enter. `wtype` and `ydotool` join the
  portable clipboard-paste default, chosen by capability check. Naming a
  backend whose tool is missing is an error rather than a silent downgrade.
- **Optional Linux `evdev` input** (`workflow.input.backend: evdev`) for
  compositors that cannot deliver global press and release. Chords come from
  configuration, either modifier side counts, order does not matter, autorepeat
  cannot re-fire a gesture, and device discovery prefers stable
  `/dev/input/by-id` paths. `auto` never opens `/dev/input`.
- **Explicit Wayland trigger commands**: the socket now accepts `press`,
  `release`, `cancel`, `deliver`, and `submit` alongside `toggle`, and
  `scripts/trigger.sh` takes the command as an argument. Existing bindings that
  send `toggle` — or nothing at all — keep working unchanged.
- **Settings → Review workflow**: controls for every workflow setting, marking
  options this host cannot use and explaining why (missing tool, Linux only,
  or `input` group membership).

### Changed
- The pipeline now publishes a structured result before any delivery occurs,
  rather than injecting text itself. Immediate mode installs a compatibility
  consumer that reproduces the previous clipboard-and-paste behaviour exactly.
- Input gestures from hotkeys, the trigger socket, and future adapters route
  through a single dispatcher, so no call site branches on interaction mode.

### Fixed
- **The overlay capsule was always on screen**: the window was mapped
  unconditionally at creation and the `Show`/`Hide` methods, though
  implemented on all three platforms, were never called by anything. An idle
  Sussurro left the pill visible, animating its dots ~60 times a second for
  no reason. Visibility now follows state — hidden when idle, shown while
  recording, transcribing, or holding reviewed text — and the animation timer
  stops while hidden. The capsule stays up when no system tray is available,
  since its right-click menu is then the only route to Settings and Quit.
  Show and hide are also marshalled onto the GTK main thread, which the
  never-called implementations had not been doing.
- **Data race in the Wayland trigger server**: recording state was read and
  written from every connection goroutine without synchronisation. The server
  no longer holds that state at all.
- **Nil dereference on context failure**: the pipeline dereferenced the window
  context after the provider returned an error.
- **Failed injector treated as usable**: a `*injection.Injector` that failed to
  initialise was passed on as a non-nil interface, which would panic on the
  first paste.
- **Settings window opened as an empty grey frame on NVIDIA + Wayland** (`internal/ui/webkit_linux.go`): WebKitGTK's DMABUF renderer takes the GDK connection down with `Error 71 (Protocol error)` as soon as the first frame is composited — the page has already loaded, so the window appears but never paints. Sussurro now sets `WEBKIT_DISABLE_DMABUF_RENDERER=1` before the webview is created, unless the environment already specifies a value. Reproduced with a bare `webview_go` program and a trivial HTML page, so it affects any WebKitGTK app on that stack, not just the settings page.
- **Windows: every first run died with `yaml: line 18: did not find expected hexdecimal number`** (`internal/setup`, `internal/config`): `EnsureSetup` wrote the generated `~/.sussurro/config.yaml` model paths as double-quoted YAML scalars, where a backslash opens an escape sequence — the `\U` of `C:\Users\…` is YAML's 32-bit unicode escape and demands eight hex digits. Line 18 is `models.asr.path`, so the config was rejected before the app could start (and therefore could never repair itself). Paths are now emitted as single-quoted scalars, which have no escape sequences at all. `LoadConfig` also re-quotes an unparseable path line in place and retries once, so installs already broken by an earlier version recover on the next launch instead of needing the file deleted by hand.
- **Released Linux binaries failed to start with `libayatana-appindicator3.so.1: cannot open shared object file`** (`go.mod`, `internal/ui/tray.go`, `Makefile`, `.github/workflows/release.yml`): the tray moved from `github.com/getlantern/systray` to `fyne.io/systray`, whose Linux backend implements the DBus StatusNotifierItem protocol in pure Go. Release binaries no longer link `libappindicator3` or `libayatana-appindicator3`, so one artifact runs on distros shipping either variant — or neither. The release workflow previously tried to force the legacy backend with `apt-get install libappindicator3-dev || true`, which is a silent no-op on Ubuntu 24.04 (`libayatana-appindicator3-dev` declares both `Provides:` and `Conflicts:` on that name), so every release was linked against the Ayatana SONAME regardless. The `legacy_appindicator` build tag and its `pkg-config` probe are gone.
- **Wayland layer-shell overlay was compiled out of every build** (`Makefile`): the probe queried `pkg-config --exists gtk-layer-shell`, but the module is named `gtk-layer-shell-0` (`gtk-layer-shell` is the distro *package* name). `HAVE_GTK_LAYER_SHELL` was therefore never defined and `gtk_layer_init_for_window()` never compiled in, silently downgrading the Wayland overlay to a plain floating window.

### Added
- **Release linkage guard** (`.github/workflows/release.yml`): the Linux jobs now fail the release if `bin/sussurro` links any appindicator library or is missing `libgtk-layer-shell.so`, so neither regression can ship silently again.

## [2.3] - 2026-05-02

### Added
- **Foldable Settings sections** (`internal/ui/assets`): every Settings block (Speech Recognition, Transcription Language, Language Model, Output, Global Hotkey) now supports collapse/expand with a header caret.

### Changed
- **Settings layout sizing** (`internal/ui/assets`): model/setting/hotkey rows now use fixed control sizing and minimum row heights to prevent clipping and keep action controls aligned.
- **Scrollable Settings content behavior** (`internal/ui/assets`): the main settings pane now reliably scrolls while restart/status bars remain anchored.

### Fixed
- **Hotkey edit handler duplication** (`internal/ui/assets/app.js`): the Change button now replaces its click handler on rerender instead of stacking multiple listeners.

## [2.2] - 2026-04-07

### Added
- **Raw output toggle** (`internal/config`, `internal/pipeline`, `internal/ui`): a new `app.skip_llm_cleanup` config field (default `false`) bypasses the LLM pass completely and uses raw Whisper output as-is. The toggle is exposed in **Settings → Output** as **Raw output** and takes effect immediately without restart.

## [2.1] - 2026-03-11

### Added
- **`sussurro-transcribe` companion CLI**: batch transcription of audio files using the same local Whisper and LLM models. Accepts any format ffmpeg supports. Flags: `-i`, `-o`, `-clean`, `-lang`, `-config`, `-debug`. Build with `make build-transcribe`.
- **`scripts/package-release-all.sh`**: combined release packager that builds and bundles both `sussurro` and `sussurro-transcribe` into a single tarball with checksum.
- **`scripts/package-transcribe.sh`**: dedicated release packager for `sussurro-transcribe` alone; produces `sussurro-transcribe-{platform}-{arch}.tar.gz` with checksum and an `INSTALL.txt`. Follows the same auto-detection style as `package-release.sh`.
- **`scripts/install-transcribe.sh`**: standalone installer for `sussurro-transcribe`; updated to download from the dedicated `sussurro-transcribe-{platform}-{arch}.tar.gz` archive.
- **`docs/transcribe.md`**: full documentation for the file transcription CLI.
- **Lowercase output toggle** (`internal/config`, `internal/pipeline`, `internal/ui`): a new `app.lowercase_output` config field (default `false`) forces all transcribed text — including LLM-cleaned output and raw Whisper fallback — to lowercase before injection. The toggle is exposed as a switch in **Settings → Output** and takes effect immediately without a restart. `SaveLowercaseOutput()` writes the value to `~/.sussurro/config.yaml`, inserting the key after `log_level:` when upgrading from older configs.
- PR merge from [@RRK37](https://github.com/RRK37)

## [2.0] - 2026-03-04

### Added
- **Hotkey mode switcher** (`internal/hotkey`, `internal/config`, `internal/ui`): a new `hotkey.mode` config field accepts `"push-to-talk"` (default, hold to record / release to transcribe) or `"toggle"` (first press starts recording, second press transcribes). The active mode is reflected immediately in the running process — no restart required.
- **Settings → Global Hotkey → Mode selector**: the Settings window now shows a "Mode" dropdown beneath the hotkey display on X11 and macOS. The row is hidden on Wayland, where the hotkey is managed externally. Saving the dropdown writes the new value to `~/.sussurro/config.yaml` and hot-swaps the callbacks live.
- **`SaveHotkeyMode()` config helper** (`internal/config`): line-by-line YAML rewriter; inserts `mode:` after `trigger:` when upgrading configs that pre-date 2.0.
- **`SetHotkeyCallbackFactory` / `UpdateHotkeyMode`** (`internal/ui`): `Manager` now stores a callback factory so the correct `onDown`/`onUp` pair can be rebuilt for any mode change without restarting the hotkey system.
- **`hotkeyMode` in `initialData`** bridge response: the JS layer reads the current mode from `getInitialData()` and pre-selects the matching option in the dropdown on every `reloadSettings()` call.

## [1.9] - 2026-03-03

### Added
- **Transcription language selector** (`internal/ui`): Settings now includes a "Transcription Language" section with a dropdown to choose the language Whisper listens for. Supported options: Auto Detect, English, German, Spanish, French, Portuguese, Russian, Italian. Defaults to English.
- **`models.asr.language` config field** (`internal/config`): new `Language` field on `ASRConfig`; viper default is `"en"`. `SaveLanguage()` writes the value to `~/.sussurro/config.yaml`, inserting the key after `threads:` in the `asr:` block when upgrading from older configs that lack it.
- **Whisper language passthrough** (`internal/asr`): `NewEngine` accepts a `language` parameter and calls `ctx.SetLanguage()` on the whisper context. Failures are logged as warnings only, preserving compatibility with English-only models.

### Fixed
- **LLM empty-output fallback** (`internal/llm`): when Qwen3 produces only a `<think>…</think>` block with no content (a rare but reproducible edge case), `validateOutput` incorrectly returned `true` for the resulting empty string, causing an empty injection. An explicit `cleaned == ""` guard now falls back to the raw ASR text before reaching `validateOutput`.

## [1.8] - 2026-03-02

### Fixed
- **Word merging at Whisper segment boundaries** (`internal/asr`): Whisper splits its output into multiple internal segments; these were joined with bare string concatenation (`result += segment.Text`), causing words at boundaries to fuse (e.g. *"went to"* → *"wentto"*). Each segment is now `TrimSpace`'d, empty segments are dropped, and all parts are joined with a single space — `strings.Join(parts, " ")` — making the fix model-agnostic.

### Build
- **Conservative Apple Silicon CPU target** (`Makefile`): whisper.cpp is now built with `-mcpu=apple-m1` on Darwin/arm64 via `-DCMAKE_C_FLAGS` / `-DCMAKE_CXX_FLAGS`. This selects ARMv8.5-A, the shared baseline for all M-series chips, preventing the compiler from emitting M2/M3-specific instructions (AMX2, SME) that caused `Illegal instruction` crashes on M1 hardware. go-llama.cpp is left unmodified to avoid clobbering its own include paths.
- **Auto-detecting release packaging** (`scripts/package-release.sh`): version, platform, and architecture are now detected automatically (from `internal/version/version.go`, `uname -s`, and `uname -m`). All three can still be overridden via positional arguments. `trigger.sh` is no longer bundled in macOS releases — it is a Wayland/X11 helper only relevant on Linux. `INSTALL.txt` is now generated dynamically per platform.

## [1.7] - 2026-02-27

### Performance
- **Lock-free RMS callback dispatch** (`internal/audio`): replaced per-frame `sync.Mutex` read with `atomic.Pointer[func(float32)]` — 2.6x faster on the audio hot-path, no lock contention between the device thread and the UI notifier.
- **Zero-copy byte→float32 conversion** (`internal/audio`): replaced a manual `binary.LittleEndian`/`math.Float32frombits` decode loop with an `unsafe.Slice` reinterpret + single `copy()` (one `memmove`) — **40x faster** (673 ns → 16.7 ns per 20 ms frame).
- **`sync.Pool` for per-frame audio buffers** (`internal/audio`): the malgo device callback previously called `make([]byte, …)` on every incoming frame (hundreds per second); recycling via `sync.Pool` eliminates those allocations entirely after the first few frames — 7.3x faster, 0 allocs/op.
- **Pre-compiled regexes** (`internal/llm`): five `regexp.MustCompile()` calls that were executed on every `CleanupText` invocation are now compiled once at package init — 1.8x faster LLM post-processing, 128 → 20 allocs per cleanup.
- **Audio buffer pre-allocation and reuse** (`internal/pipeline`): the recording buffer was set to `nil` each session and grown via repeated `append()`. It is now pre-allocated to the configured max-duration capacity at startup and reset to `[:0]` between recordings, reusing the same backing array — **18.8x faster** accumulation, 0 allocs/op.

## [1.6] - 2026-02-24

### Added
- **macOS overlay blur + border**: the capsule overlay now uses `NSVisualEffectView` (material `HUDWindow`, `NSAppearanceNameVibrantDark`) as a frosted-glass backdrop clipped to the pill silhouette, making it legible over any background. A 1.5 px semi-transparent white border is drawn as an inset stroke around the pill on both macOS and Linux.
- **Model-switch restart banner**: switching the active Whisper model in Settings no longer force-quits and relaunches the process. Instead, the config is saved silently and a blue info banner — *"Restart Sussurro to load the new model into memory"* — appears at the bottom of the settings window. The running pipeline is not disrupted.
- **In-memory config sync after model switch**: after `setup.SetActiveModel` writes the new ASR path to disk, `mgr.cfg.Models.ASR.Path` is updated in memory immediately. This fixes a race where `reloadSettings()` would read stale data and snap the UI back to the previously active model for one frame.

### Fixed
- **`onDownloadProgress` fragile name match**: download progress updates now target `#prog-<modelId>` / `#pct-<modelId>` directly by element ID instead of scanning all `.model-name` spans for a matching first word — removes a latent bug if two models share a first word.
- **`onTrayExit` no-op**: the systray exit callback now calls `m.Quit()` so the `quitCh` is closed and `processUpdates` goroutine drains cleanly when the OS removes the tray icon.
- **`sussurroModelsDir()` helper**: the `~/.sussurro/models` path was duplicated in `buildInitialData` and `resolveModelDownload`; both now call a single `sussurroModelsDir()` helper.
- **Removed stale `time` import** from `settings_bridge.go` after the auto-restart goroutine was deleted.

## [1.5] - 2026-02-23

### Added
- **macOS full overlay UI**: NSPanel overlay (Cocoa + CoreVideo CVDisplayLink), settings window, system tray, and right-click context menu now all work on macOS (previously macOS was headless-only)
- **Live hotkey reconfiguration**: changing the global hotkey in Settings takes effect immediately — no restart required (`reinstallOverlayHotkey` on both Linux and macOS)
- **Linux X11 modifier support**: `alt`/`option` (X11 Mod1) and `super`/`meta`/`cmd` (X11 Mod4) hotkey modifiers now work on Linux (previously returned an error)
- **macOS modifier aliases**: `super` and `meta` are now accepted as aliases for `cmd`/`command` on macOS
- **Hotkey recording modal**: live key-combo preview as keys are held; finalises on full key release; requires at least one non-modifier key; supports up to 3 simultaneous keys
- **Metal-safe exit on macOS**: `platformExit()` calls `overlay_terminate_macos()` which stops `CVDisplayLink` and calls `_exit(0)` to bypass C++ global destructors, preventing a Metal render-encoder assertion from `ggml-metal` on quit
- **macOS settings window close fix**: `NSWindowDelegate` now hides the window instead of destroying it, preserving the WKWebView backing store across open/close cycles
- **`ParseTrigger` exported** from `internal/hotkey` package so platform-specific UI code can reuse the modifier/key mapping without duplication

### Changed
- macOS overlay panel: window level raised to `NSStatusWindowLevel`, `hidesOnDeactivate=NO`, `NSWindowCollectionBehaviorFullScreenAuxiliary` (stays visible above full-screen apps), uses `orderFrontRegardless` instead of `makeKeyAndOrderFront` to avoid stealing keyboard focus
- macOS hotkey now registered via CGEventTap in a goroutine after `[NSApp run]` is live (300 ms defer), replacing the previous no-op stub
- `Manager.Quit()` uses `platformExit()` instead of `os.Exit(0)` directly
- Log message: `"X11/macOS detected - using overlay hotkey"` → `"Using overlay hotkey"`
- Log message: `"X11 detected - using global hotkeys"` → `"Using global hotkeys (X11 / macOS)"`

## [1.3] - 2026-02-16

### Changed
- **Upgraded LLM model** from Qwen 3 base to fine-tuned **Qwen 3 Sussurro**
- Model now hosted at https://huggingface.co/cesp99/qwen3-sussurro
- Improved transcription cleanup and accuracy with domain-specific training
- Automatic detection and migration for users upgrading from versions < v1.3
- Setup now displays file sizes for model downloads (Whisper: 488 MB, LLM: 1.28 GB)

## [1.2] - 2026-02-14

### Added
- **Full Linux support** with automatic platform detection
- **Wayland support** via trigger server and UNIX socket
- **Pure-Go clipboard** implementation (no external dependencies on X11)
- Platform-specific hotkey handlers (X11 vs Wayland)
- Trigger server for Wayland with desktop notifications
- Helper script (`scripts/trigger.sh`) for Wayland keyboard shortcuts
- Comprehensive documentation:
  - Quick Start Guide
  - Dependencies guide with distro-specific commands
  - Wayland setup guide for all major DEs
  - Platform-specific README sections
- Graceful shutdown handling (Ctrl+C now works properly)
- Parallel compilation support (multi-core builds)

### Changed
- Refactored hotkey system with platform-specific implementations
- Improved log verbosity (moved technical details to DEBUG level)
- Updated clipboard to use `github.com/atotto/clipboard` on Linux
- Build system now detects CPU cores for faster compilation
- Context providers now use build tags for platform selection

### Fixed
- macOS-specific code now properly excluded on Linux builds
- Build errors on Linux due to missing build tags
- Clipboard failures on Wayland (now requires `wl-clipboard`)
- Application hanging on shutdown
- sed syntax incompatibility in patch script (macOS vs Linux)
- Metal GPU framework attempted on Linux builds

### Documentation
- Reorganized README with platform-specific quick start sections
- Added system dependency requirements for each platform
- Clear Wayland vs X11 usage instructions
- Desktop environment-specific setup guides (GNOME, KDE, Sway, Hyprland)

## [1.1] - 2025-02-13

### Added
- Initial release
- macOS support with native hotkeys
- Whisper.cpp integration for ASR
- LLM-based text cleanup with Qwen 3
- Configuration system
- First-run setup flow

## [1.0] - 2025-02-13

- Initial development version
