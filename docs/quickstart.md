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
No extra dependencies required. The overlay capsule, settings window, and right-click context menu all work natively.

> **Accessibility permission:** On first run macOS will ask you to grant Accessibility access (System Settings → Privacy & Security → Accessibility). This is required for the global hotkey (CGEventTap).

## Step 3: Download Sussurro

Go to [GitHub Releases](https://github.com/cesp99/sussurro/releases) and download the binary for your platform:

```bash
tar -xzf sussurro-*.tar.gz
cd sussurro-*
chmod +x sussurro

# macOS only:
xattr -d com.apple.quarantine sussurro 2>/dev/null || true
```

## Step 4: Run for the First Time

```bash
./sussurro
```

Follow the prompts to choose and download the AI models:
- **Whisper Small** (~488 MB) — faster, good accuracy
- **Whisper Large v3 Turbo** (~1.62 GB) — slower, best accuracy
- **Qwen 3 Sussurro LLM** (~1.28 GB) — always required

After download completes, the overlay capsule appears at the bottom of your screen on both Linux and macOS.

> **Tip:** Switch Whisper model any time via the Settings window or `./sussurro --whisper`.

## Step 5: Configure Hotkey (Wayland Only)

**Skip this if you're on X11 or macOS — hotkeys work automatically.**

On Wayland, configure a keyboard shortcut in your DE that calls the trigger script.

### GNOME (Wayland)
1. Settings → Keyboard → Keyboard Shortcuts → Custom Shortcuts → **+**
2. Name: `Sussurro Start`, Command: `/path/to/trigger.sh start`, Shortcut: `Ctrl+Shift+Space`
3. Add a second: `Sussurro Stop`, Command: `/path/to/trigger.sh stop`, same shortcut on key-release

### KDE Plasma (Wayland)
1. System Settings → Shortcuts → Custom Shortcuts → New → Global Shortcut → Command/URL
2. Trigger: `Ctrl+Shift+Space`, Action: `/path/to/trigger.sh`

### Sway / Hyprland
See [wayland.md](wayland.md) for config file snippets.

## Step 6: Test It

1. Open any text editor and click inside it
2. **Linux X11 / macOS:** Hold the configured hotkey (default `Ctrl+Shift+Space` on Linux, `Cmd+Shift+Space` on macOS), speak, release
3. **Wayland:** Press once, speak, press again
4. Watch the capsule animate — then text appears

## Step 7: Explore Settings

Open the Settings window: **right-click the overlay capsule** → **Open Settings**.

From Settings you can:
- Switch or download Whisper models with a live progress bar
- Change the transcription language (Auto Detect, English, German, Spanish, French, Portuguese, Russian, Italian)
- Change the global hotkey (X11 / macOS) — takes effect immediately, no restart needed
  - Hold up to 3 keys in the recorder, then release them all to save

## Troubleshooting

### Overlay doesn't appear
Check that GTK3 is installed (`pkg-config --exists gtk+-3.0 && echo ok`).
On Wayland without `gtk-layer-shell`, the overlay appears as a floating window — check your compositor's window rules if it hides under other windows.

### Settings window doesn't open
Right-click the capsule at the bottom of your screen and choose **Open Settings** — that's the only way to reach Settings or quit the app.

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

To stop manually: right-click the capsule → **Quit**.
