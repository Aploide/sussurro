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

Works on Linux and macOS. The script detects your platform, downloads the right binary, and places it in `/usr/local/bin` (or `~/.local/bin`). On first run Sussurro will guide you through downloading the AI models.

> **Wayland users:** after install, bind the hotkey in your desktop environment — see [Wayland Setup](docs/wayland.md).
> **macOS users:** grant Accessibility access when prompted (System Settings → Privacy & Security → Accessibility).

---

## Features

- **Built-in Native Overlay**: A minimal, aesthetically clean floating capsule shows recording/transcribing state — always on top, no taskbar entry *(Linux & macOS)*
- **Settings UI**: Dark-themed settings window accessible via system tray or right-click on the overlay *(Linux & macOS)*
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
| Linux X11 | `Ctrl+Shift+Space` | Push to Talk | System tray or right-click capsule |
| Linux Wayland | configured in DE | n/a (external shortcut) | System tray or right-click capsule |
| macOS | `Cmd+Shift+Space` | Push to Talk | System tray or right-click capsule |

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
make build        # → bin/sussurro  (overlay + settings + tray)
```

Requires GTK3, WebKit2GTK, and AppIndicator dev headers on Linux. See [Compilation](docs/compilation.md) for full instructions and per-distro dependency lists.

---

## UI: The Overlay Capsule

When Sussurro runs (Linux or macOS), a sleek pill-shaped capsule appears at the bottom-center of your screen:

| State | Appearance |
|-------|-----------|
| **Idle** | 7 softly pulsing white dots |
| **Recording** | 7 waveform bars animated by your voice |
| **Transcribing** | "transcribing" text with a shimmer effect |

**Accessing Settings:**

| Method | How |
|--------|-----|
| System tray | Click the Sussurro icon → **Open Settings** |
| Right-click overlay | Right-click the capsule → **Open Settings** |

The settings window lets you switch Whisper models, download models with a live progress bar, select the transcription language, change the global hotkey, and choose the hotkey mode. All changes take effect immediately — no restart required.

---

## Headless / CLI Mode

```bash
./sussurro --no-ui
```

Terminal output only — no overlay, no tray. Useful for scripting or low-resource environments.

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
