#!/usr/bin/env bash
# Sussurro installer — installs the application natively (.app on macOS,
# .desktop entry on Linux) and keeps the `sussurro` command on PATH.
#
# Usage: curl -fsSL https://raw.githubusercontent.com/cesp99/sussurro/master/scripts/install.sh | bash
set -euo pipefail

REPO="cesp99/sussurro"
BINARY="sussurro"

# ── colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

info()    { printf "${CYAN}  →${RESET} %s\n" "$*"; }
success() { printf "${GREEN}  ✓${RESET} %s\n" "$*"; }
warn()    { printf "${YELLOW}  ⚠${RESET} %s\n" "$*"; }
die()     { printf "${RED}  ✗${RESET} %s\n" "$*" >&2; exit 1; }
header()  { printf "\n${BOLD}%s${RESET}\n" "$*"; }

# Read user input even when this script is piped through `bash` (curl|bash).
# stdin is the curl pipe in that case, so we read from /dev/tty instead.
prompt() {
    local prompt_text="$1" reply
    if [ -r /dev/tty ]; then
        # shellcheck disable=SC2229
        read -r -p "$prompt_text" reply </dev/tty || reply=""
    else
        read -r -p "$prompt_text" reply || reply=""
    fi
    printf "%s" "$reply"
}

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

# ── pre-download AI models so first launch works without a terminal ──────────
# Replicates internal/setup/setup.go:
#   ASR  ~488MB or ~1.62GB → ~/.sussurro/models/ggml-(small|large-v3-turbo).bin
#   LLM  ~1.28GB           → ~/.sussurro/models/qwen3-sussurro-q4_k_m.gguf
#   config.yaml            → ~/.sussurro/config.yaml
URL_ASR_SMALL="https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin"
URL_ASR_LARGE="https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo.bin"
URL_LLM="https://huggingface.co/cesp99/qwen3-sussurro/resolve/main/qwen3-sussurro-q4_k_m.gguf"

write_default_config() {
    local cfg_path="$1" asr_path="$2" llm_path="$3"
    cat > "$cfg_path" <<EOF
app:
  name: "Sussurro"
  debug: false
  log_level: "info"

audio:
  sample_rate: 16000
  channels: 1
  bit_depth: 16
  buffer_size: 1024
  max_duration: "60s"

models:
  asr:
    path: "${asr_path}"
    type: "whisper"
    threads: 4
  llm:
    path: "${llm_path}"
    context_size: 32768
    gpu_layers: 0
    threads: 4

hotkey:
  trigger: "ctrl+shift+space"
  mode: "push-to-talk"

injection:
  method: "keyboard"
EOF
}

setup_models() {
    local sussurro_dir="$HOME/.sussurro"
    local models_dir="$sussurro_dir/models"
    local config_file="$sussurro_dir/config.yaml"

    mkdir -p "$models_dir"

    header "AI models"
    if [ -f "$config_file" ] \
       && [ -f "$models_dir/qwen3-sussurro-q4_k_m.gguf" ] \
       && { [ -f "$models_dir/ggml-small.bin" ] || [ -f "$models_dir/ggml-large-v3-turbo.bin" ]; }; then
        success "Models already present in $models_dir — skipping download."
        return 0
    fi

    info "Sussurro needs two models: a Whisper ASR model and the Qwen 3 LLM (~1.28 GB)."
    info "  [1] Whisper Small         (~488 MB)  faster, lower memory usage"
    info "  [2] Whisper Large v3 Turbo (~1.62 GB) slower, higher accuracy"
    local choice
    choice=$(prompt "Choose Whisper model [1/2] (default: 1, or 's' to skip): ")
    choice="${choice:-1}"

    if [ "$choice" = "s" ] || [ "$choice" = "S" ]; then
        warn "Skipping model download. Run 'sussurro' from a terminal later to fetch them."
        return 0
    fi

    local asr_url asr_path
    case "$choice" in
        2)
            asr_url="$URL_ASR_LARGE"
            asr_path="$models_dir/ggml-large-v3-turbo.bin"
            info "Selected: Whisper Large v3 Turbo"
            ;;
        *)
            asr_url="$URL_ASR_SMALL"
            asr_path="$models_dir/ggml-small.bin"
            info "Selected: Whisper Small"
            ;;
    esac

    local llm_path="$models_dir/qwen3-sussurro-q4_k_m.gguf"

    if [ ! -f "$asr_path" ]; then
        info "Downloading ASR model..."
        download "$asr_url" "$asr_path" \
            || { rm -f "$asr_path"; die "ASR model download failed."; }
    else
        success "ASR model already present."
    fi

    if [ ! -f "$llm_path" ]; then
        info "Downloading LLM model (this is the larger one — please be patient)..."
        download "$URL_LLM" "$llm_path" \
            || { rm -f "$llm_path"; die "LLM model download failed."; }
    else
        success "LLM model already present."
    fi

    if [ ! -f "$config_file" ]; then
        info "Writing default config to $config_file..."
        write_default_config "$config_file" "$asr_path" "$llm_path"
    else
        # Make sure the configured ASR path matches what we just downloaded.
        if ! grep -q "$(basename "$asr_path")" "$config_file"; then
            info "Updating $config_file to point at the chosen ASR model..."
            local tmpcfg
            tmpcfg=$(mktemp)
            sed -E "s|ggml-(small|large-v3-turbo)\.bin|$(basename "$asr_path")|g" "$config_file" > "$tmpcfg"
            mv "$tmpcfg" "$config_file"
        fi
    fi

    success "Models ready."
}

