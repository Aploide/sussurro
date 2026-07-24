# Configuration Guide

Sussurro uses a flexible configuration system powered by [Viper](https://github.com/spf13/viper).

## Loading Mechanism

When Sussurro starts, it looks for a configuration file in the following order:

1.  **Command Line Flag**: If provided via `-config`.
    ```bash
    ./sussurro -config /path/to/my-config.yaml
    ```
2.  **Current Directory**: Checks for `config.yaml` in the directory where the binary is run.
3.  **Home Directory**: Checks for `~/.sussurro/config.yaml`.
4.  **Configs Directory**: Checks for `./configs/config.yaml`.
5.  **Fallback**: If `config.yaml` is not found, the same paths are checked for `default.yaml`.

## Configuration Structure (`config.yaml`)

The repo also includes `configs/default.yaml` with the same keys. It is a fallback if `config.yaml` is missing.

### App Settings
```yaml
app:
  name: "Sussurro"
  debug: true        # Enable verbose logging
  log_level: "info"  # debug, info, warn, error
  dictionary:        # Personal vocabulary (optional)
    - "Sussurro"
    - "Kubernetes"
```

`dictionary` lists names and terms the cleanup stage must spell exactly as
written. When the ASR mishears one of them (e.g. "Susserow" for "Sussurro"),
the cleanup replaces it with the correct spelling. Restart Sussurro after
editing the list.

### Cleanup behavior

Long dictations are cleaned in sentence-aligned chunks with strict output
validation: a chunk whose cleanup drops content, invents content, or loses the
original ending is automatically retried on smaller pieces and, as a last
resort, kept verbatim. The cleanup therefore never summarizes — worst case a
few filler words survive in a difficult passage.

Dictated enumerations ("first... second... third...") are automatically
reformatted into numbered list lines. This is done deterministically after
cleanup (LLMs apply layout instructions inconsistently), so it works with any
model and never alters wording — a spoken series must start with "first" and
contain at least two in-order markers to trigger.

`models.llm.extended_prompt: true` switches to a richer instruction set with
an explicit no-summarization contract and a prompt-level dictionary. The
bundled `qwen3-sussurro` fine-tune does not follow these extra instructions
(it is trained on the default prompt) — use this option together with a
general instruct model in `models.llm.path` (e.g. a Qwen3 Instruct GGUF) for
noticeably more thorough filler removal on long dictations, at the cost of
slower CPU cleanup.

### Audio Settings
```yaml
audio:
  sample_rate: 16000 # Required by Whisper
  channels: 1        # Mono audio
  bit_depth: 16
  buffer_size: 1024
  max_duration: "60s" # Maximum recording time (default: 60s, 0 for no limit)
```

### Model Settings
Sussurro requires two models: one for ASR and one for LLM cleanup.

```yaml
models:
  asr:
    path: "/home/you/.sussurro/models/ggml-small.bin"
    type: "whisper"
    threads: 4
    language: "en"   # BCP-47 code passed to Whisper; "auto" for auto-detection
  llm:
    path: "/home/you/.sussurro/models/qwen3-sussurro-q4_k_m.gguf" # Path to Qwen 3 model
    context_size: 32768                   # Qwen 3 supports large context
    gpu_layers: 0                         # Set > 0 if compiled with Metal or CUDA support
    threads: 4
```

Use absolute paths for model files. The first run setup writes a config file with absolute paths based on your home directory.

#### Whisper ASR Models

Two Whisper models are supported. During first-run setup you will be asked which one to download. You can also switch at any time:

```bash
sussurro --whisper   # or: sussurro --wsp
```

| Model | Filename | Size | Notes |
|-------|----------|------|-------|
| Whisper Small | `ggml-small.bin` | 488 MB | Faster, lower RAM |
| Whisper Large v3 Turbo | `ggml-large-v3-turbo.bin` | 1.62 GB | Slower, higher accuracy |

The `--whisper` / `--wsp` flag opens an interactive menu, downloads the chosen model if needed, and updates `~/.sussurro/config.yaml` automatically.

#### Transcription language

The `language` key tells Whisper which language to expect. Use any [BCP-47 code supported by Whisper](https://github.com/openai/whisper#available-models-and-languages) (e.g. `"en"`, `"it"`, `"de"`, `"fr"`) or `"auto"` to let the model detect the language automatically. Defaults to `"en"`.

The value can be changed at any time from the **Settings → Transcription Language** dropdown; the new value is written to `~/.sussurro/config.yaml` immediately and takes effect on next launch. It can also be overridden via the environment:

```bash
export SUSSURRO_MODELS_ASR_LANGUAGE=it
```

### Hotkey Settings
```yaml
hotkey:
  trigger: "ctrl+shift+space" # The key combination to use for recording
  mode: "push-to-talk"        # "push-to-talk" or "toggle"
```

**`mode`** controls how the hotkey activates recording:

| Value | Behaviour |
|-------|-----------|
| `push-to-talk` | Hold the hotkey to record; release to transcribe. |
| `toggle` | Press once to start recording; press again to transcribe. |

Defaults to `"push-to-talk"`. Can be changed from **Settings → Global Hotkey → Mode** and takes effect immediately without a restart. Not applicable on Wayland (Wayland users configure their own shortcuts externally).

The trigger string is `+`-separated: modifiers first, then the key. Modifier aliases:

| Alias(es) | Linux X11 | macOS |
|-----------|-----------|-------|
| `ctrl`, `control` | `Control_L` | `⌃ Control` |
| `shift` | `Shift_L` | `⇧ Shift` |
| `alt`, `option` | Mod1 (`Alt_L`) | `⌥ Option` |
| `cmd`, `command`, `super`, `meta` | Mod4 (`Super_L`) | `⌘ Command` |

**Examples:**
```yaml
trigger: "ctrl+shift+space"   # default Linux
trigger: "cmd+shift+space"    # default macOS
trigger: "alt+shift+f2"       # any platform
trigger: "super+space"        # Linux (Super/Windows key)
```

> **Note:** Hotkey changes made in the Settings window take effect immediately — no restart is required.

### Injection Settings
```yaml
injection:
  method: "keyboard"
```

### Environment Variables

All configuration values can be overridden using environment variables prefixed with `SUSSURRO_`. Nested keys are separated by underscores.

Example:
```bash
export SUSSURRO_APP_DEBUG=true
export SUSSURRO_MODELS_LLM_THREADS=8
./sussurro
```
