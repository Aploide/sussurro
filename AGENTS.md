# Notes for agents working on Sussurro

Practical techniques for working on this repository, recorded so they do not
have to be rediscovered.

## Testing dictation without a human speaking

Sussurro records from the default audio input, so an agent cannot exercise a
real dictation without a microphone. A PulseAudio/PipeWire null sink solves
this: it exposes a `.monitor` source that Sussurro can record from, and
anything played into the sink is what Sussurro hears.

This is a **virtual microphone fed from an audio file**, not text to speech.
No TTS engine is installed on this machine (`espeak`, `pico2wave`, `flite`
and `festival` are all absent), so speech has to come from a recording that
already exists, or from a TTS engine installed first.

### Creating the virtual microphone

```bash
MOD=$(pactl load-module module-null-sink \
    sink_name=sussurro_test \
    sink_properties=device.description=SussurroTest)
```

`sussurro_test.monitor` now appears as a recording source. Point Sussurro at
it, or make it the default:

```bash
pactl set-default-source sussurro_test.monitor
```

### Feeding it audio

```bash
paplay -d sussurro_test speech.wav
```

Whisper wants 16 kHz mono. Convert with either tool:

```bash
sox input.wav -r 16000 -c 1 speech.wav
ffmpeg -i input.mp3 -ar 16000 -ac 1 speech.wav
```

### Verifying the loopback before trusting a result

A silent test looks identical to a broken rig, so confirm audio actually
flows before concluding anything about Sussurro:

```bash
sox -n -r 16000 -c 1 /tmp/probe.wav synth 0.5 sine 440
parec -d sussurro_test.monitor --file-format=wav /tmp/captured.wav &
paplay -d sussurro_test /tmp/probe.wav
kill %1
ls -l /tmp/captured.wav        # non-trivial size means the path works
```

### Cleaning up

Always unload the module afterwards; it persists for the session otherwise
and changes the user's audio setup.

```bash
pactl unload-module "$MOD"
```

### Known limitation: not yet demonstrated end to end

The loopback itself is verified: a tone played into the sink is captured from
its monitor. Getting **Sussurro** to record from it is the part that has not
been made to work.

Both attempts failed at startup with:

```
Failed to start recording  error="failed to init device: miniaudio: Failed to open backend device"
```

Sussurro opens its capture device once, when the pipeline starts, so calling
`pactl set-default-source` afterwards is too late — the default must already
point at the monitor before the process launches. Setting it beforehand was
not tried, and doing so changes the user's default input device, which must
be restored afterwards:

```bash
PREV=$(pactl get-default-source)
pactl set-default-source sussurro_test.monitor
# ... run the test ...
pactl set-default-source "$PREV"
```

Treat this section as a starting point rather than a working recipe, and
verify audio actually reached the pipeline — `Recording stopped` logs a
`samples=` count, and `samples=0` means it did not.

## Running builds and the test suite

Use `make test` and `make build`, never a bare `go test` or `go build`. The
Makefile supplies the cgo link flags for whisper.cpp and go-llama.cpp,
including the Vulkan libraries when that backend is built. A bare `go build`
fails at link time with `undefined reference to wsp_ggml_backend_vk_reg` or
`cannot find -lbinding`, which looks like a code error but is not. (`go vet`
does work standalone, and is a cheap syntax check that needs no link.)

### Why they are slow, and what that means

`make test` takes about **4m30s even when every Go test is cached**. Measured:

```
make test  2271.83s user  117.25s system  871% cpu  4:34.29 total
```

The Go tests are not the cost — they were all `(cached)` in that run. Both
targets depend on `deps`, which runs `cmake` configure and a build check over
whisper.cpp and go-llama.cpp unconditionally; the only guard skips the *clone*,
not the build.

This is a defect, tracked as `sussurro-2cp`, not a cost to absorb quietly. A
build or test run over ~30 seconds means something is wrong with the Makefile
or the suite, and is worth reporting rather than tolerating.

### How to run them

- **Never block on them.** Use `run_in_background: true`, or a `Monitor` when
  you want progress events rather than a single completion. Blocking the turn
  on a multi-minute build wastes the user's time.
- **Never re-run to ask a second question.** Capture once with `tee` into
  `tmp/`, then grep that file as many times as needed. Running `make build`
  twice to grep two different things costs two full builds.
- **Never discard or truncate the output.** No `> /dev/null`, and do not let
  `tail -n` be your only look — a real error can appear anywhere in the log,
  and third-party compile lines make naive `grep error` match noise. Filter to
  this repository's own paths, e.g.
  `grep -E "^internal/|^cmd/|\.go:[0-9]+" tmp/build.log`.
- **One waiter per job.** Chain dependent steps inside a single background
  command rather than spawning a separate poll loop for each; several shells
  polling the same file is pure overhead.
- **Always set a timeout** on background waits so a stalled job cannot hang
  indefinitely.

## The settings page is embedded in the binary

`internal/ui/assets/{index.html,style.css,app.js}` are compiled in with
`go:embed`. Editing them changes nothing until `make build` completes and the
binary is reinstalled. Reporting a CSS fix before the build lands wastes the
user's time testing an unchanged binary.

## Measuring the settings window

The page can be rendered headlessly against the real stylesheet to measure
computed styles and geometry:

```go
//go:build ignore
// webview.New(false); w.SetHtml(page); w.Bind("report", ...)
```

Two traps, both of which have produced wrong measurements here:

- **Hidden panels do not lay out.** Measuring a panel while `hidden` reports a
  collapsed height. Make it the only visible panel first.
- **The harness cannot answer the page's bridge calls.** The model list is
  populated by a Go binding, so in a harness it renders empty and any height
  measured from the Models tab is far too small. Only the running application
  gives a true figure for that tab.

## Display scaling

`window_scale()` in `internal/ui/settings_native_linux.go` reads `gtk-xft-dpi`,
which is fractional font DPI scaling (`Xft.dpi`, GNOME `text-scaling-factor`),
not GTK's integer window scale factor. GTK applies the integer factor to window
geometry itself; it does not apply the fractional one, but WebKit applies it to
page content. That is why a window sized in device pixels yields a smaller CSS
viewport than expected.

`gtk_settings_get_default()` returns NULL before GTK is initialised and the
function then silently falls back to a scale of 1.0, which is exactly the bug
that made the settings window too small. `gtk_init_check` is called first for
this reason.
