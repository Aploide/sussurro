# Wayland Setup Guide

Wayland does not support global hotkeys due to its security model. This guide shows you how to set up Sussurro on Wayland using your desktop environment's keyboard shortcuts.

## Am I on Wayland?

Check with:
```bash
echo $XDG_SESSION_TYPE
```
If it says `wayland`, follow this guide. If it says `x11`, you don't need this for dictation - hotkeys work automatically.

The trigger socket described under [Trigger commands](#trigger-commands) runs
on every display server, not only Wayland. X11 and macOS users need it for
`deliver`, `cancel`, and `submit`, which have no hotkey binding: review mode
cannot be completed without them.

## Prerequisites

**BEFORE running Sussurro**, install the clipboard manager:

```bash
# Arch/Manjaro
sudo pacman -S wl-clipboard

# Ubuntu/Debian
sudo apt install wl-clipboard

# Fedora
sudo dnf install wl-clipboard

# openSUSE
sudo zypper install wl-clipboard
```

Without this, Sussurro will fail to inject text.

See [dependencies.md](dependencies.md) for other optional packages.

## One-Time Setup

### Option 1: Using the Helper Script (Recommended)

`scripts/install.sh` installs the helper on your PATH as **`sussurro-trigger`**, so
that name is what the examples below use. If you unpacked a release archive by
hand, substitute the full path to the bundled `trigger.sh` instead.

1. Open your desktop environment's keyboard settings
2. Add a custom keyboard shortcut
3. Set the shortcut key: `Ctrl+Shift+Space`
4. Set the command to: `sussurro-trigger`

### Option 2: Direct Socket Command

If you prefer not to use the script:

1. Open your desktop environment's keyboard settings
2. Add a custom keyboard shortcut
3. Set the shortcut key: `Ctrl+Shift+Space`
4. Set the command to: `sh -c 'echo toggle | nc -U $XDG_RUNTIME_DIR/sussurro.sock'`

## Desktop Environment Specific Instructions

### GNOME (Settings)

1. Open **Settings** → **Keyboard** → **Keyboard Shortcuts**
2. Scroll down and click **"View and Customize Shortcuts"**
3. Click **"Custom Shortcuts"** at the bottom
4. Click the **"+"** button to add a new shortcut
5. Name: `Sussurro Voice Input`
6. Command: `sussurro-trigger`
7. Click **"Set Shortcut"** and press `Ctrl+Shift+Space`
8. Click **"Add"**

### KDE Plasma (System Settings)

1. Open **System Settings** → **Shortcuts**
2. Click **"Custom Shortcuts"** in the left panel
3. Right-click in the empty area → **"New"** → **"Global Shortcut"** → **"Command/URL"**
4. In the **"Trigger"** tab, click the button and press `Ctrl+Shift+Space`
5. In the **"Action"** tab, enter: `sussurro-trigger`
6. Click **"Apply"**

### Sway (i3-like Wayland compositor)

Add to your `~/.config/sway/config`:

```
bindsym Ctrl+Shift+Space exec sussurro-trigger
```

Then reload Sway: `swaymsg reload`

### Hyprland

Add to your `~/.config/hypr/hyprland.conf`:

```
bind = CTRL SHIFT, Space, exec, sussurro-trigger
```

Then reload: `hyprctl reload`

## How to Use

After setup, the workflow is simple:

1. **Press** `Ctrl+Shift+Space` → Recording starts
2. **Speak** your text
3. **Press** `Ctrl+Shift+Space` again → Recording stops and processes
4. Text appears in your active application

## Trigger commands

The socket accepts one command per connection and replies with a status line.
`toggle` is the default and what every existing binding sends, so no
configuration change is required.

| Command   | Effect                                                        | Mode      |
|-----------|---------------------------------------------------------------|-----------|
| `toggle`  | Start recording, or stop one already running                  | Any       |
| `press`   | Begin recording; in review mode, begin an edit instruction    | Any       |
| `release` | End the recording started by `press`                          | Any       |
| `cancel`  | Abandon the session and discard any held text                 | Review    |
| `deliver` | Insert the reviewed text                                      | Review    |
| `submit`  | Insert the reviewed text and press Enter                      | Review    |

`cancel`, `deliver`, and `submit` require review mode (`workflow.mode: review`
in the config); in immediate mode they are refused with an error rather than
silently ignored.

These three are the only way to complete a review session on any platform, so
bind a shortcut to at least `deliver` and `cancel` before enabling review
mode. The socket listens regardless of display server; only Wayland needs the
recording gestures routed through it as well, because it cannot grab keys.

One instance owns the socket at a time. A second instance refuses to start its
server rather than taking the socket over, which would leave both apparently
working while only one received commands.

The bundled script takes the command as its argument:

```bash
scripts/trigger.sh          # toggle, same as before
scripts/trigger.sh press
scripts/trigger.sh deliver
```

Compositors that can bind key press and release separately should bind `press`
and `release` for true push-to-talk. Those that only fire once per shortcut
should keep using `toggle`.

Replies are `RECORDING`, `STOPPED`, `IDLE`, `CANCELLED`, `DELIVERED`, or
`ERROR <reason>`. The script exits non-zero on an error reply.

## Troubleshooting

### "Connection refused" or socket errors

Make sure Sussurro is running before pressing the hotkey.

### No response when pressing hotkey

1. Check if the keyboard shortcut is properly configured in your DE
2. Test the command manually in a terminal:
   ```bash
   echo toggle | nc -U $XDG_RUNTIME_DIR/sussurro.sock
   ```
3. Check Sussurro logs for errors

### Want to use X11 instead?

Log out of your session and select an X11 session at the login screen. Sussurro will automatically use native global hotkeys on X11.
