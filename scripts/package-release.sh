#!/bin/bash
# Package Sussurro for release.
# All three arguments are optional — they are auto-detected when omitted.
# Usage: ./scripts/package-release.sh [version] [platform] [arch]
# Example (explicit): ./scripts/package-release.sh 1.7 linux amd64
# Example (auto):     ./scripts/package-release.sh

set -e

# ── Auto-detection ─────────────────────────────────────────────────────────────

# Version: extracted from internal/version/version.go
DETECTED_VERSION=$(grep 'Version = ' internal/version/version.go 2>/dev/null \
    | sed 's/.*"\(.*\)"/\1/' | tr -d '[:space:]') || DETECTED_VERSION="unknown"

# Platform: uname -s lowercased (darwin / linux)
DETECTED_PLATFORM=$(uname -s | tr '[:upper:]' '[:lower:]')

# Arch: normalise uname -m to Go-style names (amd64 / arm64)
DETECTED_RAW_ARCH=$(uname -m)
case "${DETECTED_RAW_ARCH}" in
    x86_64)        DETECTED_ARCH="amd64"  ;;
    aarch64|arm64) DETECTED_ARCH="arm64"  ;;
    *)             DETECTED_ARCH="${DETECTED_RAW_ARCH}" ;;
esac

VERSION=${1:-"${DETECTED_VERSION}"}
PLATFORM=${2:-"${DETECTED_PLATFORM}"}
ARCH=${3:-"${DETECTED_ARCH}"}

# Remap darwin → macos for release naming
if [[ "${PLATFORM}" == "darwin" ]]; then
    PLATFORM="macos"
fi

# MSYS2 / Git Bash report MINGW64_NT-* / MSYS_NT-* → windows
case "${PLATFORM}" in
    mingw*|msys*|cygwin*) PLATFORM="windows" ;;
esac

# Binary name differs on Windows
BIN_NAME="sussurro"
HELPER_NAME="sussurro-llm-helper"
if [[ "${PLATFORM}" == "windows" ]]; then
    BIN_NAME="sussurro.exe"
    HELPER_NAME="sussurro-llm-helper.exe"
fi

# ── Setup ──────────────────────────────────────────────────────────────────────

RELEASE_NAME="sussurro-${PLATFORM}-${ARCH}"
RELEASE_DIR="release/${RELEASE_NAME}"

echo "Packaging Sussurro v${VERSION} for ${PLATFORM}-${ARCH}..."

# Clean and create release directory
rm -rf release
mkdir -p "${RELEASE_DIR}"

# Check if binary exists
if [ ! -f "bin/${BIN_NAME}" ] || [ ! -f "bin/${HELPER_NAME}" ]; then
    echo "Error: bin/${BIN_NAME} or bin/${HELPER_NAME} not found. Run 'make build' first."
    exit 1
fi

# ── Files ──────────────────────────────────────────────────────────────────────

echo "Copying binary..."
cp "bin/${BIN_NAME}" "${RELEASE_DIR}/${BIN_NAME}"
cp "bin/${HELPER_NAME}" "${RELEASE_DIR}/${HELPER_NAME}"
chmod +x "${RELEASE_DIR}/${BIN_NAME}" "${RELEASE_DIR}/${HELPER_NAME}"

# trigger.sh is a Wayland/X11 helper — only relevant on Linux
if [[ "${PLATFORM}" == "linux" ]]; then
    echo "Copying trigger.sh..."
    cp scripts/trigger.sh "${RELEASE_DIR}/trigger.sh"
    chmod +x "${RELEASE_DIR}/trigger.sh"
fi

echo "Copying example config..."
cp configs/default.yaml "${RELEASE_DIR}/config.example.yaml"

# ── INSTALL.txt ────────────────────────────────────────────────────────────────

