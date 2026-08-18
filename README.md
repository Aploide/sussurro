# Sussurro

[![Version 2.3](https://img.shields.io/badge/Version-2.3-black?style=flat)](https://github.com/aploide/sussurro/releases)
[![GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-black?style=flat)](LICENSE)
[![Go 1.24+](https://img.shields.io/badge/Go-1.24+-black?style=flat&logo=go&logoColor=white)](https://golang.org)
[![Linux](https://img.shields.io/badge/Linux-black?style=flat&logo=linux&logoColor=white)](https://github.com/aploide/sussurro)
[![macOS](https://img.shields.io/badge/macOS-black?style=flat&logo=apple&logoColor=white)](https://github.com/aploide/sussurro)
[![Windows](https://img.shields.io/badge/Windows-black?style=flat&logo=windows&logoColor=white)](https://github.com/aploide/sussurro)
[![DeepWiki](https://img.shields.io/badge/DeepWiki-black?style=flat&logo=readthedocs&logoColor=white)](https://deepwiki.com/aploide/sussurro)

Sussurro is a fully local, open-source voice-to-text system with a built-in native overlay UI. It transforms speech into clean, formatted, context-aware text and injects it into any application — entirely on your machine, using **Whisper.cpp** for ASR and a fine-tuned **Qwen 3** LLM for cleanup.

## Install

**macOS & Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/aploide/sussurro/master/scripts/install.sh | bash
```

The script detects your platform, verifies the release checksum, and places the binary in `/usr/local/bin` (or `~/.local/bin`). On Linux it also installs the Wayland trigger as `sussurro-trigger`.

**Windows** (PowerShell)

```powershell
irm https://raw.githubusercontent.com/aploide/sussurro/master/scripts/install.ps1 | iex
```

Installs into `%LOCALAPPDATA%\Programs\Sussurro` and adds it to your user PATH.

On first run Sussurro will guide you through downloading the AI models.

> **Wayland users:** after install, bind `sussurro-trigger` to your hotkey in your desktop environment — see [Wayland Setup](docs/wayland.md).
> **macOS users:** grant Accessibility access when prompted (System Settings → Privacy & Security → Accessibility). Apple Silicon only — Intel Macs must [build from source](docs/compilation.md).
> **Windows users:** needs the WebView2 runtime (preinstalled on Windows 11). GPU acceleration runs on Vulkan via your graphics driver.

---

## Features

- **Built-in Native Overlay**: A minimal, aesthetically clean floating capsule shows recording/transcribing state — always on top, no taskbar entry *(Linux, macOS & Windows)*
- **Settings UI**: Dark-themed settings window accessible via system tray or right-click on the overlay *(Linux, macOS & Windows)*
- **Smart Cleanup**: Removes filler words, handles self-corrections, prevents hallucinations
- **Local Processing**: No data leaves your machine
- **System-Wide**: Works in any application where you can type
- **Flexible ASR**: Whisper Small (fast) or Large v3 Turbo (accurate), switchable from the UI
- **Live Hotkey Config**: Change the global hotkey from Settings — takes effect instantly, no restart
- **Hotkey Mode**: Switch between *Push to Talk* (hold to record, release to transcribe) and *Toggle* (press once to start, press again to transcribe) directly from Settings *(X11, macOS & Windows — not Wayland)*
- **GPU Acceleration**: Metal on macOS, Vulkan on Windows
- **Transcription Language**: Choose the language Whisper listens for (or use Auto Detect) directly from Settings
- **Headless Mode**: `--no-ui` flag for CLI/scripting use on any platform
- **Review Mode** *(opt-in)*: Hold the transcription before it is delivered — read it, dictate a correction, or discard it, then insert it when you are ready. Off by default; see [Review workflow](#review-workflow)

---

## Quick Reference

| Platform | Default hotkey | Default mode | Access Settings |
|----------|---------------|-------------|----------------|
| Linux X11 | `Ctrl+Shift+Space` | Push to Talk | System tray or right-click capsule |
| Linux Wayland | configured in DE | n/a (external shortcut) | System tray or right-click capsule |
| macOS | `Cmd+Shift+Space` | Push to Talk | System tray or right-click capsule |
| Windows | `Ctrl+Shift+Space` | Push to Talk | System tray or right-click capsule |

The hotkey mode can be changed at any time from **Settings → Global Hotkey → Mode**.

---

## Review workflow

By default Sussurro delivers each transcription as soon as it is ready. That
behaviour is unchanged, and nothing below is enabled unless you ask for it.

**Review mode** holds the text instead, so you can check it first:

```yaml
workflow:
  mode: "review"
```

or without editing the config at all:

```bash
SUSSURRO_WORKFLOW_MODE=review sussurro
```

In review mode a completed transcription waits in a *Ready* state where you can:

- **Deliver** it into the focused window, optionally followed by Enter
- **Dictate a correction** — hold the hotkey again and say what to change; one
  revision back can be undone
- **Cancel** it, discarding the text entirely

A failed delivery leaves the text in *Ready* rather than losing it.

**Live transcription** shows partial text while you are still speaking:

```yaml
workflow:
  streaming:
    enabled: true
```

It costs extra CPU. Partial passes never overlap and never delay the final
transcription — on a slow machine the updates simply arrive less often.

Input and delivery are pluggable for compositors the defaults cannot serve:
`wtype` and `ydotool` for typing, and Linux `evdev` for true press-and-release
gestures. All are optional, and the defaults need no extra packages. See
[docs/configuration.md](docs/configuration.md) for every setting, or use
**Settings → Review workflow**, which marks what this host cannot do and why.

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
git clone https://github.com/aploide/sussurro.git
cd sussurro
make build        # → bin/sussurro  (overlay + settings + tray)
```

Requires GTK3 and WebKit2GTK dev headers on Linux. On Windows, build under MSYS2 MINGW64 with the Vulkan SDK packages. See [Compilation](docs/compilation.md) for full instructions and per-platform dependency lists.

---

## UI: The Overlay Capsule

When Sussurro runs (Linux, macOS, or Windows), a sleek pill-shaped capsule appears at the bottom-center of your screen:

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

#### Install

macOS & Linux:
```bash
curl -fsSL https://raw.githubusercontent.com/aploide/sussurro/master/scripts/install-transcribe.sh | bash
```

Windows (PowerShell):
```powershell
irm https://raw.githubusercontent.com/aploide/sussurro/master/scripts/install-transcribe.ps1 | iex
```

#### Usage
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
