#!/usr/bin/env bash
# Sussurro installer (macOS + Linux)
# Usage: curl -fsSL https://raw.githubusercontent.com/aploide/sussurro/master/scripts/install.sh | bash
#
# Windows is installed with scripts/install.ps1 instead:
#   irm https://raw.githubusercontent.com/aploide/sussurro/master/scripts/install.ps1 | iex
set -euo pipefail

REPO="aploide/sussurro"
BINARY="sussurro"
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
        die "This script installs the macOS and Linux builds.\n    On Windows run instead:\n      irm https://raw.githubusercontent.com/${REPO}/master/scripts/install.ps1 | iex"
        ;;
    *) die "Unsupported OS: $(uname -s). Only macOS, Linux, and Windows (via install.ps1) are supported." ;;
    esac

    case "$(uname -m)" in
    arm64 | aarch64) arch="arm64" ;;
    x86_64 | amd64) arch="amd64" ;;
    *) die "Unsupported architecture: $(uname -m)." ;;
    esac

    echo "${os}-${arch}"
}

# ── refuse targets the release workflow does not build ───────────────────────
# Without this, an Intel Mac would build a URL for an asset that is never
# published and fail with an opaque 404 instead of an actionable message.
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
        case "${SHELL:-}" in
        */zsh) shell_rc="$HOME/.zshrc" ;;
        */bash) shell_rc="$HOME/.bashrc" ;;
        *) shell_rc="$HOME/.profile" ;;
        esac
        printf '\n# Sussurro\nexport PATH="%s:$PATH"\n' "$dir" >>"$shell_rc"
        info "Added $dir to PATH in $shell_rc"
        warn "Run: source $shell_rc  (or open a new terminal) before using sussurro"
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

# Quiet variant for small side files (checksums) where a progress bar is noise.
download_quiet() {
    local url="$1" dest="$2"
    if command -v curl &>/dev/null; then
        curl -fsSL "$url" -o "$dest"
    else
        wget -qO "$dest" "$url"
    fi
}

# ── verify the archive against the published .sha256 ─────────────────────────
# Every release asset is published alongside a `<asset>.sha256` produced by
# scripts/package-release.sh, so a mismatch means a corrupt or tampered download.
verify_checksum() {
    local archive="$1" checksum_url="$2" dir expected actual
    dir=$(dirname "$archive")

    if ! download_quiet "$checksum_url" "${archive}.sha256" 2>/dev/null; then
        warn "Checksum file not published for this release — skipping verification."
        return 0
    fi

    # The file is `<sha256>  <archive-name>` as emitted by sha256sum/shasum.
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

# ── main ──────────────────────────────────────────────────────────────────────
main() {
    header "Sussurro installer"

    # 1. Platform
    local platform
    platform=$(detect_platform)
    info "Detected platform: ${platform}"
    check_target_published "${platform}"

    # 2. Latest version
    info "Fetching latest release..."
    local version
    version=$(fetch_latest_version)
    info "Latest version: ${version}"

    # 3. Build download URL
    #    archive name: sussurro-macos-arm64.tar.gz  (no version in filename)
    local archive_name="${BINARY}-${platform}.tar.gz"
    local base_url="https://github.com/${REPO}/releases/download/${version}"
    local download_url="${base_url}/${archive_name}"

    # 4. Download to a temp dir (removed by the global EXIT trap)
    local tmpdir
    TMP_WORKDIR=$(mktemp -d)
    tmpdir="${TMP_WORKDIR}"

    info "Downloading ${archive_name}..."
    download "$download_url" "${tmpdir}/${archive_name}" ||
        die "Download failed. Make sure a release for '${platform}' exists at:\n    ${download_url}"

    # 5. Verify the download isn't empty / is a valid tarball
    local sz
    sz=$(wc -c <"${tmpdir}/${archive_name}")
    [ "$sz" -gt 1024 ] || die "Downloaded file looks corrupt (only ${sz} bytes)."

    verify_checksum "${tmpdir}/${archive_name}" "${download_url}.sha256"

    # 6. Extract
    info "Extracting..."
    tar -xzf "${tmpdir}/${archive_name}" -C "$tmpdir"

    # Archive layout: sussurro-macos-arm64/{sussurro,sussurro-llm-helper,...}
    local extracted_dir="${tmpdir}/${BINARY}-${platform}"
    local extracted_binary="${extracted_dir}/${BINARY}"
    local extracted_helper="${extracted_dir}/sussurro-llm-helper"
    [ -f "$extracted_binary" ] && [ -f "$extracted_helper" ] ||
        die "Required binaries not found in archive. Expected: ${BINARY}-${platform}/{${BINARY},sussurro-llm-helper}"

    # 7. Install
    INSTALL_DIR=$(pick_install_dir)
    local dest="${INSTALL_DIR}/${BINARY}"
    local use_sudo=""
    if [ "$INSTALL_DIR" = "/usr/local/bin" ] && [ ! -w "/usr/local/bin" ]; then
        use_sudo="sudo"
    fi

    info "Installing to ${dest}..."
    ${use_sudo} install -m 755 "$extracted_binary" "$dest"
    ${use_sudo} install -m 755 "$extracted_helper" "${INSTALL_DIR}/sussurro-llm-helper"

    # 7b. Linux archives ship the Wayland trigger script; install it next to the
    #     binary as `sussurro-trigger` so compositor shortcuts have a stable,
    #     on-PATH command to bind instead of a path inside a deleted temp dir.
    local trigger_dest=""
    if [ -f "${extracted_dir}/trigger.sh" ]; then
        trigger_dest="${INSTALL_DIR}/${BINARY}-trigger"
        info "Installing Wayland trigger to ${trigger_dest}..."
        ${use_sudo} install -m 755 "${extracted_dir}/trigger.sh" "$trigger_dest"
    fi

    # 8. macOS: strip quarantine attribute so Gatekeeper doesn't block the binary
    if [[ "$platform" == macos-* ]]; then
        info "Removing macOS quarantine flag..."
        xattr -d com.apple.quarantine "$dest" "${INSTALL_DIR}/sussurro-llm-helper" 2>/dev/null || true
    fi

    # 9. PATH check
    ensure_in_path "$INSTALL_DIR"

    # 10. Done!
    success "Sussurro ${version} installed successfully!"
    printf "\n${BOLD}Usage${RESET}\n"
    printf "  Run Sussurro:        ${CYAN}sussurro${RESET}\n"
    if [[ "$platform" == macos-* ]]; then
        printf "  Hold to dictate:     ${CYAN}Cmd+Shift+Space${RESET}\n"
    else
        printf "  Hold to dictate:     ${CYAN}Ctrl+Shift+Space${RESET}\n"
    fi
    printf "  First run will guide you through model download automatically.\n\n"

    if [[ "$platform" == macos-* ]]; then
        printf "${YELLOW}macOS users:${RESET} grant Accessibility access when prompted\n"
        printf "  (System Settings → Privacy & Security → Accessibility).\n\n"
    fi

    if [[ "$platform" == linux-* ]] && [ -n "$trigger_dest" ]; then
        printf "${YELLOW}Wayland users:${RESET} bind Ctrl+Shift+Space in your desktop\n"
        printf "  environment to: ${CYAN}%s${RESET}\n" "$trigger_dest"
        printf "  Full guide: https://github.com/${REPO}/blob/master/docs/wayland.md\n\n"
    fi
}

main "$@"
