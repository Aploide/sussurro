# Configuration Guide

Sussurro uses a flexible configuration system powered by [Viper](https://github.com/spf13/viper).

## Loading Mechanism

When Sussurro starts, it looks for a configuration file in the following order:

1. **Command Line Flag**: If provided via `-config`.

    ```bash
    ./sussurro -config /path/to/my-config.yaml
    ```

2. **Current Directory**: Checks for `config.yaml` in the directory where the binary is run.
3. **Home Directory**: Checks for `~/.sussurro/config.yaml`.
4. **Configs Directory**: Checks for `./configs/config.yaml`.
5. **Fallback**: If `config.yaml` is not found, the same paths are checked for `default.yaml`.

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

`dictionary` lists names and terms Whisper should prefer while decoding and
then normalizes recognized text to the saved spelling, including on the fast
raw-output path. The decoder bias can improve names, technical terms, and word
boundaries that post-processing alone cannot recover. Whisper treats its initial
prompt as prior transcript, so a wrongly selected non-speech source such as an
output monitor can echo a dictionary term; choose the intended microphone. The
list can also be edited under **Settings → Dictation → Personal dictionary**;
changes saved there apply to the next dictation without a restart.

### Cleanup behavior

Raw output is enabled by default so text reaches the clipboard without waiting
several seconds for synchronous LLM inference. To opt into cleanup, set
`app.skip_llm_cleanup: false` or turn off **Raw output** under **Settings →
Dictation**.

When enabled, cleanup first removes filler words and stutters deterministically.
The LLM then uses the surrounding sentence to propose corrections for obvious
speech-recognition mistakes, such as choosing between “base” and “bass”. A
strict validator admits only similar-sounding substitutions: it rejects
inserted, deleted, or reordered words, punctuation changes, and more than one
changed token group per ten input words. Rejected output and inference errors
leave the text unchanged.

The bundled `qwen3-sussurro` model uses its trained cleanup prompt plus
contextual examples. Set `models.llm.extended_prompt: true` with a general
instruct model to use narrower correction-only instructions instead. The same
output validator applies to both modes.

The personal dictionary is applied after recognition regardless of whether LLM
cleanup is enabled. With cleanup enabled, dictionary normalization follows
contextual correction and deterministic formatting turns dictated enumerations
("first... second... third...") into numbered lines. A spoken series must start
with "first" and contain at least two in-order markers to trigger.

### Audio Settings

```yaml
audio:
  sample_rate: 16000 # Required by Whisper
  channels: 1        # Mono audio
  bit_depth: 16
  buffer_size: 1024
  max_duration: "2m"  # Safety cap for one recording ("0" means no limit)
  min_duration: "300ms" # Shorter captures are ignored as accidental presses
```

### Model Settings

Sussurro uses a Whisper ASR model, a small Silero voice-activity model, and an
LLM cleanup model.

```yaml
models:
  asr:
    path: "/home/you/.sussurro/models/ggml-small.bin"
    vad_path: "/home/you/.sussurro/models/ggml-silero-v6.2.0.bin"
    vad_threshold: 0.01 # Increase toward 1.0 if non-speech passes the VAD
    type: "whisper"
    threads: 4
    language: "en"   # BCP-47 code passed to Whisper; "auto" for auto-detection
  llm:
    path: "/home/you/.sussurro/models/qwen3-sussurro-q4_k_m.gguf" # Path to Qwen 3 model
    context_size: 32768                   # Qwen 3 supports large context
    gpu_layers: 0                         # Set > 0 if compiled with Metal or CUDA support
    threads: 4
```

Use absolute paths for model files. The first run setup writes a config file
with absolute paths based on your home directory. `vad_path` selects the Silero
model that keeps silence and non-speech noise out of Whisper; this prevents
stock subtitle phrases from appearing at the end of dictations. Existing
configs may omit it—the default is
`~/.sussurro/models/ggml-silero-v6.2.0.bin`. `vad_threshold` is Silero's
speech-probability cutoff from 0 to 1. The measured default (`0.01`) preserves
very quiet speech; raise it in environments where speech-like background noise
passes the VAD.

#### Whisper ASR Models

Two Whisper models are supported. During first-run setup you will be asked which one to download. You can also switch at any time:

```bash
sussurro --whisper   # or: sussurro --wsp
```

| Model                  | Filename                  | Size    | Notes                   |
| ---------------------- | ------------------------- | ------- | ----------------------- |
| Whisper Small          | `ggml-small.bin`          | 488 MB  | Faster, lower RAM       |
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
  push_to_talk: "ctrl+shift+space" # hold to record, release to transcribe
  toggle: ""                       # press once to start, again to stop
```

The two bindings are independent and each optional, so one key can be held
for push-to-talk while another is tapped to toggle. Leaving both empty is
valid — on Wayland the trigger socket is used instead.

| Setting        | Behaviour                                                 |
| -------------- | --------------------------------------------------------- |
| `push_to_talk` | Hold to record; release to transcribe.                    |
| `toggle`       | Press once to start recording; press again to transcribe. |

Both can be changed from **Settings → Global Hotkey** and take effect
immediately without a restart. Not applicable on Wayland, where shortcuts are
configured in the desktop environment.

The superseded `trigger` and `mode` keys are still read for existing configs:
`trigger` becomes whichever binding `mode` named, and is ignored if either new
binding is already set.

The trigger string is `+`-separated: modifiers first, then the key. Modifier aliases:

| Alias(es) | Linux X11 | macOS |
| ----------- | ----------- | ------- |
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

### Review Workflow Settings

```yaml
workflow:
  mode: "immediate"       # "immediate" or "review"
  streaming:
    enabled: true         # live partial transcription
    interval: "750ms"     # 100ms-10s
  input:
    backend: "auto"       # auto, native, trigger, evdev
    device: ""            # evdev only
    chord: ""             # evdev only
    cancel_chord: ""      # evdev only
  delivery:
    backend: "auto"        # auto, clipboard-paste, wtype, ydotool, clipboard-only
```

Every default reproduces the original dictation behaviour, so **omitting this
whole section changes nothing**. Existing configurations need no edits.

#### `mode`

| Value       | Behaviour                                                                                                 |
| ----------- | --------------------------------------------------------------------------------------------------------- |
| `immediate` | Transcribed text is delivered as soon as it is ready. The original behaviour, and the default.            |
| `review`    | Text is held so you can read it, dictate a correction, or discard it, and is delivered only when you ask. |

#### `streaming`

**`enabled`** shows partial transcriptions on the overlay while you are still
speaking, revising earlier words as more context arrives. On by default. It
costs extra CPU, because the audio captured so far is re-transcribed
periodically, so turn it off on hosts without GPU acceleration. Partial passes never run concurrently and never delay the final
transcription: if inference is slower than the interval, updates simply arrive
less often.

**`interval`** is the minimum gap between partial passes, as a Go duration
string (`750ms`, `1s`). Values outside 100ms–10s are rejected.

#### `input`

**`backend`** selects where recording gestures come from:

| Value | Behaviour |
| ------- | ----------- |
| `auto` | Native hotkeys on X11, macOS, and Windows; the trigger socket on Wayland. Never opens `/dev/input`. The default. |
| `native` | The in-process global hotkey listener. |
| `trigger` | The Unix socket only — for compositors that own their own key bindings. See [wayland.md](wayland.md). |
| `evdev` | Reads Linux input devices directly. Optional; see below. |

The `evdev` backend gives true press and release gestures on compositors that
cannot deliver them, at the cost of requiring membership of the `input` group:

```bash
sudo usermod -aG input $USER   # then log out and back in
```

**`device`** selects the keyboard by name substring or exact path. Empty picks
the first stable `/dev/input/by-id` keyboard. A name matching several devices
is an error rather than a silent choice of the wrong keyboard.

**`chord`** is the recording combination in the same notation as
`hotkey.trigger`. Empty follows `hotkey.trigger`. Either side of a modifier
satisfies it, and the parts may be pressed in any order.

**`cancel_chord`** abandons a review session and discards the held text. Empty
disables it.

These three keys apply to `evdev` only; other backends ignore them.

#### `delivery`

**`backend`** selects how reviewed text is inserted:

| Value | Behaviour |
| ------- | ----------- |
| `auto` | Uses `ydotool` or `wtype` when installed, otherwise clipboard paste. The default. |
| `clipboard-paste` | Stages the text on the clipboard and sends the paste keystroke. Works on X11, macOS, and Windows with no extra packages. |
| `wtype` | Types through the Wayland virtual keyboard protocol. Requires `wtype`. |
| `ydotool` | Types through the `ydotool` uinput daemon. Requires `ydotool` and its daemon. |

Naming a backend whose tool is not installed is an error rather than a silent
downgrade, so a misconfigured host is diagnosable. `auto` never fails this way.

`clipboard-only` is useful when the focused window is not where the text
belongs, or when you want to look before committing. It is one of the backend
values rather than a separate switch: "how to insert" has no meaning when
nothing is inserted, and offering both produced a setting that did nothing.

The superseded `clipboard_only: true` boolean is still honoured for existing
configs, and is equivalent to `backend: "clipboard-only"`.

Delivery waits for the trigger keys to be released before typing, so text is
not turned into keyboard shortcuts by a modifier that is still held. It inserts
the text exactly, adding no trailing space, and refuses to deliver empty text
so a stray Enter cannot reach a window you never dictated into.

All of these settings are also available in **Settings → Review workflow**,
which marks options this host cannot use and explains why.

### Environment Variables

All configuration values can be overridden using environment variables prefixed with `SUSSURRO_`. Nested keys are separated by underscores.

Example:

```bash
export SUSSURRO_APP_DEBUG=true
export SUSSURRO_MODELS_LLM_THREADS=8
./sussurro
```

The review workflow keys follow the same rule:

| Variable | Setting |
| ---------- | --------- |
| `SUSSURRO_WORKFLOW_MODE` | `workflow.mode` |
| `SUSSURRO_WORKFLOW_STREAMING_ENABLED` | `workflow.streaming.enabled` |
| `SUSSURRO_WORKFLOW_STREAMING_INTERVAL` | `workflow.streaming.interval` |
| `SUSSURRO_WORKFLOW_INPUT_BACKEND` | `workflow.input.backend` |
| `SUSSURRO_WORKFLOW_INPUT_DEVICE` | `workflow.input.device` |
| `SUSSURRO_WORKFLOW_INPUT_CHORD` | `workflow.input.chord` |
| `SUSSURRO_WORKFLOW_INPUT_CANCEL_CHORD` | `workflow.input.cancel_chord` |
| `SUSSURRO_WORKFLOW_DELIVERY_BACKEND` | `workflow.delivery.backend` |
| `SUSSURRO_WORKFLOW_DELIVERY_CLIPBOARD_ONLY` | `workflow.delivery.clipboard_only` |

```bash
SUSSURRO_WORKFLOW_MODE=review ./sussurro   # try review mode without editing the config
```
