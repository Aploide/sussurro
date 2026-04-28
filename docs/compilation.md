# Compilation and Build Guide

## Prerequisites

To build Sussurro from source you need:

1. **Go 1.24+**
2. **C/C++ Compiler** — GCC or Clang
3. **CMake 3.15+**
4. **Make**
5. **Git**

### macOS
```bash
xcode-select --install   # Xcode Command Line Tools (provides clang, make, git)
# Install Go: https://go.dev/dl/
```

The macOS overlay uses **Cocoa**, **QuartzCore**, and **CoreVideo** — all part of Xcode Command Line Tools. No additional system packages are needed.

> **Accessibility permission:** After the first run, macOS will prompt you to grant Accessibility access (System Settings → Privacy & Security → Accessibility). This allows Sussurro to register a global hotkey via CGEventTap.

### Linux (Arch/Manjaro)
```bash
sudo pacman -S base-devel cmake git go
```

### Linux (Ubuntu/Debian)
```bash
sudo apt install build-essential cmake git golang-go
```

### Linux (Fedora)
```bash
sudo dnf install gcc gcc-c++ cmake git golang
```

---

## Build

Requires GTK3 and WebKit2GTK development headers.

### Step 1: Install build dependencies

#### Arch Linux / Manjaro
```bash
sudo pacman -S gtk3 webkit2gtk-4.1 base-devel cmake git go

# Optional: adds wlr-layer-shell overlay on Wayland
sudo pacman -S gtk-layer-shell
```

#### Ubuntu / Debian (22.04+)
```bash
sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev \
                 build-essential cmake git golang-go

# Optional: Wayland layer-shell overlay
sudo apt install libgtk-layer-shell-dev
```

#### Fedora (38+)
```bash
sudo dnf install gtk3-devel webkit2gtk4.1-devel \
                 gcc gcc-c++ cmake git golang
```

### Step 2: Build

```bash
make deps    # if not already done
make build   # produces bin/sussurro
```

### Step 3: Run

```bash
./bin/sussurro          # UI mode (overlay + settings)
./bin/sussurro --no-ui  # headless CLI mode
```

You can also launch the binary from the OS-level launcher built by `make app` (macOS → `Sussurro.app`) or `make package` (Linux → `.desktop` entry), without a terminal.

---

## How `make build` Works Internally

The build target handles several platform quirks automatically — you do not need to do anything manually.

### webkit2gtk-4.1 compatibility shim (Arch Linux)

`webview_go` (the settings window library) hardcodes `pkg-config: webkit2gtk-4.0` in its CGO directives. Arch Linux ships `webkit2gtk-4.1` only.

`make build` auto-creates `.build-compat/pkgconfig/webkit2gtk-4.0.pc` — a shim `.pc` file that redirects pkg-config queries for `webkit2gtk-4.0` to the installed `webkit2gtk-4.1`. It then sets `PKG_CONFIG_PATH` to include this directory for the duration of the build. No manual steps needed.

### Layer-shell detection

`make build` checks for `gtk-layer-shell` via `pkg-config`. If found, it compiles the overlay with true wlr-layer-shell support (proper Wayland overlay, always above all windows). If not found, the overlay falls back to a regular floating window with `_NET_WM_STATE_ABOVE` on X11.

---

## Building C/C++ Dependencies

Sussurro uses `whisper.cpp` (ASR) and `llama.cpp` (LLM) as statically linked libraries.

```bash
make deps
```

This command:
1. Clones `whisper.cpp` into `third_party/`
2. Clones `go-llama.cpp` into `third_party/`
3. Compiles static `.a` libraries (with Metal acceleration on macOS, CPU-optimized on Linux)

You only need to run `make deps` once (or after updating the submodules).

---

## First Run

On first run Sussurro creates `~/.sussurro/config.yaml` and prompts you to download the required AI models into `~/.sussurro/models/`.

The overlay capsule, settings window, and right-click context menu work on both **Linux** and **macOS** builds.

You can also place model files manually and update the paths in `~/.sussurro/config.yaml`.

---

## All Make Targets

| Target | Description |
|--------|-------------|
| `make deps` | Build whisper.cpp and llama.cpp into `third_party/` |
| `make build` | Build `bin/sussurro` (overlay + settings) |
| `make build-transcribe` | Build `bin/sussurro-transcribe` (no UI dependencies) |
| `make app` | macOS-only: build `bin/Sussurro.app` (overlay + settings, with Info.plist + icon) |
| `make icons` | Regenerate `release/icons/Sussurro.icns` and the hicolor PNG set from `internal/ui/assets/Logo.jpeg` |
| `make package` | Produce `release/sussurro-<os>-<arch>.tar.gz` (with `Sussurro.app` on macOS, `.desktop` + icons on Linux) |
| `make run` | Build and run |
| `make clean` | Remove `bin/`, `third_party/`, and `release/` build artefacts |

---

## Troubleshooting

### `pkg-config: webkit2gtk-4.0 not found`
You are on Arch and the compat shim wasn't created. Run `make build` (not `go build` directly) — it creates the shim automatically via the `compat-pc` target.

### `gtk-layer-shell: not found` (warning, not error)
The overlay will use a regular floating window. Install `gtk-layer-shell` (Arch: `sudo pacman -S gtk-layer-shell`, Ubuntu: `sudo apt install libgtk-layer-shell-dev`) and rebuild for true Wayland overlay.

### macOS: `xcode-select: error`
Run `xcode-select --install` and accept the license agreement.

### `fatal error: gtk/gtk.h: No such file or directory`
GTK3 development headers are missing. Install `libgtk-3-dev` (Ubuntu) or `gtk3` (Arch) and retry.
