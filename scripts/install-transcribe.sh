#!/usr/bin/env bash
# sussurro-transcribe installer (macOS + Linux)
# Usage: curl -fsSL https://raw.githubusercontent.com/aploide/sussurro/master/scripts/install-transcribe.sh | bash
#
# Windows is installed with scripts/install-transcribe.ps1 instead:
#   irm https://raw.githubusercontent.com/aploide/sussurro/master/scripts/install-transcribe.ps1 | iex
set -euo pipefail

REPO="aploide/sussurro"
BINARY="sussurro-transcribe"
INSTALL_DIR="" # resolved below

# Scratch dir for the download. Kept at global scope with a global trap: the
# EXIT trap fires after main() returns, so a `local` here would already be out
# of scope and `set -u` would abort the cleanup instead of running it.
TMP_WORKDIR=""
cleanup() { [ -n "${TMP_WORKDIR}" ] && rm -rf "${TMP_WORKDIR}"; }
trap cleanup EXIT

# Platform-arch combinations the release workflow actually publishes.
# Keep in sync with the build matrix in .github/workflows/release.yml.
SUPPORTED_TARGETS="linux-amd64 linux-arm64 macos-arm64"

# ── colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
RESET='\033[0m'

info() { printf "${CYAN}  →${RESET} %s\n" "$*"; }
success() { printf "${GREEN}  ✓${RESET} %s\n" "$*"; }
warn() { printf "${YELLOW}  ⚠${RESET} %s\n" "$*"; }
die() {
    printf "${RED}  ✗${RESET} %b\n" "$*" >&2
    exit 1
}
header() { printf "\n${BOLD}%s${RESET}\n" "$*"; }

# ── detect OS & arch ─────────────────────────────────────────────────────────
detect_platform() {
    local os arch

    case "$(uname -s)" in
    Darwin) os="macos" ;;
    Linux) os="linux" ;;
    MINGW* | MSYS* | CYGWIN*)
        die "This script installs the macOS and Linux builds.\n    On Windows run instead:\n      irm https://raw.githubusercontent.com/${REPO}/master/scripts/install-transcribe.ps1 | iex"
        ;;
    *) die "Unsupported OS: $(uname -s). Only macOS, Linux, and Windows (via install-transcribe.ps1) are supported." ;;
    esac

    case "$(uname -m)" in
    arm64 | aarch64) arch="arm64" ;;
    x86_64 | amd64) arch="amd64" ;;
    *) die "Unsupported architecture: $(uname -m)." ;;
    esac

    echo "${os}-${arch}"
}

# ── refuse targets the release workflow does not build ───────────────────────
check_target_published() {
    local platform="$1" target
    for target in ${SUPPORTED_TARGETS}; do
        [ "${target}" = "${platform}" ] && return 0
    done

    if [ "${platform}" = "macos-amd64" ]; then
        die "No prebuilt binary for Intel Macs (macos-amd64).\n    Releases ship Apple Silicon (macos-arm64) only.\n    Build from source: https://github.com/${REPO}/blob/master/docs/compilation.md"
    fi
    die "No prebuilt binary for '${platform}'.\n    Published targets: ${SUPPORTED_TARGETS}\n    Build from source: https://github.com/${REPO}/blob/master/docs/compilation.md"
}

# ── check for ffmpeg ──────────────────────────────────────────────────────────
check_ffmpeg() {
    if command -v ffmpeg &>/dev/null; then
        success "ffmpeg found: $(ffmpeg -version 2>&1 | head -1 | cut -d' ' -f1-3)"
    else
        warn "ffmpeg not found — sussurro-transcribe requires it to decode audio files."
        printf "\n  Install ffmpeg:\n"
        case "$(uname -s)" in
        Linux)
            printf "    Arch/Manjaro:   sudo pacman -S ffmpeg\n"
            printf "    Ubuntu/Debian:  sudo apt install ffmpeg\n"
            printf "    Fedora:         sudo dnf install ffmpeg\n"
            ;;
        Darwin)
            printf "    macOS:          brew install ffmpeg\n"
            ;;
        esac
        printf "\n"
        warn "Continuing install — please install ffmpeg before using sussurro-transcribe."
    fi
}

# ── pick install dir ──────────────────────────────────────────────────────────
pick_install_dir() {
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
        case "${SHELL:-}" in
        */zsh) shell_rc="$HOME/.zshrc" ;;
        */bash) shell_rc="$HOME/.bashrc" ;;
        *) shell_rc="$HOME/.profile" ;;
        esac
        printf '\n# Sussurro companion tools\nexport PATH="%s:$PATH"\n' "$dir" >>"$shell_rc"
        info "Added $dir to PATH in $shell_rc"
        warn "Run: source $shell_rc  (or open a new terminal) before using sussurro-transcribe"
    fi
}

