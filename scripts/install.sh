#!/usr/bin/env bash
# Sussurro installer
# Usage: curl -fsSL https://raw.githubusercontent.com/cesp99/sussurro/master/scripts/install.sh | bash
set -euo pipefail

REPO="cesp99/sussurro"
BINARY="sussurro"
INSTALL_DIR=""   # resolved below

# ── colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

info()    { printf "${CYAN}  →${RESET} %s\n" "$*"; }
success() { printf "${GREEN}  ✓${RESET} %s\n" "$*"; }
warn()    { printf "${YELLOW}  ⚠${RESET} %s\n" "$*"; }
die()     { printf "${RED}  ✗${RESET} %s\n" "$*" >&2; exit 1; }
header()  { printf "\n${BOLD}%s${RESET}\n" "$*"; }

# ── detect OS & arch ─────────────────────────────────────────────────────────
detect_platform() {
    local os arch

    case "$(uname -s)" in
        Darwin) os="macos" ;;
        Linux)  os="linux" ;;
        *)      die "Unsupported OS: $(uname -s). Only macOS and Linux are supported." ;;
    esac

    case "$(uname -m)" in
        arm64|aarch64) arch="arm64" ;;
        x86_64|amd64)  arch="amd64" ;;
        *)             die "Unsupported architecture: $(uname -m)." ;;
    esac

    echo "${os}-${arch}"
}

# ── pick install dir ──────────────────────────────────────────────────────────
pick_install_dir() {
    # Prefer /usr/local/bin if writable, otherwise ~/.local/bin
    if [ -w "/usr/local/bin" ] || sudo -n true 2>/dev/null; then
        echo "/usr/local/bin"
    else
        local local_bin="$HOME/.local/bin"
        mkdir -p "$local_bin"
        echo "$local_bin"
    fi
}

# ── ensure PATH contains the install dir ─────────────────────────────────────
ensure_in_path() {
    local dir="$1"
    if [[ ":$PATH:" != *":$dir:"* ]]; then
        warn "$dir is not in your PATH."
        local shell_rc=""
        case "$SHELL" in
            */zsh)  shell_rc="$HOME/.zshrc" ;;
            */bash) shell_rc="$HOME/.bashrc" ;;
            *)      shell_rc="$HOME/.profile" ;;
        esac
        printf '\n# Sussurro\nexport PATH="%s:$PATH"\n' "$dir" >> "$shell_rc"
        info "Added $dir to PATH in $shell_rc"
        warn "Run: source $shell_rc  (or open a new terminal) before using sussurro"
    fi
}

# ── resolve latest version from GitHub ───────────────────────────────────────
fetch_latest_version() {
    local tag
    if command -v curl &>/dev/null; then
        tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
              | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    elif command -v wget &>/dev/null; then
        tag=$(wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" \
              | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    else
        die "Neither curl nor wget found. Please install one and retry."
    fi
    [ -n "$tag" ] || die "Could not determine latest release. Check your internet connection."
    echo "$tag"
}

# ── download helper ───────────────────────────────────────────────────────────
download() {
    local url="$1" dest="$2"
    if command -v curl &>/dev/null; then
        curl -fsSL --progress-bar "$url" -o "$dest"
    else
        wget -q --show-progress "$url" -O "$dest"
    fi
}

# ── main ──────────────────────────────────────────────────────────────────────
main() {
    header "Sussurro installer"

    # 1. Platform
    local platform
    platform=$(detect_platform)
    info "Detected platform: ${platform}"

    # 2. Latest version
    info "Fetching latest release..."
    local version
    version=$(fetch_latest_version)
    info "Latest version: ${version}"

    # 3. Build download URL
    #    archive name: sussurro-macos-arm64.tar.gz  (no version in filename)
    local archive_name="${BINARY}-${platform}.tar.gz"
    local download_url="https://github.com/${REPO}/releases/download/${version}/${archive_name}"

    # 4. Download to a temp dir
    local tmpdir
    tmpdir=$(mktemp -d)
    trap 'rm -rf "$tmpdir"' EXIT

    info "Downloading ${archive_name}..."
    download "$download_url" "${tmpdir}/${archive_name}" \
        || die "Download failed. Make sure a release for '${platform}' exists at:\n  ${download_url}"

    # 5. Verify the download isn't empty / is a valid tarball
    local sz
    sz=$(wc -c < "${tmpdir}/${archive_name}")
    [ "$sz" -gt 1024 ] || die "Downloaded file looks corrupt (only ${sz} bytes)."

    # 6. Extract
    info "Extracting..."
    tar -xzf "${tmpdir}/${archive_name}" -C "$tmpdir"

    # Binary lives inside: sussurro-macos-arm64/sussurro
    local extracted_binary="${tmpdir}/${BINARY}-${platform}/${BINARY}"
    [ -f "$extracted_binary" ] \
        || die "Binary not found in archive. Expected: ${BINARY}-${platform}/${BINARY}"

    # 7. Install
    INSTALL_DIR=$(pick_install_dir)
    local dest="${INSTALL_DIR}/${BINARY}"

    info "Installing to ${dest}..."
    if [ "$INSTALL_DIR" = "/usr/local/bin" ] && [ ! -w "/usr/local/bin" ]; then
        sudo install -m 755 "$extracted_binary" "$dest"
    else
        install -m 755 "$extracted_binary" "$dest"
    fi

    # 8. macOS: strip quarantine attribute so Gatekeeper doesn't block the binary
    if [[ "$platform" == macos-* ]]; then
        info "Removing macOS quarantine flag..."
        xattr -d com.apple.quarantine "$dest" 2>/dev/null || true
    fi

    # 9. PATH check
    ensure_in_path "$INSTALL_DIR"

    # 10. Done!
    success "Sussurro ${version} installed successfully!"
    printf "\n${BOLD}Usage${RESET}\n"
    printf "  Run Sussurro:        ${CYAN}sussurro${RESET}\n"
    printf "  Hold to dictate:     ${CYAN}Ctrl+Shift+Space${RESET}\n"
    printf "  Toggle dictation:    ${CYAN}Ctrl+Shift+Enter${RESET}\n"
    printf "  First run will guide you through model download automatically.\n\n"

    if [[ "$platform" == linux-* ]]; then
        printf "${YELLOW}Wayland users:${RESET} bind Ctrl+Shift+Space to the\n"
        printf "  trigger script (see docs/wayland.md) for hotkey support.\n\n"
    fi
}

main "$@"
