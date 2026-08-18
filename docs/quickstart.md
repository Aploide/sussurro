# Quick Start Guide

Get Sussurro running in under 5 minutes.

## Step 1: Check Your Display Server

```bash
echo $XDG_SESSION_TYPE   # prints "wayland" or "x11"
```

## Step 2: Install Dependencies (Linux Only)

### Wayland users
```bash
# Arch/Manjaro
sudo pacman -S gtk3 webkit2gtk-4.1 wl-clipboard gtk-layer-shell

# Ubuntu/Debian (22.04+)
sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev \
                 wl-clipboard libgtk-layer-shell-dev

# Fedora
sudo dnf install gtk3 webkit2gtk4.1 wl-clipboard
```

### X11 users
```bash
# Arch/Manjaro
sudo pacman -S gtk3 webkit2gtk-4.1

# Ubuntu/Debian
sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev

# Fedora
sudo dnf install gtk3 webkit2gtk4.1
```

### macOS
No extra dependencies required. The overlay capsule, settings window, system tray, and right-click context menu all work natively.

> **Accessibility permission:** On first run macOS will ask you to grant Accessibility access (System Settings → Privacy & Security → Accessibility). This is required for the global hotkey (CGEventTap).

### Windows
No extra dependencies required beyond the **WebView2 runtime**, which powers the overlay and settings window and is preinstalled on Windows 11. On Windows 10, get it from [Microsoft](https://developer.microsoft.com/microsoft-edge/webview2/).

GPU acceleration for Whisper runs on **Vulkan** through your graphics driver; if no Vulkan device is available it falls back to CPU.

## Step 3: Install Sussurro

The fastest path is the install script, which resolves the latest release, verifies its checksum, and puts the binary on your PATH:

```bash
# macOS & Linux
curl -fsSL https://raw.githubusercontent.com/aploide/sussurro/master/scripts/install.sh | bash
```

```powershell
# Windows
irm https://raw.githubusercontent.com/aploide/sussurro/master/scripts/install.ps1 | iex
```

To install manually instead, grab the archive for your platform from [GitHub Releases](https://github.com/aploide/sussurro/releases):

```bash
tar -xzf sussurro-*.tar.gz
cd sussurro-*
chmod +x sussurro

# macOS only:
xattr -d com.apple.quarantine sussurro 2>/dev/null || true
```

On Windows, unzip `sussurro-windows-amd64.zip` and run `sussurro.exe` from the extracted folder.

> Release archives are published for `linux-amd64`, `linux-arm64`, `macos-arm64`, and `windows-amd64`. Intel Macs are not prebuilt — see [Compilation](compilation.md).

## Step 4: Run for the First Time

> **Important:** At this stage Sussurro must be launched from a terminal. Double-clicking the binary or using a `.desktop` shortcut is not yet supported — the overlay and tray icon will not appear correctly outside a terminal session.

```bash
./sussurro          # macOS / Linux
```

```powershell
sussurro            # Windows
```

Follow the prompts to choose and download the AI models:
- **Whisper Small** (~488 MB) — faster, good accuracy
- **Whisper Large v3 Turbo** (~1.62 GB) — slower, best accuracy
- **Qwen 3 Sussurro LLM** (~1.28 GB) — always required

After download completes, the overlay capsule appears at the bottom of your screen on Linux, macOS, and Windows.

> **Tip:** Switch Whisper model any time via the Settings window or `./sussurro --whisper`.

## Step 5: Configure Hotkey (Wayland Only)

**Skip this if you're on X11, macOS, or Windows — hotkeys work automatically.**

On Wayland, configure a keyboard shortcut in your DE that calls the trigger script. The install script puts it on your PATH as `sussurro-trigger`; if you unpacked the archive manually, use the full path to the bundled `trigger.sh` instead.

### GNOME (Wayland)
1. Settings → Keyboard → Keyboard Shortcuts → Custom Shortcuts → **+**
2. Name: `Sussurro Start`, Command: `sussurro-trigger start`, Shortcut: `Ctrl+Shift+Space`
3. Add a second: `Sussurro Stop`, Command: `sussurro-trigger stop`, same shortcut on key-release

### KDE Plasma (Wayland)
1. System Settings → Shortcuts → Custom Shortcuts → New → Global Shortcut → Command/URL
2. Trigger: `Ctrl+Shift+Space`, Action: `sussurro-trigger`

### Sway / Hyprland
See [wayland.md](wayland.md) for config file snippets.

## Step 6: Test It

1. Open any text editor and click inside it
2. **Linux X11 / macOS / Windows:** Hold the configured hotkey (default `Ctrl+Shift+Space` on Linux and Windows, `Cmd+Shift+Space` on macOS), speak, release
3. **Wayland:** Press once, speak, press again
4. Watch the capsule animate — then text appears

## Step 7: Explore Settings

Open the Settings window:
- **System tray:** click the Sussurro pill icon → **Open Settings**
- **Right-click the capsule** → **Open Settings**

From Settings you can:
- Switch or download Whisper models with a live progress bar
- Change the transcription language (Auto Detect, English, German, Spanish, French, Portuguese, Russian, Italian)
- Change the global hotkey (X11 / macOS / Windows) — takes effect immediately, no restart needed
  - Hold up to 3 keys in the recorder, then release them all to save
- Turn on the **review workflow** (see Step 8), including live transcription and
  the delivery backend

---

## Step 8: Try Review Mode (optional)

By default the transcription is inserted as soon as it is ready. Review mode
holds it first, so you can check it, correct it by voice, or throw it away.

Try it without changing any files:

```bash
SUSSURRO_WORKFLOW_MODE=review ./sussurro
```

Then dictate as usual. Instead of the text appearing straight away, the session
waits in a *Ready* state, where you can:

- **deliver** the text into the focused window
- **hold the hotkey again** and say what to change — for example
  "capitalise the first word" — to revise it
- **cancel** to discard it

To keep it on, add this to `~/.sussurro/config.yaml`:

```yaml
workflow:
  mode: "review"
```

Or set it from **Settings → Review workflow**, which also offers live
transcription and marks anything this machine cannot do, with the reason.

> **Note:** review state is not yet drawn in the overlay capsule — run with
> `app.debug: true` to follow it in the logs. See
> [KNOWN_ISSUES.md](../KNOWN_ISSUES.md).

## Troubleshooting

### Overlay doesn't appear
Check that GTK3 is installed (`pkg-config --exists gtk+-3.0 && echo ok`).
On Wayland without `gtk-layer-shell`, the overlay appears as a floating window — check your compositor's window rules if it hides under other windows.

### Settings window doesn't open
Right-click the capsule at the bottom of your screen and choose **Open Settings**. If the tray icon is missing (some DEs need `snixembed` or a compatible bar), the right-click menu is the fallback.

### "clipboard failed" error
Wayland: install `wl-clipboard` (see Step 2).

### Hotkey doesn't work (X11)
Another app may have grabbed the hotkey. Change it via Settings → Global Hotkey, then the new hotkey activates immediately.

### Hotkey doesn't work (macOS)
Check that Sussurro has Accessibility permission: System Settings → Privacy & Security → Accessibility. If it was recently added to the list, toggle it off and on, then relaunch.

### Hotkey doesn't work (Wayland)
Complete Step 5. Test the trigger manually:
```bash
echo toggle | nc -U /run/user/$(id -u)/sussurro.sock
```

### No text appears
- Speak for at least 2 seconds
- Check the terminal for error messages when running with `--no-ui`

## Daily Usage

**Linux X11 / macOS:**
1. Launch Sussurro from a terminal (`./sussurro`)
2. Hold hotkey anywhere you can type → speak → release → text appears

**Linux Wayland:**
1. Launch from a terminal, then press hotkey once to start recording → speak → press again to stop → text appears

To stop manually: right-click the capsule → **Quit**, or click Quit in the tray menu.

**Review mode (any platform):** dictate as usual, then deliver, revise by
voice, or cancel. On Wayland the review actions are also available from the
trigger socket — see [wayland.md](wayland.md).