# ── resolve latest version from GitHub ───────────────────────────────────────
fetch_latest_version() {
    local tag
    if command -v curl &>/dev/null; then
        tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
            grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    elif command -v wget &>/dev/null; then
        tag=$(wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" |
            grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    else
        die "Neither curl nor wget found. Please install one and retry."
    fi
    [ -n "$tag" ] || die "Could not determine latest release. Check your internet connection."
    echo "$tag"
}

# ── download helpers ──────────────────────────────────────────────────────────
download() {
    local url="$1" dest="$2"
    if command -v curl &>/dev/null; then
        curl -fL --progress-bar "$url" -o "$dest"
    else
        wget -q --show-progress "$url" -O "$dest"
    fi
}

download_quiet() {
    local url="$1" dest="$2"
    if command -v curl &>/dev/null; then
        curl -fsSL "$url" -o "$dest"
    else
        wget -qO "$dest" "$url"
    fi
}

# ── verify the archive against the published .sha256 ─────────────────────────
verify_checksum() {
    local archive="$1" checksum_url="$2" expected actual

    if ! download_quiet "$checksum_url" "${archive}.sha256" 2>/dev/null; then
        warn "Checksum file not published for this release — skipping verification."
        return 0
    fi

    expected=$(awk '{print $1}' "${archive}.sha256")
    if [ -z "$expected" ]; then
        warn "Checksum file is empty — skipping verification."
        return 0
    fi

    if command -v sha256sum &>/dev/null; then
        actual=$(sha256sum "$archive" | awk '{print $1}')
    elif command -v shasum &>/dev/null; then
        actual=$(shasum -a 256 "$archive" | awk '{print $1}')
    else
        warn "Neither sha256sum nor shasum found — skipping verification."
        return 0
    fi

    [ "$expected" = "$actual" ] ||
        die "Checksum mismatch — refusing to install.\n    expected: ${expected}\n    actual:   ${actual}"
    success "Checksum verified"
}

# ── check whether Sussurro config exists ─────────────────────────────────────
check_sussurro_config() {
    local cfg="$HOME/.sussurro/config.yaml"
    if [ -f "$cfg" ]; then
        success "Sussurro config found: $cfg"
    else
        warn "~/.sussurro/config.yaml not found."
        warn "sussurro-transcribe shares models with the main Sussurro app."
        warn "Run 'sussurro' at least once to download models and generate config,"
        warn "or point to a custom config with:  sussurro-transcribe -config <path>"
    fi
}

# ── main ──────────────────────────────────────────────────────────────────────
main() {
    header "sussurro-transcribe installer"

    # 1. Platform
    local platform
    platform=$(detect_platform)
    info "Detected platform: ${platform}"
    check_target_published "${platform}"

    # 2. Check runtime dependencies
    check_ffmpeg

    # 3. Latest version
    info "Fetching latest release..."
    local version
    version=$(fetch_latest_version)
    info "Latest version: ${version}"

    # 4. Build download URL
    #    The dedicated transcribe archive: sussurro-transcribe-linux-amd64.tar.gz
    local archive_base="${BINARY}-${platform}"
    local archive_name="${archive_base}.tar.gz"
    local download_url="https://github.com/${REPO}/releases/download/${version}/${archive_name}"

    # 5. Download to a temp dir (removed by the global EXIT trap)
    local tmpdir
    TMP_WORKDIR=$(mktemp -d)
    tmpdir="${TMP_WORKDIR}"

    info "Downloading ${archive_name}..."
    download "$download_url" "${tmpdir}/${archive_name}" ||
        die "Download failed. Make sure a release for '${platform}' exists at:\n    ${download_url}"

    # 6. Verify download
    local sz
    sz=$(wc -c <"${tmpdir}/${archive_name}")
    [ "$sz" -gt 1024 ] || die "Downloaded file looks corrupt (only ${sz} bytes)."

    verify_checksum "${tmpdir}/${archive_name}" "${download_url}.sha256"

    # 7. Extract
    info "Extracting..."
    tar -xzf "${tmpdir}/${archive_name}" -C "$tmpdir"

    # Archive layout includes the CLI and its sibling LLM helper.
    local extracted_binary="${tmpdir}/${archive_base}/${BINARY}"
    local extracted_helper="${tmpdir}/${archive_base}/sussurro-llm-helper"
    [ -f "$extracted_binary" ] && [ -f "$extracted_helper" ] ||
        die "Required binaries not found in archive. Expected: ${archive_base}/{${BINARY},sussurro-llm-helper}"

    # 8. Install
    INSTALL_DIR=$(pick_install_dir)
    local dest="${INSTALL_DIR}/${BINARY}"

    info "Installing to ${dest}..."
    if [ "$INSTALL_DIR" = "/usr/local/bin" ] && [ ! -w "/usr/local/bin" ]; then
        sudo install -m 755 "$extracted_binary" "$dest"
        sudo install -m 755 "$extracted_helper" "${INSTALL_DIR}/sussurro-llm-helper"
    else
        install -m 755 "$extracted_binary" "$dest"
        install -m 755 "$extracted_helper" "${INSTALL_DIR}/sussurro-llm-helper"
    fi

    # 9. macOS: strip quarantine
    if [[ "$platform" == macos-* ]]; then
        info "Removing macOS quarantine flag..."
        xattr -d com.apple.quarantine "$dest" "${INSTALL_DIR}/sussurro-llm-helper" 2>/dev/null || true
    fi

    # 10. PATH check
    ensure_in_path "$INSTALL_DIR"

    # 11. Sussurro config check
    check_sussurro_config

    # 12. Done!
    success "sussurro-transcribe ${version} installed successfully!"
    printf "\n${BOLD}Usage${RESET}\n"
    printf "  Basic:      ${CYAN}sussurro-transcribe -i audio.mp3${RESET}\n"
    printf "  With LLM:   ${CYAN}sussurro-transcribe -i audio.wav -clean${RESET}\n"
    printf "  To file:    ${CYAN}sussurro-transcribe -i audio.mp3 -o out.txt${RESET}\n"
    printf "\n  Full docs:  https://github.com/${REPO}/blob/master/docs/transcribe.md\n\n"
}

main "$@"
