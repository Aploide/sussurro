# System Dependencies

This guide lists all system packages required to **run** and **build** Sussurro.

---

## Runtime Dependencies

### macOS

Nothing. Sussurro uses native macOS APIs (Cocoa, CoreGraphics, WKWebView, NSStatusItem).

---

### Linux — UI Mode (default)

The overlay, settings window, and system tray require the following libraries at runtime:

| Library | Purpose | Package name |
|---------|---------|-------------|
| GTK 3 | Overlay window, UI toolkit | `gtk3` / `libgtk-3-0` |
| WebKit2GTK | Settings window HTML renderer | `webkit2gtk-4.1` / `libwebkit2gtk-4.1-0` |
| gtk-layer-shell | True Wayland overlay (optional) | `gtk-layer-shell` / `libgtk-layer-shell0` |
| wl-clipboard | Clipboard on Wayland | `wl-clipboard` |

The system tray needs **no library**: it is spoken over DBus
(StatusNotifierItem) in pure Go, so neither `libappindicator-gtk3` nor
`libayatana-appindicator3-1` is required. What it does need is a desktop that
hosts SNI items — see [Tray icon missing](#tray-icon-missing) below.

#### Arch Linux / Manjaro
```bash
# Required
sudo pacman -S gtk3 webkit2gtk-4.1

# Wayland clipboard (required on Wayland)
sudo pacman -S wl-clipboard

# Recommended: true wlr-layer-shell overlay
sudo pacman -S gtk-layer-shell

# X11 optional helpers
sudo pacman -S xdotool xorg-xprop
```

#### Ubuntu / Debian (22.04+)
```bash
# Required
sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0

# Wayland clipboard
sudo apt install wl-clipboard

# Recommended
sudo apt install libgtk-layer-shell0

# X11 optional
sudo apt install xdotool x11-utils
```

#### Fedora (38+)
```bash
sudo dnf install gtk3 webkit2gtk4.1 wl-clipboard
```

#### openSUSE
```bash
sudo zypper install libgtk-3-0 libwebkit2gtk-4.1 wl-clipboard
```

---

### Linux — Headless / CLI Mode (`--no-ui`)

Only `wl-clipboard` is needed on Wayland. No GTK or WebKit required.

```bash
sudo pacman -S wl-clipboard   # Arch (Wayland only)
sudo apt install wl-clipboard  # Ubuntu/Debian (Wayland only)
```

### Linux — Review Workflow (optional)

The review workflow needs **no additional packages**. Everything below is
optional and enabled only if you configure it; the defaults use the same
clipboard-and-paste path as immediate mode.

| Package | Enables | Needed when |
|---------|---------|-------------|
| `wtype` | `workflow.delivery.backend: wtype` | Wayland compositors implementing the virtual keyboard protocol |
| `ydotool` | `workflow.delivery.backend: ydotool` | Direct typing under any compositor; also needs the `ydotoold` daemon running |
| — | `workflow.input.backend: evdev` | No package; requires membership of the `input` group |

```bash
# Arch / Manjaro
sudo pacman -S wtype ydotool

# Ubuntu / Debian
sudo apt install wtype ydotool

# Fedora
sudo dnf install wtype ydotool
```

**evdev input** reads Linux input devices directly, which requires group
membership rather than a package:

```bash
sudo usermod -aG input $USER   # then log out and back in
```

`workflow.input.backend: auto` never opens `/dev/input`, so an ordinary host
needs none of this. Selecting a backend whose tool is missing produces an
error naming what to install rather than silently falling back — **Settings →
Review workflow** shows the same information per option.

---

## Build Dependencies

These are required when compiling from source.

### All Platforms
- **Go 1.24+**
- **Make**
- **CMake 3.15+**
- **C/C++ compiler** (gcc/clang)
- **Git**

### Linux (`make build`)

All runtime libraries plus their `-dev` / header packages:

#### Arch Linux / Manjaro
```bash
sudo pacman -S gtk3 webkit2gtk-4.1 base-devel cmake git go

# Optional (adds wlr-layer-shell support to the overlay)
sudo pacman -S gtk-layer-shell
```

#### Ubuntu / Debian (22.04+)
```bash
sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev \
                 build-essential cmake git golang-go

# Optional
sudo apt install libgtk-layer-shell-dev
```

#### Fedora (38+)
```bash
sudo dnf install gtk3-devel webkit2gtk4.1-devel \
                 gcc gcc-c++ cmake git golang
```

> **Note for Arch users:** The `webview_go` dependency declares `webkit2gtk-4.0` in its CGO
> directives. `make build` automatically creates a compatibility shim so that
> `webkit2gtk-4.1` (the package available on Arch) is used instead. No manual steps needed.

### macOS
```bash
xcode-select --install   # Xcode Command Line Tools
# Go: https://go.dev/dl/
```

---

## Verifying Runtime Dependencies

```bash
# Check GTK3
pkg-config --exists gtk+-3.0 && echo "GTK3: OK" || echo "GTK3: MISSING"

# Check WebKit
pkg-config --exists webkit2gtk-4.1 && echo "WebKit 4.1: OK" || \
  pkg-config --exists webkit2gtk-4.0 && echo "WebKit 4.0: OK" || echo "WebKit: MISSING"

# Check layer-shell (optional)
pkg-config --exists gtk-layer-shell-0 && echo "Layer shell: OK" || echo "Layer shell: not installed (overlay will use fallback)"

# Check Wayland clipboard
which wl-copy && echo "wl-clipboard: OK" || echo "wl-clipboard: MISSING"
```

---

## Troubleshooting

### Settings window blank or doesn't open
WebKit2GTK is missing or the wrong version. Install `webkit2gtk-4.1` (or `webkit2gtk-4.0` on older distros).

If the window opens as an empty grey frame, it is WebKitGTK's DMABUF renderer,
which fails on some driver/compositor pairs — notably the NVIDIA proprietary
driver under Wayland, where it kills the GDK connection outright
(`Gdk-Message: Error 71 (Protocol error) dispatching to Wayland display`).
Sussurro sets `WEBKIT_DISABLE_DMABUF_RENDERER=1` for itself at startup, so this
should not happen; if you set that variable to `0` in your environment you are
forcing the accelerated path back on.

### Tray icon missing
Some desktop environments need an SNI proxy:
- GNOME: install the [AppIndicator extension](https://extensions.gnome.org/extension/615/appindicator-support/) or `snixembed`
- If no tray is available, **right-click the overlay capsule** to access Settings and Quit

### Overlay appears below other windows (X11 without layer-shell)
The overlay uses `_NET_WM_STATE_ABOVE` on X11. If a compositor ignores this, try installing `gtk-layer-shell` and rebuilding with `make build` (layer-shell takes priority on Wayland).

### "clipboard failed" on Wayland
Install `wl-clipboard`:
```bash
sudo pacman -S wl-clipboard   # Arch
sudo apt install wl-clipboard  # Ubuntu/Debian
```
