# Sussurro

[![Version 2.3](https://img.shields.io/badge/Version-2.3-black?style=flat)](https://github.com/cesp99/sussurro/releases)
[![GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-black?style=flat)](LICENSE)
[![Go 1.24+](https://img.shields.io/badge/Go-1.24+-black?style=flat&logo=go&logoColor=white)](https://golang.org)
[![Linux](https://img.shields.io/badge/Linux-black?style=flat&logo=linux&logoColor=white)](https://github.com/cesp99/sussurro)
[![macOS](https://img.shields.io/badge/macOS-black?style=flat&logo=apple&logoColor=white)](https://github.com/cesp99/sussurro)
[![DeepWiki](https://img.shields.io/badge/DeepWiki-black?style=flat&logo=readthedocs&logoColor=white)](https://deepwiki.com/cesp99/sussurro)

Sussurro is a fully local, open-source voice-to-text system with a built-in native overlay UI. It transforms speech into clean, formatted, context-aware text and injects it into any application — entirely on your machine, using **Whisper.cpp** for ASR and a fine-tuned **Qwen 3** LLM for cleanup.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/cesp99/sussurro/master/scripts/install.sh | bash
```

Works on Linux and macOS. The installer:

- **macOS** — drops `Sussurro.app` into `/Applications` (or `~/Applications` if you don't have admin rights) so it shows up in Launchpad and Spotlight, and symlinks `sussurro` onto your `PATH` for CLI use.
- **Linux** — installs the `sussurro` binary to `/usr/local/bin`, registers a `.desktop` entry, and installs hicolor icons so it appears in your application menu.
- **Both** — downloads the AI models (~1.77 GB or ~2.90 GB, your choice) so the first launch from Launchpad / the app menu works without a terminal.

You can launch Sussurro from your normal app launcher *or* from a terminal — both work.

> **Wayland users:** after install, bind the hotkey in your desktop environment — see [Wayland Setup](docs/wayland.md).
> **macOS users:** grant Microphone and Accessibility access when prompted (System Settings → Privacy & Security).

---

## Features

- **Built-in Native Overlay**: A minimal, aesthetically clean floating capsule shows recording/transcribing state — always on top, no taskbar entry *(Linux & macOS)*
- **Settings UI**: Dark-themed settings window — right-click the overlay capsule to open it or quit *(Linux & macOS)*
- **Smart Cleanup**: Removes filler words, handles self-corrections, prevents hallucinations
- **Local Processing**: No data leaves your machine
- **System-Wide**: Works in any application where you can type
- **Flexible ASR**: Whisper Small (fast) or Large v3 Turbo (accurate), switchable from the UI
- **Live Hotkey Config**: Change the global hotkey from Settings — takes effect instantly, no restart
- **Hotkey Mode**: Switch between *Push to Talk* (hold to record, release to transcribe) and *Toggle* (press once to start, press again to transcribe) directly from Settings *(X11 & macOS only)*
- **Transcription Language**: Choose the language Whisper listens for (or use Auto Detect) directly from Settings
- **Headless Mode**: `--no-ui` flag for CLI/scripting use on any platform

---

## Quick Reference

| Platform | Default hotkey | Default mode | Access Settings |
|----------|---------------|-------------|----------------|
| Linux X11 | `Ctrl+Shift+Space` | Push to Talk | Right-click the overlay capsule |
| Linux Wayland | configured in DE | n/a (external shortcut) | Right-click the overlay capsule |
| macOS | `Cmd+Shift+Space` | Push to Talk | Right-click the overlay capsule |

The hotkey mode can be changed at any time from **Settings → Global Hotkey → Mode**.

---

## Documentation

- [**Quick Start**](docs/quickstart.md): Get up and running in under 5 minutes
- [**Dependencies**](docs/dependencies.md): System requirements and package installation
- [**Wayland Setup**](docs/wayland.md): One-time configuration for Wayland users
- [**Configuration**](docs/configuration.md): Detailed guide on `config.yaml` and environment variables
- [**Architecture**](docs/architecture.md): How the audio pipeline, ASR, and LLM engines work
- [**Compilation**](docs/compilation.md): Building from source (CLI and UI builds)
- [**File Transcription**](docs/transcribe.md): `sussurro-transcribe` companion CLI — batch transcription of audio files

---

## Building from Source

```bash
git clone https://github.com/cesp99/sussurro.git
cd sussurro
make build        # → bin/sussurro  (overlay + settings)
make app          # → bin/Sussurro.app  (macOS application bundle)
make package      # → release/sussurro-<os>-<arch>.tar.gz  (release tarball)
```

`make app` is macOS-only and produces a double-clickable `Sussurro.app`. On Linux, use `make package` to produce a tarball with the `.desktop` entry and hicolor icon set bundled.

Requires GTK3 and WebKit2GTK dev headers on Linux. See [Compilation](docs/compilation.md) for full instructions and per-distro dependency lists.

---

## UI: The Overlay Capsule

When Sussurro runs (Linux or macOS), a sleek pill-shaped capsule appears at the bottom-center of your screen:

| State | Appearance |
|-------|-----------|
| **Idle** | 7 softly pulsing white dots |
| **Recording** | 7 waveform bars animated by your voice |
| **Transcribing** | "transcribing" text with a shimmer effect |

**Accessing Settings:** right-click the capsule → **Open Settings**. The same context menu also has a **Quit** entry.

The settings window lets you switch Whisper models, download models with a live progress bar, select the transcription language, change the global hotkey, and choose the hotkey mode. All changes take effect immediately — no restart required.

---

## Headless / CLI Mode

```bash
./sussurro --no-ui
```

Terminal output only — no overlay, no settings window. Useful for scripting or low-resource environments.

---

## Switching Whisper Models

Via the Settings UI (recommended) — or from the command line:

```bash
./sussurro --whisper   # (or --wsp)
```

| Model | Size | Best for |
|-------|------|----------|
| Whisper Small | ~488 MB | Faster, lower RAM |
| Whisper Large v3 Turbo | ~1.62 GB | Higher accuracy |

---

## Companion Tools

### `sussurro-transcribe` — File Transcription

A standalone CLI for transcribing audio files using the same local models. Requires `ffmpeg`.

# Install
```bash
curl -fsSL https://raw.githubusercontent.com/cesp99/sussurro/master/scripts/install-transcribe.sh | bash
```
# Usage
```bash
sussurro-transcribe -i recording.mp3              # raw Whisper output to stdout
sussurro-transcribe -i recording.wav -clean       # with LLM cleanup
sussurro-transcribe -i audio.m4a -o out.txt       # write to file
sussurro-transcribe -i audio.mp3 -lang en -debug  # force language, verbose
```

See [File Transcription](docs/transcribe.md) for full documentation.


---

## License

GNU General Public License v3.0 — see [LICENSE](LICENSE).
