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
