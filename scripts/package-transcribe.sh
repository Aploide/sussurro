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

# ── Setup ──────────────────────────────────────────────────────────────────────

RELEASE_NAME="sussurro-transcribe-${PLATFORM}-${ARCH}"
RELEASE_DIR="release/${RELEASE_NAME}"

echo "Packaging sussurro-transcribe v${VERSION} for ${PLATFORM}-${ARCH}..."

# Clean only this transcribe artefact set so we can be invoked alongside
# scripts/package-release.sh in the same job (CI does this) without losing
# the main app's tarball.
rm -rf "${RELEASE_DIR}" "release/${RELEASE_NAME}.tar.gz" "release/${RELEASE_NAME}.tar.gz.sha256"
mkdir -p "${RELEASE_DIR}"

# Check if binary exists
if [ ! -f "bin/sussurro-transcribe" ]; then
    echo "Error: bin/sussurro-transcribe not found. Run 'make build-transcribe' first."
    exit 1
fi

# ── Files ──────────────────────────────────────────────────────────────────────

echo "Copying binary..."
cp bin/sussurro-transcribe "${RELEASE_DIR}/sussurro-transcribe"
chmod +x "${RELEASE_DIR}/sussurro-transcribe"

echo "Copying example config..."
cp configs/default.yaml "${RELEASE_DIR}/config.example.yaml"

# ── INSTALL.txt ────────────────────────────────────────────────────────────────

{
    echo "sussurro-transcribe v${VERSION} Installation"
    echo "============================================="
    echo ""
    echo "Quick Start:"
    if [[ "${PLATFORM}" == "macos" ]]; then
        echo "1. Make the binary executable:  chmod +x sussurro-transcribe"
        echo "2. Remove macOS quarantine:     xattr -d com.apple.quarantine sussurro-transcribe"
        echo "3. Run:                         ./sussurro-transcribe -i audio.mp3"
    else
        echo "1. Make the binary executable:  chmod +x sussurro-transcribe"
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
    else
        echo "  macOS:          brew install ffmpeg"
    fi
    echo ""
    echo "- AI models are shared with the main Sussurro app (~/.sussurro/models/)."
    echo "  Run 'sussurro' at least once to download them, or use -config to point"
    echo "  to a config file with custom model paths."
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
    echo "Full docs:  https://github.com/cesp99/sussurro/blob/master/docs/transcribe.md"
} > "${RELEASE_DIR}/INSTALL.txt"

# ── Tarball + checksum ─────────────────────────────────────────────────────────

echo "Creating tarball..."
cd release
tar -czf "${RELEASE_NAME}.tar.gz" "${RELEASE_NAME}/"
cd ..

echo "Generating checksum..."
cd release
if command -v sha256sum &> /dev/null; then
    sha256sum "${RELEASE_NAME}.tar.gz" > "${RELEASE_NAME}.tar.gz.sha256"
elif command -v shasum &> /dev/null; then
    shasum -a 256 "${RELEASE_NAME}.tar.gz" > "${RELEASE_NAME}.tar.gz.sha256"
else
    echo "Warning: sha256sum or shasum not found. Skipping checksum generation."
fi
cd ..

# ── Summary ────────────────────────────────────────────────────────────────────

echo ""
echo "Release package created successfully!"
echo ""
echo "Package : release/${RELEASE_NAME}.tar.gz"
echo "SHA256  : release/${RELEASE_NAME}.tar.gz.sha256"
echo ""
echo "Contents:"
ls -lh "release/${RELEASE_NAME}/"
echo ""
echo "Upload these files to GitHub Releases:"
echo "  - release/${RELEASE_NAME}.tar.gz"
echo "  - release/${RELEASE_NAME}.tar.gz.sha256"
