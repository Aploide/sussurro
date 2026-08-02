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

> **Note:** Always run Sussurro from a terminal. Launching via a desktop icon or application menu is not yet supported — the overlay and tray will not work correctly outside a terminal session.

```bash
./bin/sussurro          # UI mode (overlay + tray + settings)
./bin/sussurro --no-ui  # headless CLI mode
```

---

## How `make build` Works Internally

The build target handles several platform quirks automatically — you do not need to do anything manually.

### webkit2gtk-4.1 compatibility shim (Arch Linux)

`webview_go` (the settings window library) hardcodes `pkg-config: webkit2gtk-4.0` in its CGO directives. Arch Linux ships `webkit2gtk-4.1` only.

`make build` auto-creates `.build-compat/pkgconfig/webkit2gtk-4.0.pc` — a shim `.pc` file that redirects pkg-config queries for `webkit2gtk-4.0` to the installed `webkit2gtk-4.1`. It then sets `PKG_CONFIG_PATH` to include this directory for the duration of the build. No manual steps needed.

### System tray

The tray uses `fyne.io/systray`, whose Linux backend speaks the DBus
StatusNotifierItem protocol in pure Go. Nothing links against
`libappindicator3` or `libayatana-appindicator3`, so there is no backend to
select and no AppIndicator development package to install — one binary runs on
every distro. Only the desktop-side SNI host matters at runtime (see
[dependencies.md](dependencies.md)).

### Layer-shell detection

`make build` checks for `gtk-layer-shell-0` via `pkg-config` (the module name is
suffixed; the *package* is called `gtk-layer-shell` / `libgtk-layer-shell-dev`).
If found, it compiles the overlay with true wlr-layer-shell support (proper
Wayland overlay, always above all windows). If not found, the overlay falls back
to a regular floating window with `_NET_WM_STATE_ABOVE` on X11. `make build`
prints `Layer shell : yes|no` so you can confirm which path was compiled in.

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

The overlay capsule, settings window, system tray, and right-click context menu work on both **Linux** and **macOS** builds.

You can also place model files manually and update the paths in `~/.sussurro/config.yaml`.

---

## All Make Targets

| Target | Description |
|--------|-------------|
| `make deps` | Build whisper.cpp and llama.cpp |
| `make build` | Build binary with overlay + settings + tray |
| `make build-transcribe` | Build `bin/sussurro-transcribe` (no UI dependencies) |
| `make run` | Build and run |
| `make clean` | Remove `bin/` |

---

## Versioning

The version is not stored in the source tree — the git tag is the source of
truth. `internal/version.Version` defaults to `dev`, and builds stamp the real
value in via `-ldflags`:

```bash
make build VERSION=2.4        # binary reports 2.4
make build                    # binary reports dev
```

Pushing a version tag runs `.github/workflows/release.yml`, which builds
Linux (amd64 + arm64), macOS (arm64) and Windows (amd64), stamps the tag into
every binary, and publishes a GitHub release with the archives and checksums:

```bash
git tag v2.4
git push origin v2.4
```

A tag with a pre-release suffix (`v2.4-rc1`) is published as a pre-release and
is not marked "latest", so the install scripts keep pointing at the last stable
release.

---

## Troubleshooting

### `pkg-config: webkit2gtk-4.0 not found`
You are on Arch and the compat shim wasn't created. Run `make build` (not `go build` directly) — it creates the shim automatically via the `compat-pc` target.

### `error while loading shared libraries: libayatana-appindicator3.so.1`
Releases up to and including v2.4 linked one of the two AppIndicator variants
and only start where that exact SONAME is installed. Later builds link neither.
Upgrade to a newer release, or rebuild from source.

### `gtk-layer-shell: not found` (warning, not error)
The overlay will use a regular floating window. Install `gtk-layer-shell` (Arch: `sudo pacman -S gtk-layer-shell`, Ubuntu: `sudo apt install libgtk-layer-shell-dev`) and rebuild for true Wayland overlay.

### macOS: `xcode-select: error`
Run `xcode-select --install` and accept the license agreement.

### `fatal error: gtk/gtk.h: No such file or directory`
GTK3 development headers are missing. Install `libgtk-3-dev` (Ubuntu) or `gtk3` (Arch) and retry.

---

## Windows

Windows builds use MSYS2's MINGW64 environment (CGO requires a MinGW
toolchain; MSVC is not supported) and enable the ggml **Vulkan** backend for
whisper.cpp, so transcription runs on any Vulkan-capable GPU through the
regular graphics driver — no CUDA toolkit needed.

### Prerequisites

1. **Go (windows/amd64)** ≥ 1.25 — https://go.dev/dl/ (the dependency tree
   requires 1.25; older toolchains auto-download it when `GOTOOLCHAIN=auto`,
   the default).
2. **MSYS2** — https://www.msys2.org. In the **MSYS2 MINGW64** shell:

```bash
pacman -S --needed make git \
  mingw-w64-x86_64-gcc mingw-w64-x86_64-make mingw-w64-x86_64-pkgconf \
  mingw-w64-x86_64-cmake mingw-w64-x86_64-ninja \
  mingw-w64-x86_64-vulkan-headers mingw-w64-x86_64-vulkan-loader \
  mingw-w64-x86_64-shaderc
```

Make sure `go` is reachable from the MINGW64 shell, e.g. add to `~/.bashrc`:

```bash
export PATH="$PATH:/c/Program Files/Go/bin"
```

### Build

From the **MSYS2 MINGW64** shell:

```bash
git -c core.autocrlf=false clone https://github.com/aploide/sussurro.git
cd sussurro
make build build-transcribe   # produces bin/sussurro.exe and bin/sussurro-transcribe.exe
```

Notes:

- Clone with `core.autocrlf=false` (or rely on the repo's `.gitattributes`) —
  CRLF line endings break the `scripts/*.sh` patch steps.
- `make deps` clones pinned revisions of whisper.cpp and go-llama.cpp, applies
  `scripts/patch-whisper.sh` (symbol renames) and
  `scripts/patch-llama-windows.sh` (MinGW fixes), and builds whisper.cpp with
  `-DWSP_GGML_VULKAN=ON`. The first build takes 10–20 minutes.
- The binaries are statically linked (no MinGW runtime DLLs) and run from any
  shell; only `vulkan-1.dll` (from the graphics driver) and the WebView2
  runtime (preinstalled on Windows 11) are needed at runtime.
- The LLM cleanup stage runs on CPU; Vulkan acceleration applies to Whisper.

### Windows Troubleshooting

**`RegisterHotKey failed`** — another app owns the trigger combination;
change `hotkey.trigger` in `%USERPROFILE%\.sussurro\config.yaml`.

**`have you installed the static version of the vulkan-1 library?`** — the
`mingw-w64-x86_64-vulkan-loader` package is missing.

**`glslc: command not found` during deps** — install
`mingw-w64-x86_64-shaderc`.

**Settings window never appears / blank** — verify the WebView2 Evergreen
runtime is installed (Windows 10).
