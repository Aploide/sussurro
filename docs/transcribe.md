# sussurro-transcribe — File Transcription CLI

`sussurro-transcribe` is a companion command-line tool that transcribes audio files using the same local Whisper and LLM models that power the main Sussurro app. No internet connection, no cloud, no additional model downloads if you already have Sussurro set up.

---

## Prerequisites

| Requirement | Why |
|-------------|-----|
| Sussurro already configured | Shares `~/.sussurro/config.yaml` and model files |
| `ffmpeg` on `PATH` | Decodes any audio format to raw PCM before transcription |

Install `ffmpeg` if not already present:

```bash
# Arch / Manjaro
sudo pacman -S ffmpeg

# Ubuntu / Debian
sudo apt install ffmpeg

# Fedora
sudo dnf install ffmpeg

# macOS (Homebrew)
brew install ffmpeg
```

---

## Build

From the repository root:

```bash
make build-transcribe
# produces: bin/sussurro-transcribe
```

This target links only against Whisper and llama — no GTK, no WebKit, no UI dependencies. It compiles cleanly on any machine that can run `make deps`.

To build **both** binaries at once:

```bash
make build             # → bin/sussurro
make build-transcribe  # → bin/sussurro-transcribe
```

Or use the combined release script:

```bash
./scripts/package-release-all.sh
```

---

## Installation

To install `sussurro-transcribe` system-wide:

```bash
./scripts/install-transcribe.sh
```

The script downloads the latest pre-built binary from GitHub Releases and places it in `/usr/local/bin` (or `~/.local/bin` if you don't have sudo).

---

## Usage

```
sussurro-transcribe -i <audio-file> [options]
```

### Flags

| Flag | Default | Description |
| ------ | --------- | ------------- |
| `-i <file>` | *(required)* | Input audio file — any format ffmpeg supports (MP3, WAV, M4A, OGG, FLAC, …) |
| `-o <file>` | stdout | Write transcription to a file instead of printing to stdout |
| `-clean` | off | Run LLM cleanup on the raw Whisper output (removes filler words, fixes self-corrections) |
| `-lang <code>` | from config | Override transcription language (`en`, `fr`, `de`, `auto`, …) |
| `-config <path>` | `~/.sussurro/config.yaml` | Use a custom configuration file |
| `-debug` | off | Print verbose debug output to stderr |

### Examples

```bash
# Transcribe a recording, print to terminal
sussurro-transcribe -i meeting.mp3

# Transcribe and clean up with LLM, save to file
sussurro-transcribe -i interview.wav -clean -o transcript.txt

# Force English, use debug output
sussurro-transcribe -i audio.m4a -lang en -debug

# Transcribe multiple files in a loop
for f in recordings/*.mp3; do
    sussurro-transcribe -i "$f" -o "${f%.mp3}.txt"
done

# Pipe output into another tool
sussurro-transcribe -i voice-note.ogg | wc -w
```

---

## How It Works

1. **Audio decoding** — `ffmpeg` converts the input file to 16 kHz mono 32-bit float PCM (the format Whisper expects). Any container or codec that ffmpeg supports works transparently.
2. **ASR** — The PCM samples are passed to the Whisper engine (`internal/asr`), the same code path used during live microphone recording.
3. **LLM cleanup** (optional, `-clean`) — The raw Whisper text is passed to the LLM engine (`internal/llm`). The model removes filler words, handles self-corrections, and produces clean, readable text.
4. **Output** — The final text is written to stdout or to the file specified with `-o`.

The binary reads `~/.sussurro/config.yaml` for model paths, thread counts, and
language settings, so no extra configuration is required once Sussurro is
already set up. When upgrading an older configuration, it automatically
downloads the small Silero voice-activity model before the first transcription.

---

## Configuration

`sussurro-transcribe` reads the same `config.yaml` as the main app. Relevant fields:

```yaml
models:
  asr:
    path: ~/.sussurro/models/whisper-large-v3-turbo.bin
    vad_path: ~/.sussurro/models/ggml-silero-v6.2.0.bin
    vad_threshold: 0.01
    threads: 4
    language: auto          # overridden by -lang flag
  llm:
    path: ~/.sussurro/models/qwen3-sussurro.gguf
    threads: 4
    context_size: 4096
    gpu_layers: 99          # Offloads layers when the cleanup backend supports it
```

To use a completely separate configuration (different models, different language):

```bash
sussurro-transcribe -i audio.mp3 -config ~/my-transcribe-config.yaml
```

---

## Supported Audio Formats

Any format that `ffmpeg` can decode works: MP3, WAV, FLAC, M4A, AAC, OGG, Opus, WebM, MP4 (audio track), and many more. The tool does not care about the container — it only sees the decoded PCM stream.

---

## Troubleshooting

### `ffmpeg: command not found`

Install `ffmpeg` — see the Prerequisites section above.

### `Error: failed to load config`

Run `sussurro` at least once to generate `~/.sussurro/config.yaml`, or point to a config file with `-config`.

### `Error: failed to initialize ASR engine`

The Whisper model file is missing or the path in `config.yaml` is wrong. Run `sussurro --whisper` to download models, or update the `models.asr.path` field in your config.

### `Warning: LLM cleanup failed, using raw transcription`

The LLM model is unavailable (missing file, out of memory, etc.). The raw Whisper transcription is still returned — `-clean` degrades gracefully.

### Output is empty or very short

- The audio may be too quiet or silent. Check the file plays back correctly.
- Try `-lang en` (or the actual language) instead of `auto` for short clips.
- Add `-debug` to see what Whisper receives and outputs.
