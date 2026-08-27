#!/bin/bash
# Package sussurro-transcribe for release.
# All three arguments are optional — they are auto-detected when omitted.
# Usage: ./scripts/package-transcribe.sh [version] [platform] [arch]
# Example (explicit): ./scripts/package-transcribe.sh 2.2 linux amd64
# Example (auto):     ./scripts/package-transcribe.sh

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
BIN_NAME="sussurro-transcribe"
HELPER_NAME="sussurro-llm-helper"
if [[ "${PLATFORM}" == "windows" ]]; then
    BIN_NAME="sussurro-transcribe.exe"
    HELPER_NAME="sussurro-llm-helper.exe"
fi

# ── Setup ──────────────────────────────────────────────────────────────────────

RELEASE_NAME="sussurro-transcribe-${PLATFORM}-${ARCH}"
RELEASE_DIR="release/${RELEASE_NAME}"

echo "Packaging sussurro-transcribe v${VERSION} for ${PLATFORM}-${ARCH}..."

# Clean and create release directory
rm -rf release
mkdir -p "${RELEASE_DIR}"

# Check if binary exists
if [ ! -f "bin/${BIN_NAME}" ] || [ ! -f "bin/${HELPER_NAME}" ]; then
    echo "Error: bin/${BIN_NAME} or bin/${HELPER_NAME} not found. Run 'make build-transcribe' first."
    exit 1
fi

# ── Files ──────────────────────────────────────────────────────────────────────

echo "Copying binary..."
cp "bin/${BIN_NAME}" "${RELEASE_DIR}/${BIN_NAME}"
cp "bin/${HELPER_NAME}" "${RELEASE_DIR}/${HELPER_NAME}"
chmod +x "${RELEASE_DIR}/${BIN_NAME}" "${RELEASE_DIR}/${HELPER_NAME}"

echo "Copying example config..."
cp configs/default.yaml "${RELEASE_DIR}/config.example.yaml"

# ── INSTALL.txt ────────────────────────────────────────────────────────────────

{
    echo "sussurro-transcribe v${VERSION} Installation"
    echo "============================================="
    echo ""
    echo "Quick Start:"
    if [[ "${PLATFORM}" == "macos" ]]; then
        echo "1. Make the binaries executable: chmod +x sussurro-transcribe sussurro-llm-helper"
        echo "2. Remove macOS quarantine:      xattr -d com.apple.quarantine sussurro-transcribe sussurro-llm-helper"
        echo "3. Run:                         ./sussurro-transcribe -i audio.mp3"
    elif [[ "${PLATFORM}" == "windows" ]]; then
        echo "1. Open a terminal in this folder"
        echo "2. Run:                         .\\sussurro-transcribe.exe -i audio.mp3"
    else
        echo "1. Make the binaries executable: chmod +x sussurro-transcribe sussurro-llm-helper"
        echo "2. Run:                         ./sussurro-transcribe -i audio.mp3"
    fi
    echo ""
    echo "Requirements:"
    echo "-------------"
    echo "- ffmpeg must be installed and available in PATH"
    if [[ "${PLATFORM}" == "linux" ]]; then
        echo "  Arch/Manjaro:   sudo pacman -S ffmpeg"
        echo "  Ubuntu/Debian:  sudo apt install ffmpeg"
        echo "  Fedora:         sudo dnf install ffmpeg"
    elif [[ "${PLATFORM}" == "windows" ]]; then
        echo "  Windows:        winget install Gyan.FFmpeg"
    else
        echo "  macOS:          brew install ffmpeg"
    fi
    echo ""
    if [[ "${PLATFORM}" == "windows" ]]; then
        echo "- AI models are shared with the main Sussurro app"
        echo "  (%USERPROFILE%\\.sussurro\\models\\). Run 'sussurro' at least once to"
        echo "  download them, or use -config to point to a config file with custom"
        echo "  model paths."
    else
        echo "- AI models are shared with the main Sussurro app (~/.sussurro/models/)."
        echo "  Run 'sussurro' at least once to download them, or use -config to point"
        echo "  to a config file with custom model paths."
    fi
    echo ""
    echo "Usage:"
    echo "------"
    echo "  Basic:      sussurro-transcribe -i audio.mp3"
    echo "  With LLM:   sussurro-transcribe -i audio.wav -clean"
    echo "  To file:    sussurro-transcribe -i audio.mp3 -o out.txt"
    echo "  Language:   sussurro-transcribe -i audio.mp3 -lang fr"
    echo ""
    echo "Documentation:"
    echo "--------------"
    echo "Full docs:  https://github.com/aploide/sussurro/blob/master/docs/transcribe.md"
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