# ── macOS install ────────────────────────────────────────────────────────────
install_macos() {
    local extracted_root="$1" version="$2"

    local source_app="${extracted_root}/Sussurro.app"
    local source_bin="${extracted_root}/${BINARY}"

    if [ ! -d "$source_app" ]; then
        die "Sussurro.app not found in archive (expected ${source_app}). The release may be from an older version."
    fi

    # Pick destination for the .app bundle.
    local apps_dir
    if [ -w "/Applications" ] || sudo -n true 2>/dev/null; then
        apps_dir="/Applications"
    else
        apps_dir="$HOME/Applications"
        mkdir -p "$apps_dir"
    fi
    local dest_app="${apps_dir}/Sussurro.app"

    info "Installing Sussurro.app to ${apps_dir}..."

    # Remove any previous bundle so the copy is clean (avoids stale resources).
    if [ "$apps_dir" = "/Applications" ] && [ ! -w "/Applications" ]; then
        sudo rm -rf "$dest_app"
        sudo cp -R "$source_app" "$dest_app"
    else
        rm -rf "$dest_app"
        cp -R "$source_app" "$dest_app"
    fi

    # Strip macOS quarantine so Gatekeeper doesn't block the unsigned binary.
    info "Removing macOS quarantine flag..."
    if [ "$apps_dir" = "/Applications" ] && [ ! -w "/Applications" ]; then
        sudo xattr -dr com.apple.quarantine "$dest_app" 2>/dev/null || true
    else
        xattr -dr com.apple.quarantine "$dest_app" 2>/dev/null || true
    fi

    # Install or symlink the CLI binary so `sussurro` works from a terminal.
    local cli_dest="/usr/local/bin/${BINARY}"
    local fallback_cli_dest="$HOME/.local/bin/${BINARY}"
    local cli_target="${dest_app}/Contents/MacOS/${BINARY}"

    info "Installing ${BINARY} CLI command..."
    if [ -w "/usr/local/bin" ] || sudo -n true 2>/dev/null; then
        if [ -w "/usr/local/bin" ]; then
            ln -sfn "$cli_target" "$cli_dest"
        else
            sudo ln -sfn "$cli_target" "$cli_dest"
        fi
        ensure_in_path "/usr/local/bin"
    else
        mkdir -p "$(dirname "$fallback_cli_dest")"
        ln -sfn "$cli_target" "$fallback_cli_dest"
        cli_dest="$fallback_cli_dest"
        ensure_in_path "$(dirname "$fallback_cli_dest")"
    fi
    success "${BINARY} CLI available at ${cli_dest}"

    # Pre-download models so launching from Launchpad / Spotlight just works.
    setup_models

    success "Sussurro ${version} installed!"
    printf "\n${BOLD}Launch${RESET}\n"
    printf "  Spotlight or Launchpad → ${CYAN}Sussurro${RESET}\n"
    printf "  Or from a terminal:    ${CYAN}sussurro${RESET}\n"
    printf "  Default hotkey:        ${CYAN}Cmd+Shift+Space${RESET}\n\n"
    warn "On first launch, macOS may ask for Microphone and Accessibility permissions."
    warn "Grant both in System Settings → Privacy & Security."
}

