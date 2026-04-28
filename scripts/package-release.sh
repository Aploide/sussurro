#!/bin/bash
# Package Sussurro for release.
# All three arguments are optional — they are auto-detected when omitted.
# Usage: ./scripts/package-release.sh [version] [platform] [arch]
# Example (explicit): ./scripts/package-release.sh 2.3 linux amd64
# Example (auto):     ./scripts/package-release.sh
#
# Output (release/<release-name>/):
#   macOS:
#     sussurro                – CLI binary (also placed inside the .app)
#     Sussurro.app/           – proper application bundle
#     config.example.yaml
#     INSTALL.txt
#   Linux:
#     sussurro                – CLI binary
#     trigger.sh              – Wayland helper
#     desktop/sussurro.desktop
#     desktop/icons/hicolor/<size>/apps/sussurro.png  – hicolor icon set
#     config.example.yaml
#     INSTALL.txt

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

RELEASE_NAME="sussurro-${PLATFORM}-${ARCH}"
RELEASE_DIR="release/${RELEASE_NAME}"

echo "Packaging Sussurro v${VERSION} for ${PLATFORM}-${ARCH}..."

# Clean and create release directory (preserve any sussurro-transcribe artefacts)
rm -rf "${RELEASE_DIR}" "release/${RELEASE_NAME}.tar.gz" "release/${RELEASE_NAME}.tar.gz.sha256"
mkdir -p "${RELEASE_DIR}"

# Check if binary exists
if [ ! -f "bin/sussurro" ]; then
    echo "Error: bin/sussurro not found. Run 'make build' first."
    exit 1
fi

# ── Generate icons (idempotent) ────────────────────────────────────────────────

# scripts/generate-icons.sh writes to release/icons/.  We always regenerate so
# release artefacts stay in sync with the latest internal/ui/assets/Logo.jpeg.
echo "Generating icon set..."
./scripts/generate-icons.sh

# ── Common files ───────────────────────────────────────────────────────────────

echo "Copying binary..."
cp bin/sussurro "${RELEASE_DIR}/sussurro"
chmod +x "${RELEASE_DIR}/sussurro"

echo "Copying example config..."
cp configs/default.yaml "${RELEASE_DIR}/config.example.yaml"

# ── macOS: build Sussurro.app bundle ───────────────────────────────────────────

if [[ "${PLATFORM}" == "macos" ]]; then
    APP_BUNDLE="${RELEASE_DIR}/Sussurro.app"
    echo "Building Sussurro.app bundle..."
    mkdir -p "${APP_BUNDLE}/Contents/MacOS"
    mkdir -p "${APP_BUNDLE}/Contents/Resources"

    # Binary inside the bundle
    cp bin/sussurro "${APP_BUNDLE}/Contents/MacOS/sussurro"
    chmod +x "${APP_BUNDLE}/Contents/MacOS/sussurro"

    # Icon
    if [ -f "release/icons/Sussurro.icns" ]; then
        cp "release/icons/Sussurro.icns" "${APP_BUNDLE}/Contents/Resources/Sussurro.icns"
    else
        echo "  Warning: release/icons/Sussurro.icns missing — bundle will have no icon."
    fi

    # Info.plist (substitute version placeholder)
    sed "s/__VERSION__/${VERSION}/g" release-templates/Info.plist \
        > "${APP_BUNDLE}/Contents/Info.plist"

    # PkgInfo (legacy but expected by some macOS APIs)
    printf "APPL????" > "${APP_BUNDLE}/Contents/PkgInfo"

    # Touch the bundle so Finder picks up the icon refresh
    touch "${APP_BUNDLE}"

    echo "  ✓ ${APP_BUNDLE}"
fi

# ── Linux: desktop entry + hicolor icons + trigger.sh ──────────────────────────

if [[ "${PLATFORM}" == "linux" ]]; then
    echo "Copying trigger.sh..."
    cp scripts/trigger.sh "${RELEASE_DIR}/trigger.sh"
    chmod +x "${RELEASE_DIR}/trigger.sh"

    DESKTOP_DIR="${RELEASE_DIR}/desktop"
    mkdir -p "${DESKTOP_DIR}"

    echo "Copying .desktop entry..."
    cp release-templates/sussurro.desktop "${DESKTOP_DIR}/sussurro.desktop"

    if [ -d "release/icons/hicolor" ]; then
        echo "Copying hicolor icon set..."
        cp -R release/icons/hicolor "${DESKTOP_DIR}/icons-hicolor"
    else
        echo "  Warning: release/icons/hicolor missing — install will fall back to no icon."
    fi
fi

# ── INSTALL.txt ────────────────────────────────────────────────────────────────

{
    echo "Sussurro v${VERSION} Installation"
    echo "================================"
    echo ""
    if [[ "${PLATFORM}" == "macos" ]]; then
        echo "Recommended:"
        echo "  Drag Sussurro.app into /Applications, then launch it from"
        echo "  Launchpad or Spotlight. The first launch will guide you"
        echo "  through downloading the AI models."
        echo ""
        echo "  If macOS blocks the app on first launch, run:"
        echo "    xattr -dr com.apple.quarantine /Applications/Sussurro.app"
        echo ""
        echo "CLI users (optional):"
        echo "  The same binary lives at:"
        echo "    Sussurro.app/Contents/MacOS/sussurro"
        echo "  Symlink it onto your PATH if you want the 'sussurro' command,"
        echo "  e.g.:"
        echo "    ln -s /Applications/Sussurro.app/Contents/MacOS/sussurro \\"
        echo "        /usr/local/bin/sussurro"
        echo ""
        echo "  Or use the standalone 'sussurro' binary in this archive."
    else
        echo "Quick start:"
        echo "  1. chmod +x sussurro trigger.sh"
        echo "  2. Install the binary: sudo cp sussurro /usr/local/bin/"
        echo "  3. Install the .desktop entry & icons:"
        echo "       sudo cp desktop/sussurro.desktop /usr/share/applications/"
        echo "       sudo cp -R desktop/icons-hicolor/* /usr/share/icons/hicolor/"
        echo "       sudo update-desktop-database"
        echo "       sudo gtk-update-icon-cache /usr/share/icons/hicolor"
        echo "  4. Launch Sussurro from your application menu."
        echo ""
        echo "Or use the official installer:"
        echo "  curl -fsSL https://raw.githubusercontent.com/cesp99/sussurro/master/scripts/install.sh | bash"
        echo ""
        echo "For Wayland Users:"
        echo "-----------------"
        echo "If you're on Wayland (check with: echo \$XDG_SESSION_TYPE):"
        echo ""
        echo "1. Install wl-clipboard:"
        echo "   Arch:   sudo pacman -S wl-clipboard"
        echo "   Ubuntu: sudo apt install wl-clipboard"
        echo ""
        echo "2. Bind a keyboard shortcut to /full/path/to/trigger.sh"
        echo "   See: https://github.com/cesp99/sussurro/blob/master/docs/wayland.md"
        echo ""
        echo "For X11 Users:"
        echo "-------------"
        echo "Just run sussurro — hotkeys work automatically!"
        echo "Hold Ctrl+Shift+Space to talk, release to transcribe."
    fi
    echo ""
    echo "Documentation:"
    echo "-------------"
    echo "Full docs:       https://github.com/cesp99/sussurro"
    echo "Quick Start:     https://github.com/cesp99/sussurro/blob/master/docs/quickstart.md"
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