{
    echo "Sussurro v${VERSION} Installation"
    echo "================================"
    echo ""
    echo "Quick Start:"
    if [[ "${PLATFORM}" == "macos" ]]; then
        echo "1. Make the binaries executable: chmod +x sussurro sussurro-llm-helper"
        echo "2. Remove macOS quarantine:      xattr -d com.apple.quarantine sussurro sussurro-llm-helper"
        echo "3. Run:                         ./sussurro"
    elif [[ "${PLATFORM}" == "windows" ]]; then
        echo "1. Run sussurro.exe (double-click, or from a terminal for setup prompts)"
        echo "2. Hold Ctrl+Shift+Space to talk, release to transcribe."
        echo ""
        echo "Requirements: Windows 10/11 with the WebView2 runtime (preinstalled"
        echo "on Windows 11). GPU acceleration uses Vulkan via your graphics driver."
    else
        echo "1. Make the binaries executable: chmod +x sussurro sussurro-llm-helper trigger.sh"
        echo "2. Run:                         ./sussurro"
    fi
    echo "   Follow the prompts to download AI models."
    echo ""
    if [[ "${PLATFORM}" == "linux" ]]; then
        echo "For Wayland Users:"
        echo "-----------------"
        echo "If you're on Wayland (check with: echo \$XDG_SESSION_TYPE):"
        echo ""
        echo "1. Make sure you have wl-clipboard installed:"
        echo "   Arch:   sudo pacman -S wl-clipboard"
        echo "   Ubuntu: sudo apt install wl-clipboard"
        echo ""
        echo "2. Set up a keyboard shortcut in your desktop environment:"
        echo "   - Open keyboard settings"
        echo "   - Add custom shortcut: Ctrl+Shift+Space"
        echo "   - Command: /full/path/to/trigger.sh"
        echo "   - See full guide: https://github.com/aploide/sussurro/blob/master/docs/wayland.md"
        echo ""
        echo "For X11 Users:"
        echo "-------------"
        echo "Just run ./sussurro — hotkeys work automatically!"
        echo "Hold Ctrl+Shift+Space to talk, release to transcribe."
        echo ""
    fi
    echo "Documentation:"
    echo "-------------"
    echo "Full docs:       https://github.com/aploide/sussurro"
    echo "Quick Start:     https://github.com/aploide/sussurro/blob/master/docs/quickstart.md"
} > "${RELEASE_DIR}/INSTALL.txt"

# ── Tarball + checksum ─────────────────────────────────────────────────────────

if [[ "${PLATFORM}" == "windows" ]]; then
    # Windows users expect a zip (Explorer extracts it natively).
    ARCHIVE="${RELEASE_NAME}.zip"
    echo "Creating zip..."
    cd release
    if command -v zip &> /dev/null; then
        zip -qr "${ARCHIVE}" "${RELEASE_NAME}/"
    else
        bsdtar -a -cf "${ARCHIVE}" "${RELEASE_NAME}/"
    fi
    cd ..
else
    ARCHIVE="${RELEASE_NAME}.tar.gz"
    echo "Creating tarball..."
    cd release
    tar -czf "${ARCHIVE}" "${RELEASE_NAME}/"
    cd ..
fi

echo "Generating checksum..."
cd release
if command -v sha256sum &> /dev/null; then
    sha256sum "${ARCHIVE}" > "${ARCHIVE}.sha256"
elif command -v shasum &> /dev/null; then
    shasum -a 256 "${ARCHIVE}" > "${ARCHIVE}.sha256"
else
    echo "Warning: sha256sum or shasum not found. Skipping checksum generation."
fi
cd ..

# ── Summary ────────────────────────────────────────────────────────────────────

echo ""
echo "Release package created successfully!"
echo ""
echo "Package : release/${ARCHIVE}"
echo "SHA256  : release/${ARCHIVE}.sha256"
echo ""
echo "Contents:"
ls -lh "release/${RELEASE_NAME}/"
echo ""
echo "Upload these files to GitHub Releases:"
echo "  - release/${ARCHIVE}"
echo "  - release/${ARCHIVE}.sha256"