# ── Linux install ────────────────────────────────────────────────────────────
install_linux() {
    local extracted_root="$1" version="$2"

    local source_bin="${extracted_root}/${BINARY}"
    local source_desktop="${extracted_root}/desktop/sussurro.desktop"
    local source_icons_dir="${extracted_root}/desktop/icons-hicolor"

    [ -f "$source_bin" ] \
        || die "Binary not found in archive at ${source_bin}."

    # Pick the binary install dir.
    local bin_dir
    if [ -w "/usr/local/bin" ] || sudo -n true 2>/dev/null; then
        bin_dir="/usr/local/bin"
    else
        bin_dir="$HOME/.local/bin"
        mkdir -p "$bin_dir"
    fi

    local bin_dest="${bin_dir}/${BINARY}"
    info "Installing binary to ${bin_dest}..."
    if [ "$bin_dir" = "/usr/local/bin" ] && [ ! -w "/usr/local/bin" ]; then
        sudo install -m 755 "$source_bin" "$bin_dest"
    else
        install -m 755 "$source_bin" "$bin_dest"
    fi
    ensure_in_path "$bin_dir"

    # Pick destinations for the .desktop entry and icons.
    local apps_dir icons_dir
    if [ "$bin_dir" = "/usr/local/bin" ]; then
        apps_dir="/usr/share/applications"
        icons_dir="/usr/share/icons/hicolor"
    else
        apps_dir="$HOME/.local/share/applications"
        icons_dir="$HOME/.local/share/icons/hicolor"
    fi
    mkdir -p "$apps_dir" "$icons_dir" 2>/dev/null || true

    # Install the .desktop entry.
    if [ -f "$source_desktop" ]; then
        info "Installing desktop entry to ${apps_dir}/sussurro.desktop..."
        if [ ! -w "$apps_dir" ] && sudo -n true 2>/dev/null; then
            sudo install -m 644 "$source_desktop" "${apps_dir}/sussurro.desktop"
        else
            install -m 644 "$source_desktop" "${apps_dir}/sussurro.desktop" 2>/dev/null \
                || sudo install -m 644 "$source_desktop" "${apps_dir}/sussurro.desktop"
        fi
    else
        warn "Desktop entry missing in archive; Sussurro won't appear in your app menu."
    fi

    # Install icons.
    if [ -d "$source_icons_dir" ]; then
        info "Installing hicolor icons to ${icons_dir}..."
        if [ ! -w "$icons_dir" ] && sudo -n true 2>/dev/null; then
            sudo cp -R "${source_icons_dir}/." "${icons_dir}/"
        else
            cp -R "${source_icons_dir}/." "${icons_dir}/" 2>/dev/null \
                || sudo cp -R "${source_icons_dir}/." "${icons_dir}/"
        fi
    else
        warn "Icons missing in archive; Sussurro will use a generic application icon."
    fi

    # Refresh desktop & icon caches so the new entry shows up immediately.
    if command -v update-desktop-database >/dev/null 2>&1; then
        info "Refreshing desktop database..."
        if [ -w "$apps_dir" ]; then
            update-desktop-database "$apps_dir" >/dev/null 2>&1 || true
        else
            sudo update-desktop-database "$apps_dir" >/dev/null 2>&1 || true
        fi
    fi
    if command -v gtk-update-icon-cache >/dev/null 2>&1; then
        info "Refreshing icon cache..."
        if [ -w "$icons_dir" ]; then
            gtk-update-icon-cache -q "$icons_dir" 2>/dev/null || true
        else
            sudo gtk-update-icon-cache -q "$icons_dir" 2>/dev/null || true
        fi
    fi

    # Pre-download models so launching from the app menu just works.
    setup_models

    success "Sussurro ${version} installed!"
    printf "\n${BOLD}Launch${RESET}\n"
    printf "  From your app menu     ${CYAN}Sussurro${RESET}\n"
    printf "  Or from a terminal:    ${CYAN}sussurro${RESET}\n"
    printf "  Default hotkey:        ${CYAN}Ctrl+Shift+Space${RESET}\n\n"

    if [ "${XDG_SESSION_TYPE:-}" = "wayland" ] || [ -n "${WAYLAND_DISPLAY:-}" ]; then
        warn "Wayland detected: bind Ctrl+Shift+Space to your session's"
        warn "  trigger script (see docs/wayland.md) for hotkey support."
    fi
}

# ── main ──────────────────────────────────────────────────────────────────────
main() {
    header "Sussurro installer"

    local platform
    platform=$(detect_platform)
    info "Detected platform: ${platform}"

    info "Fetching latest release..."
    local version
    version=$(fetch_latest_version)
    info "Latest version: ${version}"

    local archive_name="${BINARY}-${platform}.tar.gz"
    local download_url="https://github.com/${REPO}/releases/download/${version}/${archive_name}"

    local tmpdir
    tmpdir=$(mktemp -d)
    trap 'rm -rf "$tmpdir"' EXIT

    info "Downloading ${archive_name}..."
    download "$download_url" "${tmpdir}/${archive_name}" \
        || die "Download failed. Make sure a release for '${platform}' exists at:\n  ${download_url}"

    # Sanity check: at least 1 KB.
    local sz
    sz=$(wc -c < "${tmpdir}/${archive_name}")
    [ "$sz" -gt 1024 ] || die "Downloaded file looks corrupt (only ${sz} bytes)."

    info "Extracting..."
    tar -xzf "${tmpdir}/${archive_name}" -C "$tmpdir"

    local extracted_root="${tmpdir}/${BINARY}-${platform}"
    [ -d "$extracted_root" ] \
        || die "Archive did not contain expected directory: ${BINARY}-${platform}/"

    case "$platform" in
        macos-*) install_macos "$extracted_root" "$version" ;;
        linux-*) install_linux "$extracted_root" "$version" ;;
        *)       die "Unsupported platform: $platform." ;;
    esac
}

main "$@"
