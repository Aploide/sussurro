#!/usr/bin/env bash
# Generate platform icons from internal/ui/assets/Logo.jpeg.
#
# macOS:
#   - release/icons/Sussurro.icns        (multi-size icon for the .app bundle)
#
# Linux:
#   - release/icons/sussurro.png         (512x512 master, used by .desktop)
#   - release/icons/hicolor/<size>/apps/sussurro.png  (hicolor theme tree)
#
# Usage: ./scripts/generate-icons.sh
set -euo pipefail

SRC="internal/ui/assets/Logo.jpeg"
OUT_DIR="release/icons"
MASTER="${OUT_DIR}/sussurro.png"

if [ ! -f "$SRC" ]; then
    echo "Error: source logo not found at $SRC" >&2
    exit 1
fi

mkdir -p "$OUT_DIR"

# ── master 1024x1024 PNG (resize source up so all derived sizes are sharp) ────
echo "Generating master PNG (1024x1024)..."
if command -v sips >/dev/null 2>&1; then
    # macOS path: sips is always present
    sips -s format png -z 1024 1024 "$SRC" --out "${OUT_DIR}/sussurro-1024.png" >/dev/null
elif command -v magick >/dev/null 2>&1; then
    magick "$SRC" -resize 1024x1024 "${OUT_DIR}/sussurro-1024.png"
elif command -v convert >/dev/null 2>&1; then
    convert "$SRC" -resize 1024x1024 "${OUT_DIR}/sussurro-1024.png"
else
    echo "Error: need sips (macOS) or ImageMagick (magick/convert) to generate icons." >&2
    exit 1
fi

# 512px master used by the Linux .desktop entry and as a generic fallback
cp "${OUT_DIR}/sussurro-1024.png" "${OUT_DIR}/sussurro-master.png"
if command -v sips >/dev/null 2>&1; then
    sips -s format png -z 512 512 "${OUT_DIR}/sussurro-master.png" --out "$MASTER" >/dev/null
elif command -v magick >/dev/null 2>&1; then
    magick "${OUT_DIR}/sussurro-master.png" -resize 512x512 "$MASTER"
else
    convert "${OUT_DIR}/sussurro-master.png" -resize 512x512 "$MASTER"
fi
rm -f "${OUT_DIR}/sussurro-master.png"

# ── Linux hicolor icon tree ──────────────────────────────────────────────────
echo "Generating Linux hicolor icon set..."
for size in 16 24 32 48 64 128 256 512; do
    dest_dir="${OUT_DIR}/hicolor/${size}x${size}/apps"
    mkdir -p "$dest_dir"
    dest="${dest_dir}/sussurro.png"
    if command -v sips >/dev/null 2>&1; then
        sips -s format png -z "$size" "$size" "${OUT_DIR}/sussurro-1024.png" --out "$dest" >/dev/null
    elif command -v magick >/dev/null 2>&1; then
        magick "${OUT_DIR}/sussurro-1024.png" -resize "${size}x${size}" "$dest"
    else
        convert "${OUT_DIR}/sussurro-1024.png" -resize "${size}x${size}" "$dest"
    fi
done

# ── macOS .icns ──────────────────────────────────────────────────────────────
# Only build .icns when iconutil is available (macOS host). Linux runners just
# skip this step — the .icns is only needed for macOS releases.
if command -v iconutil >/dev/null 2>&1; then
    echo "Generating Sussurro.icns..."
    ICONSET="${OUT_DIR}/Sussurro.iconset"
    rm -rf "$ICONSET"
    mkdir -p "$ICONSET"

    # Apple's required iconset sizes & names.
    # https://developer.apple.com/library/archive/documentation/GraphicsAnimation/Conceptual/HighResolutionOSX/Optimizing/Optimizing.html
    declare -a entries=(
        "16:icon_16x16.png"
        "32:icon_16x16@2x.png"
        "32:icon_32x32.png"
        "64:icon_32x32@2x.png"
        "128:icon_128x128.png"
        "256:icon_128x128@2x.png"
        "256:icon_256x256.png"
        "512:icon_256x256@2x.png"
        "512:icon_512x512.png"
        "1024:icon_512x512@2x.png"
    )
    for entry in "${entries[@]}"; do
        size="${entry%%:*}"
        name="${entry#*:}"
        sips -s format png -z "$size" "$size" "${OUT_DIR}/sussurro-1024.png" --out "${ICONSET}/${name}" >/dev/null
    done

    iconutil -c icns "$ICONSET" -o "${OUT_DIR}/Sussurro.icns"
    rm -rf "$ICONSET"
    echo "  ✓ ${OUT_DIR}/Sussurro.icns"
else
    echo "  (skipping .icns generation — iconutil not available on this host)"
fi

# Drop the temporary 1024 master once derived sizes exist
rm -f "${OUT_DIR}/sussurro-1024.png"

echo "Done."
echo "  - ${MASTER}"
echo "  - ${OUT_DIR}/hicolor/<size>/apps/sussurro.png"
[ -f "${OUT_DIR}/Sussurro.icns" ] && echo "  - ${OUT_DIR}/Sussurro.icns"
